// Package app holds the application services (use cases). Each service is a
// thin orchestrator that depends only on domain ports; infrastructure adapters
// are injected at the composition root.
package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// TokenRefresher renews OAuth access tokens on a connection when needed.
// Optional; nil means no refresh.
type TokenRefresher interface {
	EnsureAccess(ctx context.Context, conn *domain.Connection) error
}

// RouterService is the core use case: take a chat request (in OpenAI format),
// route it to the right upstream(s), and return the response.
type RouterService struct {
	Combos      domain.ComboRepo
	Connections domain.ConnectionRepo
	Executor    domain.Executor
	Translator  domain.Translator
	Usage       domain.UsageRepo
	// Tokens is optional OAuth refresh before upstream calls.
	Tokens TokenRefresher
	// Cache is optional response cache (direct-hash). Nil disables caching.
	Cache *CacheService
	// Compressor is optional request body compressor (RTK). Nil disables
	// compression. When set, tool_result content is compressed before the
	// upstream call to reduce input tokens.
	Compressor domain.RequestCompressor
	// Savings tracks cumulative token/byte savings from cache hits and RTK
	// compression. Nil disables tracking.
	Savings *SavingsTracker
	// Models is the persisted model catalog. Pricing is resolved during
	// model sync (by ModelSyncService) and stored in the database. The
	// hot path reads pricing from an in-memory cache (Pricing) — no DB or
	// registry lookup per request.
	Models domain.ModelRepo

	// comboRotation is in-memory state for round-robin combo strategy.
	// Not persisted; rotation resets on process restart (acceptable).
	rotationMu sync.Mutex
	rotation   map[string]int

	// Health tracks per-(combo, model, connection) failures so that
	// subsequent requests skip unhealthy keys and a background probe
	// restores them when they recover. Not persisted; resets on restart.
	Health *HealthTracker

	// Pricing is the in-memory pricing cache. Nil-safe on the hot path.
	Pricing *PricingCache
	// Selector owns the provider config cache and connection rotation.
	// Nil-safe: callers fall back to a default openai config.
	Selector *ConnectionSelector
	// Prober owns background health probing. Nil-safe: disables probing.
	Prober *HealthProber
	// TPS is the in-memory tokens-per-second cache used by the velocity
	// strategy. Nil-safe: the strategy falls back to the configured order.
	TPS *TPSCache
	// TPSProber measures TPS for models with no usage data so the velocity
	// strategy can rank them. Nil-safe: probing is skipped.
	TPSProber *TPSProber
	// Strategies resolves a combo's strategy name to a ComboStrategy. Set
	// in NewRouterService; nil-safe (routeCombo falls back to ordered).
	Strategies *StrategyRegistry
}

// probeCtxKey is used to mark a context as originating from a health probe
// so that test doubles (mock executors) can distinguish probe calls from
// real request calls and avoid polluting call snapshots.
type probeCtxKey struct{}

// maxComboDepth caps how deeply the router will recurse into nested combos.
// The dashboard validation (ComboService.detectCycle) already rejects cycles
// at save time; this is a safety net for manually-edited data.
const maxComboDepth = 5

// IsProbeCall reports whether the given context originated from a health
// probe. Exported for test doubles.
func IsProbeCall(ctx context.Context) bool {
	v, _ := ctx.Value(probeCtxKey{}).(bool)
	return v
}

// NewRouterService constructs a RouterService with the round-robin state
// initialised. Use this rather than a bare struct literal.
func NewRouterService(combos domain.ComboRepo, conns domain.ConnectionRepo, exec domain.Executor, tr domain.Translator, usage domain.UsageRepo) *RouterService {
	s := &RouterService{
		Combos:      combos,
		Connections: conns,
		Executor:    exec,
		Translator:  tr,
		Usage:       usage,
		rotation:    map[string]int{},
		Health:      NewHealthTracker(),
		Selector:    NewConnectionSelector(nil, nil),
	}
	s.Prober = NewHealthProber(s.Health, conns, exec, tr, s.Selector)
	s.TPSProber = NewTPSProber(nil, s)
	s.Strategies = NewStrategyRegistry(s)
	return s
}

// RouteOptions tunes how a chat request is processed. The zero value is
// sensible: OpenAI as the client format, chat as the endpoint, no passthrough.
type RouteOptions struct {
	InputFormat domain.Format // client format of the request body; FormatOpenAI when unset
	Endpoint    string        // "" = chat (format-based URL); "embeddings" | "images/generations" | ...
	ContentType string        // for multipart passthrough bodies
}

// RouteChat handles a chat/completions-style request. The body is in the
// client's format (opts.InputFormat, OpenAI by default). The router translates
// to the target provider's format, executes, and translates the response back
// to the client's format. modelStr is the model extracted by the handler so
// we avoid a second json.Unmarshal on the hot path. apiKey is the client-facing
// key (for usage tracking); empty when key auth is not required.
func (s *RouterService) RouteChat(ctx context.Context, body []byte, modelStr string, stream bool, apiKey string, opts RouteOptions) (*RouterResponse, error) {
	if opts.InputFormat == "" {
		opts.InputFormat = domain.FormatOpenAI
	}
	// Cache lookup: short-circuit on hit. Only for chat (endpoint=="") and
	// only when cache is enabled and the request doesn't opt out.
	if s.Cache != nil && s.Cache.Enabled() && opts.Endpoint == "" && !isCacheDisabled(ctx) {
		cacheKey := s.Cache.ComputeKey(body, modelStr, opts.InputFormat)
		if cached, ok := s.Cache.Lookup(ctx, cacheKey); ok {
			var prompt, completion, cacheRead, cacheCreation int
			if cached.Stream {
				prompt, completion, cacheRead, cacheCreation = parseUsageFromSSEFull(cached.StreamChunks)
			} else {
				prompt, completion, cacheRead, cacheCreation = parseUsageFromJSONFull(cached.Body)
			}
			// Calculate the real cost that was avoided by serving from cache.
			var costSaved float64
			var mid domain.ModelID
			if s.Pricing != nil {
				if m, ok := domain.SplitModelID(modelStr); ok {
					mid = m
					if pricing, ok := s.Pricing.Get(mid); ok {
						costSaved = CalculateCost(pricing, "", prompt, completion, cacheRead, cacheCreation)
					}
				}
			}
			// Record a usage entry so savings survive restarts and can be
			// aggregated per model/combo. Cache hits have zero cost (no
			// upstream call) and zero latency.
			go s.recordCacheHitUsage(mid, modelStr, apiKey, prompt, completion, costSaved)
			if s.Savings != nil {
				s.Savings.RecordCacheHit(prompt+completion, costSaved)
			}
			if cached.Stream {
				return &RouterResponse{
					StatusCode: cached.StatusCode,
					Headers:    cached.Headers,
					Body:       io.NopCloser(bytes.NewReader(cached.StreamChunks)),
					Stream:     true,
					Cached:     true,
				}, nil
			}
			return &RouterResponse{
				StatusCode: cached.StatusCode,
				Headers:    cached.Headers,
				Body:       io.NopCloser(bytes.NewReader(cached.Body)),
				Stream:     false,
				Cached:     true,
			}, nil
		}
		// Stash the key so the response path can store the result.
		ctx = withCacheKey(ctx, cacheKey)
	}
	modelID, ok := domain.SplitModelID(modelStr)
	if ok {
		return s.routeSingle(ctx, modelID, body, stream, apiKey, opts, "")
	}
	combo, err := s.Combos.GetByName(ctx, modelStr)
	if err == domain.ErrNotFound {
		return nil, fmt.Errorf("model %q not found", modelStr)
	}
	if err != nil {
		return nil, err
	}
	return s.routeCombo(ctx, combo, body, stream, apiKey, opts, "", 0, nil)
}

// RoutePassthrough routes a non-chat endpoint (embeddings, images) to a
// single upstream connection. The body stays in OpenAI format — no
// translation is applied. Combos are supported via model-name lookup just
// like chat. endpoint is "embeddings" or "images/generations".
func (s *RouterService) RoutePassthrough(ctx context.Context, body []byte, modelStr string, endpoint string, apiKey string, contentType string) (*RouterResponse, error) {
	opts := RouteOptions{InputFormat: domain.FormatOpenAI, Endpoint: endpoint, ContentType: contentType}
	modelID, ok := domain.SplitModelID(modelStr)
	if ok {
		return s.routeSingle(ctx, modelID, body, false, apiKey, opts, endpoint, contentType)
	}
	combo, err := s.Combos.GetByName(ctx, modelStr)
	if err == domain.ErrNotFound {
		return nil, fmt.Errorf("model %q not found", modelStr)
	}
	if err != nil {
		return nil, err
	}
	return s.routeCombo(ctx, combo, body, false, apiKey, opts, endpoint, 0, nil, contentType)
}

// RouterResponse is what the HTTP handler receives. It is either a buffered
// JSON body (non-stream) or a ReadCloser yielding SSE (stream). The caller
// must close Body if non-nil.
type RouterResponse struct {
	StatusCode  int
	Headers     http.Header
	Body        io.ReadCloser
	Stream      bool
	Provider    string
	Model       string
	ConnectionID string
	// Cached is true when the response came from the response cache.
	Cached bool
	// RTK savings — populated by executeOne when RTK compression reduced
	// the request body. Propagated to recordUsage for persistence.
	RTKBytesSaved  int
	RTKTokensSaved int
	RTKCostSaved   float64
}

func (s *RouterService) routeSingle(ctx context.Context, m domain.ModelID, body []byte, stream bool, apiKey string, opts RouteOptions, endpoint string, contentType ...string) (*RouterResponse, error) {
	start := time.Now()
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	conns, err := s.Connections.ListByProvider(ctx, m.Provider)
	if err != nil {
		return nil, err
	}
	if len(conns) == 0 {
		return nil, domain.ErrNoConnection
	}
	modelStr := m.Provider + "/" + m.Model
	startIdx := 0
	if s.Selector != nil {
		startIdx = s.Selector.StartIndex(conns)
	}
	for i := 0; i < len(conns); i++ {
		conn := &conns[(startIdx+i)%len(conns)]
		if !conn.IsActive || conn.RateLimitedUntil.After(time.Now()) {
			continue
		}
		if s.Health.IsUnhealthy("", modelStr, conn.ID) {
			if s.Prober != nil && s.Health.TryStartProbe("", modelStr, conn.ID) {
				go s.Prober.RunProbe("", modelStr, m, conn.ID)
			}
			continue
		}
		res, err := s.executeOne(ctx, m, conn, body, stream, opts, ct)
		if err != nil {
			s.Health.MarkUnhealthy("", modelStr, conn.ID)
			if s.Prober != nil && s.Health.TryStartProbe("", modelStr, conn.ID) {
				go s.Prober.RunProbe("", modelStr, m, conn.ID)
			}
			continue
		}
		if endpoint == "" && res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
			s.Health.MarkUnhealthy("", modelStr, conn.ID)
			s.markRateLimited(ctx, conn, res)
			if s.Prober != nil && s.Health.TryStartProbe("", modelStr, conn.ID) {
				go s.Prober.RunProbe("", modelStr, m, conn.ID)
			}
			if res.Body != nil {
				res.Body.Close()
			}
			continue
		}
		s.Health.MarkHealthy("", modelStr, conn.ID)
		s.wrapUsageTracking(ctx, res, m, conn, apiKey, endpoint, nil, start)
		res.Provider = m.Provider
		res.Model = m.Model
		res.ConnectionID = conn.ID
		return res, nil
	}
	return nil, fmt.Errorf("%w: provider %q", domain.ErrNoConnection, m.Provider)
}

// singleCompletion is a helper used by internal features (such as the
// intelligence strategy classifier) to issue a non-streaming chat completion
// call in OpenAI format and return the assistant text response.
func (s *RouterService) singleCompletion(ctx context.Context, modelStr string, messages []map[string]any, apiKey string) (string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model":       modelStr,
		"messages":    messages,
		"max_tokens":  500,
		"temperature": 0.0,
	})
	if err != nil {
		return "", err
	}
	res, err := s.RouteChat(ctx, reqBody, modelStr, false, apiKey, RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("classifier returned status %d", res.StatusCode)
	}
	buf, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(buf, &resp); err != nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("classifier response unparseable: %s", string(buf))
	}
	return resp.Choices[0].Message.Content, nil
}

// measureModelTPS sends a standardized test prompt to the given model and
// returns the assistant text, the completion token count, and any error.
// It is used by TPSProber to measure a model's throughput for the velocity
// strategy. The call goes through the normal routing path (connection
// selection, translation, execution) so usage is recorded in the DB — this
// means future cache refreshes will also pick up the measured TPS organically.
func (s *RouterService) measureModelTPS(ctx context.Context, modelStr string, apiKey string) (text string, completionTokens int, err error) {
	reqBody, err := json.Marshal(map[string]any{
		"model":       modelStr,
		"messages":    tpsProbeMessages,
		"max_tokens":  tpsProbeMaxTokens,
		"temperature": 0.0,
	})
	if err != nil {
		return "", 0, err
	}
	res, err := s.RouteChat(ctx, reqBody, modelStr, false, apiKey, RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", 0, fmt.Errorf("tps probe upstream status %d", res.StatusCode)
	}
	buf, err := io.ReadAll(res.Body)
	if err != nil {
		return "", 0, err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(buf, &resp); err != nil {
		return "", 0, fmt.Errorf("tps probe response unparseable: %s", string(buf))
	}
	if len(resp.Choices) == 0 {
		return "", 0, fmt.Errorf("tps probe returned no choices")
	}
	tokens := 0
	if resp.Usage != nil {
		tokens = resp.Usage.CompletionTokens
	}
	return resp.Choices[0].Message.Content, tokens, nil
}

// measureModelTPSStreaming sends a standardized streaming test prompt and
// measures TTFT (time to first token) separately from the total time. This
// lets TPSProber calculate the real generation TPS (excluding TTFT) so the
// velocity strategy ranks models by their actual throughput, not penalized
// by high prefill latency. Returns (text, completionTokens, ttftMs, err).
func (s *RouterService) measureModelTPSStreaming(ctx context.Context, modelStr string, apiKey string) (text string, completionTokens int, ttftMs int64, err error) {
	reqBody, err := json.Marshal(map[string]any{
		"model":           modelStr,
		"messages":        tpsProbeMessages,
		"max_tokens":      tpsProbeMaxTokens,
		"temperature":     0.0,
		"stream":          true,
		"stream_options":  map[string]any{"include_usage": true},
	})
	if err != nil {
		return "", 0, 0, err
	}
	res, err := s.RouteChat(ctx, reqBody, modelStr, true, apiKey, RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		return "", 0, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", 0, 0, fmt.Errorf("tps probe upstream status %d", res.StatusCode)
	}

	start := time.Now()
	reader := bufio.NewReader(res.Body)
	var accumulated strings.Builder
	var firstByteAt time.Time

	for {
		line, rErr := reader.ReadSlice('\n')
		if len(line) > 0 && firstByteAt.IsZero() {
			firstByteAt = time.Now()
		}
		if rErr != nil {
			break
		}
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(trimmed, []byte("data: "))
		if bytes.Equal(data, []byte("[DONE]")) {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if jErr := json.Unmarshal(data, &chunk); jErr != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			accumulated.WriteString(chunk.Choices[0].Delta.Content)
		}
		if chunk.Usage != nil {
			completionTokens = chunk.Usage.CompletionTokens
		}
	}

	if !firstByteAt.IsZero() {
		ttftMs = firstByteAt.Sub(start).Milliseconds()
	}
	return accumulated.String(), completionTokens, ttftMs, nil
}

func (s *RouterService) routeCombo(ctx context.Context, combo *domain.Combo, body []byte, stream bool, apiKey string, opts RouteOptions, endpoint string, depth int, comboChain []string, contentType ...string) (*RouterResponse, error) {
	start := time.Now()
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	models := combo.Models
	if s.Strategies != nil {
		strat := s.Strategies.For(combo.Strategy)
		ordered, err := strat.Order(ctx, StrategyRequest{Combo: combo, Body: body, APIKey: apiKey})
		if err != nil {
			slog.Warn("combo strategy failed; using configured order", "combo", combo.Name, "strategy", combo.Strategy, "err", err)
		} else if len(ordered) > 0 {
			// Log when the strategy reorders models (intelligence classifier
			// chose a different first model than the configured order).
			if ordered[0] != combo.Models[0] && combo.Strategy == StrategyIntelligence {
				slog.Info("intelligence classifier chose different model", "combo", combo.Name, "configured_first", combo.Models[0], "chosen_first", ordered[0])
			}
			models = ordered
		}
	}
	var lastErr error
	var skipped []skipEntry // models where all connections are unhealthy
	for _, modelStr := range models {
		m, ok := domain.SplitModelID(modelStr)
		if !ok {
			// No "/" → treat as a nested combo. Resolved here so the
			// nested combo's own strategy is applied transparently.
			if depth >= maxComboDepth {
				lastErr = fmt.Errorf("combo nesting depth limit reached at %q", combo.Name)
				slog.Warn("combo nesting depth limit reached", "combo", combo.Name, "depth", depth)
				continue
			}
			nested, err := s.Combos.GetByName(ctx, modelStr)
			if err != nil {
				lastErr = fmt.Errorf("combo model %q invalid: %w", modelStr, err)
				continue
			}
			nestedChain := append(append([]string{}, comboChain...), combo.Name)
			res, err := s.routeCombo(ctx, nested, body, stream, apiKey, opts, endpoint, depth+1, nestedChain, ct)
			if err != nil {
				slog.Warn("combo fallback: nested combo failed, trying next", "parent_combo", combo.Name, "failed_combo", modelStr, "err", err)
				lastErr = err
				continue
			}
			if res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
				slog.Warn("combo fallback: nested combo returned error status, trying next", "parent_combo", combo.Name, "failed_combo", modelStr, "status", res.StatusCode)
				lastErr = fmt.Errorf("upstream %d", res.StatusCode)
				if res.Body != nil {
					res.Body.Close()
				}
				continue
			}
			// No wrapUsageTracking here — the nested combo's routeCombo
			// already recorded usage with the full chain. The parent combo
			// gets credit via the combo_executions table.
			return res, nil
		}
		conns, err := s.Connections.ListByProvider(ctx, m.Provider)
		if err != nil {
			lastErr = err
			continue
		}
		// Check if ALL connections for this model are unhealthy. If so,
		// skip and launch background probes. tryModel handles the
		// per-connection health tracking internally.
		if s.allConnectionsUnhealthy(combo.Name, modelStr, conns) {
			if s.Prober != nil {
				s.Prober.LaunchProbes(combo.Name, modelStr, m, conns)
			}
			skipped = append(skipped, skipEntry{modelStr: modelStr, m: m, conns: conns})
			continue
		}
		childChain := append(append([]string{}, comboChain...), combo.Name)
		res, err := s.tryModelWithConns(ctx, m, conns, body, stream, apiKey, opts, childChain, start, true, ct)
		if err != nil {
			slog.Warn("combo fallback: model failed, trying next", "combo", combo.Name, "failed_model", modelStr, "err", err)
			lastErr = err
			continue
		}
		if res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
			slog.Warn("combo fallback: model returned error status, trying next", "combo", combo.Name, "failed_model", modelStr, "status", res.StatusCode)
			lastErr = fmt.Errorf("upstream %d", res.StatusCode)
			if res.Body != nil {
				res.Body.Close()
			}
			continue
		}
		return res, nil
	}
	// Last resort: every model's connections are all unhealthy (or no
	// healthy model worked). Retry the skipped models inline — a real
	// request can succeed where the probe hasn't run yet.
	for _, sk := range skipped {
		skippedChain := append(append([]string{}, comboChain...), combo.Name)
		res, err := s.tryModelWithConns(ctx, sk.m, sk.conns, body, stream, apiKey, opts, skippedChain, start, false, ct)
		if err != nil {
			lastErr = err
			continue
		}
		if res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
			lastErr = fmt.Errorf("upstream %d", res.StatusCode)
			if res.Body != nil {
				res.Body.Close()
			}
			continue
		}
		return res, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrAllModelsFailed, lastErr)
	}
	return nil, domain.ErrAllModelsFailed
}

// skipEntry bundles the data needed for the last-resort retry pass so we
// don't re-resolve connections for each skipped model.
type skipEntry struct {
	modelStr string
	m        domain.ModelID
	conns    []domain.Connection
}

// allConnectionsUnhealthy checks whether every active connection for the
// given model is currently marked unhealthy for this (combo, model). If at
// least one connection is healthy (or not yet tried), returns false.
func (s *RouterService) allConnectionsUnhealthy(comboName, modelStr string, conns []domain.Connection) bool {
	if len(conns) == 0 {
		return true
	}
	for i := range conns {
		conn := &conns[i]
		if !conn.IsActive || conn.RateLimitedUntil.After(time.Now()) {
			continue
		}
		if !s.Health.IsUnhealthy(comboName, modelStr, conn.ID) {
			return false
		}
	}
	return true
}

func (s *RouterService) tryModel(ctx context.Context, m domain.ModelID, body []byte, stream bool, apiKey string, opts RouteOptions, comboChain []string, start time.Time, contentType ...string) (*RouterResponse, error) {
	conns, err := s.Connections.ListByProvider(ctx, m.Provider)
	if err != nil {
		return nil, err
	}
	return s.tryModelWithConns(ctx, m, conns, body, stream, apiKey, opts, comboChain, start, true, contentType...)
}

// tryModelForceTries iterates connections without skipping unhealthy ones.
// Used in the last-resort pass: a real request can succeed where a probe
// hasn't run yet (e.g. transient failure already resolved).
func (s *RouterService) tryModelForceTries(ctx context.Context, m domain.ModelID, body []byte, stream bool, apiKey string, opts RouteOptions, comboChain []string, start time.Time, contentType ...string) (*RouterResponse, error) {
	conns, err := s.Connections.ListByProvider(ctx, m.Provider)
	if err != nil {
		return nil, err
	}
	return s.tryModelWithConns(ctx, m, conns, body, stream, apiKey, opts, comboChain, start, false, contentType...)
}

// tryModelWithConns is the shared connection iteration logic. When skipUnhealthy
// is true, connections marked unhealthy are skipped and a background probe is
// launched. When false (last-resort), all active connections are tried inline
// regardless of health state.
func (s *RouterService) tryModelWithConns(ctx context.Context, m domain.ModelID, conns []domain.Connection, body []byte, stream bool, apiKey string, opts RouteOptions, comboChain []string, start time.Time, skipUnhealthy bool, contentType ...string) (*RouterResponse, error) {
	if len(conns) == 0 {
		return nil, fmt.Errorf("%w: provider %q", domain.ErrNoConnection, m.Provider)
	}
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	modelStr := m.Provider + "/" + m.Model
	// comboName for health tracking is the leaf combo (immediate parent
	// of the model). Empty for single-model requests.
	comboName := ""
	if len(comboChain) > 0 {
		comboName = comboChain[len(comboChain)-1]
	}
	startIdx := 0
	if s.Selector != nil {
		startIdx = s.Selector.StartIndex(conns)
	}
	for i := 0; i < len(conns); i++ {
		conn := &conns[(startIdx+i)%len(conns)]
		if !conn.IsActive || conn.RateLimitedUntil.After(time.Now()) {
			continue
		}
		if skipUnhealthy && s.Health.IsUnhealthy(comboName, modelStr, conn.ID) {
			if s.Prober != nil && s.Health.TryStartProbe(comboName, modelStr, conn.ID) {
				go s.Prober.RunProbe(comboName, modelStr, m, conn.ID)
			}
			continue
		}
		res, err := s.executeOne(ctx, m, conn, body, stream, opts, ct)
		if err != nil {
			s.Health.MarkUnhealthy(comboName, modelStr, conn.ID)
			if skipUnhealthy && s.Prober != nil {
				if s.Health.TryStartProbe(comboName, modelStr, conn.ID) {
					go s.Prober.RunProbe(comboName, modelStr, m, conn.ID)
				}
			}
			continue
		}
		if res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
			s.Health.MarkUnhealthy(comboName, modelStr, conn.ID)
			s.markRateLimited(ctx, conn, res)
			if skipUnhealthy && s.Prober != nil {
				if s.Health.TryStartProbe(comboName, modelStr, conn.ID) {
					go s.Prober.RunProbe(comboName, modelStr, m, conn.ID)
				}
			}
			if res.Body != nil {
				res.Body.Close()
			}
			continue
		}
		s.Health.MarkHealthy(comboName, modelStr, conn.ID)
		s.wrapUsageTracking(ctx, res, m, conn, apiKey, opts.Endpoint, comboChain, start)
		res.Provider = m.Provider
		res.Model = m.Model
		res.ConnectionID = conn.ID
		return res, nil
	}
	return nil, fmt.Errorf("%w: provider %q", domain.ErrNoConnection, m.Provider)
}

func (s *RouterService) executeOne(ctx context.Context, m domain.ModelID, conn *domain.Connection, body []byte, stream bool, opts RouteOptions, contentType string) (*RouterResponse, error) {
	if s.Tokens != nil && conn != nil && conn.RefreshToken != "" {
		if err := s.Tokens.EnsureAccess(ctx, conn); err != nil {
			slog.Warn("oauth refresh failed", "provider", conn.ProviderID, "err", err)
			// continue with existing token; upstream may 401
		}
	}

	cfg := &domain.ProviderConfig{ID: m.Provider, Format: domain.FormatOpenAI}
	if s.Selector != nil {
		cfg = s.Selector.Config(m.Provider)
	}

	translated := body
	var respBody io.ReadCloser
	if opts.Endpoint == "" {
		// Chat path: translate from client format to upstream format, then
		// back from upstream to client on the response.
		inputFmt := opts.InputFormat
		if inputFmt == "" {
			inputFmt = domain.FormatOpenAI
		}
		targetFmt := cfg.Format
		if targetFmt == "" || targetFmt == domain.FormatAuto {
			targetFmt = domain.FormatOpenAI
		}
		// 1) Client format -> OpenAI (our canonical translation pivot)
		if inputFmt != domain.FormatOpenAI {
			t, err := s.Translator.TranslateRequest(inputFmt, domain.FormatOpenAI, m.Model, body)
			if err != nil {
				return nil, err
			}
			body = t
		}
		if stream && targetFmt == domain.FormatOpenAI {
			body = injectStreamUsage(body)
		}
		// 2) OpenAI -> upstream format
		var err error
		translated, err = s.Translator.TranslateRequest(domain.FormatOpenAI, targetFmt, m.Model, body)
		if err != nil {
			return nil, err
		}
		// RTK: compress tool_result content in the translated body. Fail-open;
		// nil compressor or passthrough endpoint skips compression.
		var rtkBytesSaved, rtkTokensSaved int
		var rtkCostSaved float64
		if s.Compressor != nil {
			before := len(translated)
			translated = s.Compressor.Compress(translated)
			if len(translated) < before {
				rtkBytesSaved = before - len(translated)
				rtkTokensSaved = rtkBytesSaved / 4
				if s.Pricing != nil {
					if pricing, ok := s.Pricing.Get(m); ok {
						rtkCostSaved = float64(rtkTokensSaved) * pricing.InputCostPerToken
					}
				}
				if s.Savings != nil {
					s.Savings.RecordRTKCompression(rtkBytesSaved, rtkCostSaved)
				}
			}
		}
		execReq := domain.ExecuteRequest{
			ProviderID:   m.Provider,
			Connection:   conn,
			Config:       cfg,
			UpstreamModel: m.Model,
			Body:         io.NopCloser(bytes.NewReader(translated)),
			Stream:       stream,
		}
		slog.Info("executing upstream request", "provider", m.Provider, "model", m.Model, "payload", string(translated))
		res, err := s.Executor.Execute(ctx, execReq)
		if err != nil {
			return nil, err
		}
		if res.StatusCode >= 400 {
			bufErr, _ := io.ReadAll(res.Body)
			res.Body.Close()
			slog.Warn("upstream returned error status", "status", res.StatusCode, "provider", m.Provider, "model", m.Model, "response_body", string(bufErr), "request_payload", string(translated))
			res.Body = io.NopCloser(bytes.NewReader(bufErr))
		}
		// 3) Upstream format -> OpenAI
		openaiBody := res.Body
		if res.Stream && targetFmt != domain.FormatOpenAI {
			openaiBody, err = s.Translator.TranslateResponseStream(ctx, targetFmt, domain.FormatOpenAI, res.Body)
			if err != nil {
				return nil, err
			}
		} else if !res.Stream && targetFmt != domain.FormatOpenAI {
			buf, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				return nil, err
			}
			t, err := s.Translator.TranslateResponseJSON(targetFmt, domain.FormatOpenAI, buf)
			if err != nil {
				return nil, err
			}
			openaiBody = io.NopCloser(bytes.NewReader(t))
		}
		// 4) OpenAI -> client format
		respBody = openaiBody
		if inputFmt != domain.FormatOpenAI {
			if res.Stream {
				respBody, err = s.Translator.TranslateResponseStream(ctx, domain.FormatOpenAI, inputFmt, openaiBody)
				if err != nil {
					return nil, err
				}
			} else {
				buf, err := io.ReadAll(openaiBody)
				openaiBody.Close()
				if err != nil {
					return nil, err
				}
				t, err := s.Translator.TranslateResponseJSON(domain.FormatOpenAI, inputFmt, buf)
				if err != nil {
					return nil, err
				}
				respBody = io.NopCloser(bytes.NewReader(t))
			}
		}
	return &RouterResponse{
		StatusCode:     res.StatusCode,
		Headers:        res.Headers,
		Body:           respBody,
		Stream:         res.Stream,
		RTKBytesSaved:  rtkBytesSaved,
		RTKTokensSaved: rtkTokensSaved,
		RTKCostSaved:   rtkCostSaved,
	}, nil
	}
	// Passthrough (endpoint != ""). For JSON bodies (embeddings, images,
	// audio/speech) we rewrite the model field via the OpenAI->OpenAI
	// translator. For multipart bodies (audio/transcriptions) we rewrite
	// the "model" form field to strip the provider prefix — the upstream
	// expects the bare model name (e.g. "whisper-1", not "openai/whisper-1").
	if len(translated) > 0 && translated[0] == '{' {
		var err error
		translated, err = s.Translator.TranslateRequest(domain.FormatOpenAI, domain.FormatOpenAI, m.Model, translated)
		if err != nil {
			return nil, err
		}
	} else if len(translated) > 0 && contentType != "" && strings.HasPrefix(contentType, "multipart/") {
		translated = rewriteMultipartModel(translated, m.Model)
	}
	execReq := domain.ExecuteRequest{
		ProviderID:   m.Provider,
		Connection:   conn,
		Config:       cfg,
		UpstreamModel: m.Model,
		Body:         io.NopCloser(bytes.NewReader(translated)),
		Stream:       false,
		Endpoint:     opts.Endpoint,
	}
	if contentType != "" {
		execReq.Headers = map[string]string{"Content-Type": contentType}
	}
	res, err := s.Executor.Execute(ctx, execReq)
	if err != nil {
		return nil, err
	}
	return &RouterResponse{
		StatusCode: res.StatusCode,
		Headers:    res.Headers,
		Body:       res.Body,
		Stream:     false,
	}, nil
}

// wrapUsageTracking wraps res.Body with a tee reader that copies response
// bytes into an in-memory buffer. When the body is closed (after the HTTP
// handler has finished writing to the client) the buffer is parsed for token
// usage and a UsageEntry is recorded. This keeps the hot path (streaming to
// the client) untouched while still capturing usage asynchronously.
// When a cache key is present in the context, the buffered response is also
// stored in the response cache.
func (s *RouterService) wrapUsageTracking(ctx context.Context, res *RouterResponse, m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, comboChain []string, start time.Time) {
	cacheEnabled := s.Cache != nil && s.Cache.Enabled()
	_, hasCacheKey := cacheKeyFromCtx(ctx)
	bufLimit := maxUsageBuf
	if cacheEnabled && hasCacheKey {
		bufLimit = maxCacheBuf
	}
	tee := &teeReadCloser{
		r:    res.Body,
		limit: bufLimit,
		start: start,
		onClose: func(buf []byte, ttftMs int64) {
			s.recordUsage(m, conn, apiKey, endpoint, res.StatusCode, res.Stream, buf, comboChain, start, ttftMs, res.RTKBytesSaved, res.RTKTokensSaved, res.RTKCostSaved)
			if cacheEnabled && hasCacheKey && res.StatusCode < 400 {
				if key, ok := cacheKeyFromCtx(ctx); ok {
					if res.Stream {
						s.Cache.StoreStream(ctx, key, res.StatusCode, res.Headers, buf)
					} else {
						s.Cache.Store(ctx, key, res.StatusCode, res.Headers, buf)
					}
				}
			}
		},
	}
	res.Body = tee
}

// recordUsage parses token counts from the buffered response body and writes
// a single UsageEntry. Uses a detached context (the request may be done by
// the time the body is closed). When the model has pricing data in the
// in-memory pricing cache, the dollar cost is calculated and recorded.
// comboChain is the list of combo names from root to leaf (e.g.
// ["coding", "medium"]). After inserting the usage entry, combo_executions
// rows are inserted so every combo in the chain gets credit.
func (s *RouterService) recordUsage(m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, status int, stream bool, buf []byte, comboChain []string, start time.Time, ttftMs int64, rtkBytes int, rtkTokens int, rtkCost float64) {
	prompt, completion, cacheRead, cacheCreation := 0, 0, 0, 0
	if endpoint == "" {
		endpoint = "chat/completions"
	}
	if status < 400 {
		if stream {
			prompt, completion, cacheRead, cacheCreation = parseUsageFromSSEFull(buf)
		} else {
			prompt, completion, cacheRead, cacheCreation = parseUsageFromJSONFull(buf)
		}
	}
	var cost float64
	if s.Pricing != nil {
		if pricing, ok := s.Pricing.Get(m); ok {
			cost = CalculateCost(pricing, endpoint, prompt, completion, cacheRead, cacheCreation)
		}
	}
	var connID string
	if conn != nil {
		connID = conn.ID
	}
	entry := domain.UsageEntry{
		Timestamp:        time.Now(),
		Provider:         m.Provider,
		Model:            m.Model,
		ConnectionID:     connID,
		ApiKey:           apiKey,
		Endpoint:         endpoint,
		LatencyMs:        time.Since(start).Milliseconds(),
		TTFTMs:           ttftMs,
		PromptTokens:     prompt,
		CompletionTokens:  completion,
		Cost:             cost,
		Status:           status,
		RTKCompressed:    rtkBytes > 0,
		RTKBytesSaved:    rtkBytes,
		RTKTokensSaved:   rtkTokens,
		RTKCostSaved:     rtkCost,
		ComboChain:       comboChain,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Usage.Record(ctx, &entry)
}

// recordCacheHitUsage writes a UsageEntry for a cache hit so savings data
// survives restarts and can be aggregated per model/combo/period. Runs in a
// goroutine — never blocks the request path.
func (s *RouterService) recordCacheHitUsage(m domain.ModelID, modelStr string, apiKey string, prompt, completion int, costSaved float64) {
	entry := domain.UsageEntry{
		Timestamp:        time.Now(),
		Provider:         m.Provider,
		Model:            m.Model,
		ApiKey:           apiKey,
		Endpoint:         "chat/completions",
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CacheHit:         true,
		CacheTokensSaved: prompt + completion,
		CacheCostSaved:   costSaved,
		Status:           200,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Usage.Record(ctx, &entry)
}

// teeReadCloser wraps an io.ReadCloser, copying bytes into an internal buffer
// (up to limit). On Close it invokes onClose with the buffered data.
// Close is idempotent — sse.Write and the handler's defer both call Close.
type teeReadCloser struct {
	r           io.ReadCloser
	buf         bytes.Buffer
	limit       int
	closed      bool
	start       time.Time    // request start, for TTFT and total latency
	firstByteAt time.Time   // zero until the first non-empty Read
	onClose     func(buf []byte, ttftMs int64)
}

func (t *teeReadCloser) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		if t.firstByteAt.IsZero() {
			t.firstByteAt = time.Now()
		}
		limit := t.limit
		if limit == 0 {
			limit = maxUsageBuf
		}
		if t.buf.Len() < limit {
			remaining := limit - t.buf.Len()
			if n <= remaining {
				t.buf.Write(p[:n])
			} else {
				t.buf.Write(p[:remaining])
			}
		}
	}
	return n, err
}

func (t *teeReadCloser) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	err := t.r.Close()
	if t.onClose != nil {
		ttftMs := int64(0)
		if !t.firstByteAt.IsZero() && !t.start.IsZero() {
			ttftMs = t.firstByteAt.Sub(t.start).Milliseconds()
		}
		t.onClose(t.buf.Bytes(), ttftMs)
	}
	return err
}

func (s *RouterService) markRateLimited(ctx context.Context, conn *domain.Connection, res *RouterResponse) {
	retryAfter := domain.ParseRetryAfter(res.Headers.Get("Retry-After"))
	if retryAfter == 0 {
		retryAfter = 5 * time.Second
	}
	until := time.Now().Add(retryAfter)
	_ = s.Connections.SetRateLimited(ctx, conn.ID, until)
}

func (s *RouterService) rotatedModels(name string, models []string) []string {
	s.rotationMu.Lock()
	defer s.rotationMu.Unlock()
	i := s.rotation[name]
	if i >= len(models) {
		i = 0
	}
	s.rotation[name] = (i + 1) % len(models)
	rotated := make([]string, len(models))
	for j := 0; j < len(models); j++ {
		rotated[j] = models[(i+j)%len(models)]
	}
	return rotated
}

// RefreshProviderCache delegates to the ConnectionSelector. Called by
// handlers after provider config changes.
func (s *RouterService) RefreshProviderCache(ctx context.Context) {
	if s.Selector != nil {
		s.Selector.Refresh(ctx)
	}
}

// RefreshPricingCache delegates to the PricingCache. Called at startup and
// after each model sync.
func (s *RouterService) RefreshPricingCache(ctx context.Context) {
	if s.Pricing != nil {
		s.Pricing.Refresh(ctx)
	}
}

type openAIChatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// extractModel returns the "model" field from an OpenAI-format request body.
// It tries a cheap json.Unmarshal of just the model field first; if the body
// is multipart (audio/transcriptions), it falls back to scanning the multipart
// form. This avoids a full json.Unmarshal of the entire body on the hot path.
func extractModel(body []byte) (string, error) {
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err == nil {
		if probe.Model != "" {
			return probe.Model, nil
		}
		return "", fmt.Errorf("model field is required")
	}
	if model, ok := extractModelFromMultipart(body); ok {
		return model, nil
	}
	return "", fmt.Errorf("parse openai request: could not extract model")
}

// extractModelFromMultipart scans a multipart/form-data body for a "model"
// field and returns its value. Returns ok=false if the body is not multipart
// or the field is absent. This avoids pulling in mime/multipart parsing of
// the full request (the body is already read as bytes by the handler) by
// doing a cheap text scan for the form field name.
func extractModelFromMultipart(body []byte) (string, bool) {
	const marker = `name="model"`
	idx := bytes.Index(body, []byte(marker))
	if idx < 0 {
		return "", false
	}
	// The value follows the header block: after "name="model"\r\n\r\n".
	rest := body[idx+len(marker):]
	// Skip the closing quote of the name attribute and any remaining headers.
	hdrEnd := bytes.Index(rest, []byte("\r\n\r\n"))
	if hdrEnd < 0 {
		return "", false
	}
	val := rest[hdrEnd+4:]
	// The value ends at the next CRLF (boundary line) or end of body.
	end := bytes.Index(val, []byte("\r\n"))
	if end < 0 {
		end = len(val)
	}
	v := strings.TrimSpace(string(val[:end]))
	if v == "" {
		return "", false
	}
	return v, true
}

// rewriteMultipartModel replaces the value of the "model" field in a
// multipart/form-data body with the given upstream model name. This strips
// the provider prefix (e.g. "openai/whisper-1" -> "whisper-1") that the
// client sends, since the upstream expects the bare model name.
func rewriteMultipartModel(body []byte, upstreamModel string) []byte {
	const marker = `name="model"`
	idx := bytes.Index(body, []byte(marker))
	if idx < 0 {
		return body
	}
	rest := body[idx+len(marker):]
	hdrEnd := bytes.Index(rest, []byte("\r\n\r\n"))
	if hdrEnd < 0 {
		return body
	}
	valStart := idx + len(marker) + hdrEnd + 4
	valEnd := valStart
	end := bytes.Index(body[valStart:], []byte("\r\n"))
	if end < 0 {
		valEnd = len(body)
	} else {
		valEnd = valStart + end
	}
	oldVal := body[valStart:valEnd]
	if string(oldVal) == upstreamModel {
		return body
	}
	out := make([]byte, 0, len(body)-len(oldVal)+len(upstreamModel))
	out = append(out, body[:valStart]...)
	out = append(out, []byte(upstreamModel)...)
	out = append(out, body[valEnd:]...)
	return out
}

// ModelsService builds the /v1/models list from combos + the persisted
// model catalog (synced from providers). It no longer fetches live from
// upstreams on every request — the catalog is kept fresh by ModelSyncService.
type ModelsService struct {
	Combos domain.ComboRepo
	Models domain.ModelRepo
}

func (s *ModelsService) List(ctx context.Context) ([]domain.ModelInfo, error) {
	var out []domain.ModelInfo
	combos, err := s.Combos.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range combos {
		kind := c.Kind
		if kind == "" {
			kind = domain.KindLLM
		}
		out = append(out, domain.ModelInfo{ID: c.Name, Object: "model", OwnedBy: "combo", Kind: kind})
	}
	// Read active models from the catalog (no live fetch).
	if s.Models != nil {
		entries, err := s.Models.ListActive(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			out = append(out, domain.ModelInfo{ID: e.ID, Object: "model", OwnedBy: e.ProviderID, Kind: e.Kind})
		}
	}
	return out, nil
}

// cacheKeyCtxKey is the context key for stashing the response cache key.
type cacheKeyCtxKey struct{}

func withCacheKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, cacheKeyCtxKey{}, key)
}

func cacheKeyFromCtx(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(cacheKeyCtxKey{}).(string)
	return key, ok
}
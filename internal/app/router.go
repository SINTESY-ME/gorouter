// Package app holds the application services (use cases). Each service is a
// thin orchestrator that depends only on domain ports; infrastructure adapters
// are injected at the composition root.
package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	// SemanticCache is optional vector-similarity cache. Nil disables it.
	SemanticCache *SemanticCacheService
	// MaxCacheHistory, when > 0, skips caching requests with more than this
	// many chat messages (conversations that won't repeat just bloat the
	// cache). 0 disables the guard.
	MaxCacheHistory int
	// Hooks is the optional hook pipeline (PreCall/PostCall/PostCallFailure).
	// Nil disables hooks entirely — every hook point is skipped at zero cost.
	Hooks *HookPipeline
	// MCP is the optional MCP gateway service. When set, exposed MCP tools
	// are injected into chat/responses/antihopic requests and the
	// non-stream agent loop can execute tool calls server-side. Nil
	// disables all MCP behavior at zero cost on the hot path.
	MCP *MCPService
}

// probeCtxKey is used to mark a context as originating from a health probe
// so that test doubles (mock executors) can distinguish probe calls from
// real request calls and avoid polluting call snapshots.
type probeCtxKey struct{}

// maxComboDepth caps how deeply the router will recurse into nested combos.
// The dashboard validation (ComboService.detectCycle) already rejects cycles
// at save time; this is a safety net for manually-edited data.
const maxComboDepth = 5

// maxTransientRetries is how many times the router retries an upstream call
// on the same connection after a transient failure (429/5xx status or a
// network error) before falling through to the next connection or model.
// Retrying is safe: nothing has been written to the client yet.
const maxTransientRetries = 2

// transientBackoff returns the delay before retry attempt n (0-based).
func transientBackoff(n int) time.Duration {
	switch n {
	case 0:
		return 250 * time.Millisecond
	case 1:
		return 500 * time.Millisecond
	default:
		return time.Second
	}
}

// retryableStatus reports whether an upstream HTTP status is worth a
// same-connection retry. Successful statuses and deterministic client errors
// (400/422/415 — a malformed request fails identically on every retry) never
// retry. Every other failure status retries when the connection was healthy at
// request start; health — not the specific error class — is what drives the
// retry decision (see executeOneWithRetry).
func retryableStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, // 400
		http.StatusUnprocessableEntity,  // 422
		http.StatusUnsupportedMediaType: // 415
		return false
	}
	return status >= 400
}

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
	ctx = withInputFormat(ctx, opts.InputFormat)
	requestID := uuid.New().String()
	// Hooks: the pre-call admission gate runs before the cache lookup and
	// routing. When no hooks are registered hc is nil and every hook point
	// is skipped at zero cost. A hook may rewrite the body or the model.
	hc := s.newHookContext(requestID, modelStr, stream, apiKey, opts, body)
	if hc != nil {
		ctx = withHookContext(ctx, hc)
		if err := s.Hooks.RunPreCall(ctx, hc); err != nil {
			return nil, err
		}
		body = hc.Body
		modelStr = hc.Model
	}
	ctx = withRequestBody(ctx, body)
	// MCP tool injection: merge the gateway's exposed tools into the request
	// body (format-aware) before the cache lookup and routing, so the cache
	// key reflects the tools the model actually sees. Existing caller-supplied
	// tools win on name collision.
	if s.MCP != nil && opts.Endpoint == "" {
		if injected, err := s.MCP.InjectTools(ctx, opts.InputFormat, body); err == nil {
			body = injected
		}
	}
	// Per-key model access: reject models the authenticated key isn't allowed
	// to use before any upstream/cache work.
	if !s.modelAllowed(ctx, modelStr) {
		return nil, domain.ErrForbidden
	}
	// Deterministic cache lookup: short-circuit on hit. Only for chat
	// (endpoint=="") and only when cache is enabled and the request doesn't
	// opt out. Combos skip this — their cache lookup is strategy-aware and
	// happens inside routeCombo, keyed by the real model that produced the
	// response.
	if s.Cache != nil && s.Cache.Enabled() && opts.Endpoint == "" && !isCacheDisabled(ctx) {
		// Very long conversations rarely repeat exactly and mostly bloat the
		// cache (Bifrost's conversation-history-threshold guard); skip them.
		if s.MaxCacheHistory > 0 {
			if n := messageCount(body); n > s.MaxCacheHistory {
				ctx = WithCacheDisabled(ctx)
			}
		}
	}
	if s.Cache != nil && s.Cache.Enabled() && opts.Endpoint == "" && !isCacheDisabled(ctx) {
		if _, isModel := domain.SplitModelID(modelStr); isModel {
			cacheKey := s.Cache.ComputeKey(body, modelStr, opts.InputFormat)
			if cached, ok := s.Cache.Lookup(ctx, cacheKey); ok {
				return s.buildCachedResponse(ctx, cached, modelStr, apiKey), nil
			}
		}
	}

	// Semantic cache: after deterministic miss, before routing. Look up
	// by vector similarity across all candidate models. The same
	// x-gr-cache: off opt-out that bypasses the deterministic cache also
	// bypasses the semantic cache.
	if s.SemanticCache != nil && s.SemanticCache.Enabled() && opts.Endpoint == "" && !isCacheDisabled(ctx) {
		var candidates []string
		if mid, ok := domain.SplitModelID(modelStr); ok {
			candidates = []string{mid.Provider + "/" + mid.Model}
		} else if combo, err := s.Combos.GetByName(ctx, modelStr); err == nil {
			candidates = combo.Models
		}
		if len(candidates) > 0 {
			result := s.SemanticCache.Lookup(ctx, body, candidates, opts.InputFormat)
			if result.Hit && result.Response != nil {
				var prompt, completion, cacheRead, cacheCreation int
				if result.Response.Stream {
					prompt, completion, cacheRead, cacheCreation = parseUsageFromSSEFull(result.Response.StreamChunks)
				} else {
					prompt, completion, cacheRead, cacheCreation = parseUsageFromJSONFull(result.Response.Body)
				}
				var costSaved float64
				var mid domain.ModelID
				if s.Pricing != nil {
					if m, ok := domain.SplitModelID(result.Model); ok {
						mid = m
						if pricing, ok := s.Pricing.Get(mid); ok {
							costSaved = CalculateCost(pricing, "", prompt, completion, cacheRead, cacheCreation)
						}
					}
				}
				go s.recordSemanticCacheHitUsage(mid, result.Model, apiKey, prompt, completion, costSaved)
				if s.Savings != nil {
					s.Savings.RecordSemanticCacheHit(prompt+completion, costSaved)
				}
				// Clone the cached headers so we don't mutate the shared
				// cache entry, and tag the response as a semantic hit.
				outHeaders := result.Response.Headers.Clone()
				outHeaders.Set("x-gr-semantic-cache-hit", "true")
				outHeaders.Set("x-gr-similarity", fmt.Sprintf("%.4f", result.Similarity))
				outHeaders.Set("x-gr-semantic-model", result.Model)
				if result.Response.Stream {
					return &RouterResponse{
						StatusCode: result.Response.StatusCode,
						Headers:    outHeaders,
						Body:       io.NopCloser(bytes.NewReader(result.Response.StreamChunks)),
						Stream:     true,
						Cached:     true,
					}, nil
				}
				return &RouterResponse{
					StatusCode: result.Response.StatusCode,
					Headers:    outHeaders,
					Body:       io.NopCloser(bytes.NewReader(result.Response.Body)),
					Stream:     false,
					Cached:     true,
				}, nil
			}
		}
	}

	// Route the request. When the MCP agent loop is active (non-stream
	// OpenAI chat with MCP configured), wrap the single dispatch in the
	// tool-execution loop so tool calls are resolved server-side.
	if s.MCP != nil && !stream && opts.InputFormat == domain.FormatOpenAI && opts.Endpoint == "" {
		res, err := s.routeWithAgentLoop(ctx, modelStr, body, apiKey, opts, requestID)
		return s.finishRoute(ctx, hc, res, err)
	}
	res, err := s.routeChatDispatch(ctx, modelStr, body, stream, apiKey, opts, requestID)
	return s.finishRoute(ctx, hc, res, err)
}

// routeChatDispatch routes a chat request to a single model or a combo,
// returning the raw (un-finished) result. It is the shared dispatch used both
// by RouteChat and by the MCP agent loop.
func (s *RouterService) routeChatDispatch(ctx context.Context, modelStr string, body []byte, stream bool, apiKey string, opts RouteOptions, requestID string) (*RouterResponse, error) {
	modelID, ok := domain.SplitModelID(modelStr)
	if ok {
		attempt := 0
		return s.routeSingle(ctx, modelID, body, stream, apiKey, opts, "", requestID, &attempt)
	}
	combo, err := s.Combos.GetByName(ctx, modelStr)
	if err == domain.ErrNotFound {
		return nil, fmt.Errorf("model %q not found", modelStr)
	}
	if err != nil {
		return nil, err
	}
	attempt := 0
	return s.routeCombo(ctx, combo, body, stream, apiKey, opts, "", 0, nil, requestID, &attempt)
}

// RoutePassthrough routes a non-chat endpoint (embeddings, images) to a
// single upstream connection. The body stays in OpenAI format — no
// translation is applied. Combos are supported via model-name lookup just
// like chat. endpoint is "embeddings" or "images/generations".
func (s *RouterService) RoutePassthrough(ctx context.Context, body []byte, modelStr string, endpoint string, apiKey string, contentType string) (*RouterResponse, error) {
	opts := RouteOptions{InputFormat: domain.FormatOpenAI, Endpoint: endpoint, ContentType: contentType}
	requestID := uuid.New().String()
	// Same pre-call admission gate as chat. Passthrough responses can be
	// binary, so post-call hooks are chat-only; failures still notify.
	hc := s.newHookContext(requestID, modelStr, false, apiKey, opts, body)
	if hc != nil {
		ctx = withHookContext(ctx, hc)
		if err := s.Hooks.RunPreCall(ctx, hc); err != nil {
			return nil, err
		}
		body = hc.Body
		modelStr = hc.Model
	}
	if !s.modelAllowed(ctx, modelStr) {
		return nil, domain.ErrForbidden
	}
	modelID, ok := domain.SplitModelID(modelStr)
	if ok {
		attempt := 0
		res, err := s.routeSingle(ctx, modelID, body, false, apiKey, opts, endpoint, requestID, &attempt, contentType)
		return s.finishRoute(ctx, hc, res, err)
	}
	combo, err := s.Combos.GetByName(ctx, modelStr)
	if err == domain.ErrNotFound {
		return nil, fmt.Errorf("model %q not found", modelStr)
	}
	if err != nil {
		return nil, err
	}
	attempt := 0
	res, err := s.routeCombo(ctx, combo, body, false, apiKey, opts, endpoint, 0, nil, requestID, &attempt, contentType)
	return s.finishRoute(ctx, hc, res, err)
}

// RouterResponse is what the HTTP handler receives. It is either a buffered
// JSON body (non-stream) or a ReadCloser yielding SSE (stream). The caller
// must close Body if non-nil.
type RouterResponse struct {
	StatusCode   int
	Headers      http.Header
	Body         io.ReadCloser
	Stream       bool
	Provider     string
	Model        string
	ConnectionID string
	// Cached is true when the response came from the response cache.
	Cached bool
	// FallbackReason is set when this request fell through to a
	// different combo/model than the one chosen by the intelligence
	// classifier. Empty when no fallback occurred.
	FallbackReason string
	// Attempts is the number of upstream calls made before this response
	// (1 = first try). Exposed as the x-gr-attempted-retries header.
	Attempts int
	// RTK savings — populated by executeOne when RTK compression reduced
	// the request body. Propagated to recordUsage for persistence.
	RTKBytesSaved  int
	RTKTokensSaved int
	RTKCostSaved   float64
}

func (s *RouterService) routeSingle(ctx context.Context, m domain.ModelID, body []byte, stream bool, apiKey string, opts RouteOptions, endpoint string, requestID string, attempt *int, contentType ...string) (*RouterResponse, error) {
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
	// Snapshot which keys were already failing before this request so phase
	// 2 (unhealthy keys) only retries them — keys that fail during phase 1
	// are not retried, each key is tried at most once per request.
	unhealthy := s.unhealthySnapshot(modelStr, conns)
	var lastUpstream *domain.UpstreamError
	for _, phaseHealthy := range []bool{true, false} {
		for i := 0; i < len(conns); i++ {
			conn := &conns[(startIdx+i)%len(conns)]
			if !conn.IsActive {
				continue
			}
			// Honor the upstream's rate-limit pause: a connection with
			// RateLimitedUntil in the future is skipped for this request
			// instead of being hammered again before Retry-After elapses.
			if conn.RateLimitedUntil.After(time.Now()) {
				continue
			}
			if unhealthy[conn.ID] == phaseHealthy {
				if phaseHealthy {
					s.maybeProbe(modelStr, m, conn.ID)
				}
				continue
			}
			connStart := time.Now()
			res, err := s.executeOneWithRetry(ctx, m, conn, body, stream, opts, ct, phaseHealthy)
			if err != nil {
				s.recordFailedUsage(m, conn, apiKey, endpoint, 0, err.Error(), connStart, nil, requestID, *attempt, 0, 0)
				*attempt++
				s.Health.MarkUnhealthy(modelStr, conn.ID)
				s.maybeProbe(modelStr, m, conn.ID)
				continue
			}
			if endpoint == "" && res.StatusCode >= 400 {
				message := upstreamErrorMessage(res)
				if message == "" {
					message = fmt.Sprintf("upstream %d", res.StatusCode)
				}
				s.recordFailedUsage(m, conn, apiKey, endpoint, res.StatusCode, message, connStart, nil, requestID, *attempt, 0, 0)
				if !domain.ShouldFallback(res.StatusCode, nil) {
					return res, nil
				}
				*attempt++
				s.Health.MarkUnhealthy(modelStr, conn.ID)
				s.markRateLimited(ctx, conn, res)
				s.maybeProbe(modelStr, m, conn.ID)
				// Remember the last real upstream failure so the client sees
				// its actual status (e.g. 429, 500) instead of a generic one.
				lastUpstream = &domain.UpstreamError{Status: res.StatusCode, Message: message}
				if res.Body != nil {
					res.Body.Close()
				}
				continue
			}
			s.Health.MarkHealthy(modelStr, conn.ID)
			if err := s.finalizeSuccess(ctx, res, m, conn, apiKey, endpoint, nil, start, requestID, *attempt); err != nil {
				return nil, err
			}
			*attempt++
			res.Provider = m.Provider
			res.Model = m.Model
			res.ConnectionID = conn.ID
			res.Attempts = *attempt
			return res, nil
		}
	}
	if lastUpstream != nil {
		return nil, lastUpstream
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
		"model":          modelStr,
		"messages":       tpsProbeMessages,
		"max_tokens":     tpsProbeMaxTokens,
		"temperature":    0.0,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
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

func (s *RouterService) routeCombo(ctx context.Context, combo *domain.Combo, body []byte, stream bool, apiKey string, opts RouteOptions, endpoint string, depth int, comboChain []string, requestID string, attempt *int, contentType ...string) (*RouterResponse, error) {
	start := time.Now()
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	models := combo.Models
	var strat ComboStrategy
	if s.Strategies != nil {
		strat = s.Strategies.For(combo.Strategy)
		ordered, err := strat.Order(ctx, StrategyRequest{Combo: combo, Body: body, APIKey: apiKey})
		if err != nil {
			slog.Warn("combo strategy failed; using configured order", "combo", combo.Name, "strategy", combo.Strategy, "err", err)
		} else if len(ordered) > 0 {
			if ordered[0] != combo.Models[0] && combo.Strategy == StrategyIntelligence {
				slog.Info("intelligence classifier chose different model", "combo", combo.Name, "configured_first", combo.Models[0], "chosen_first", ordered[0])
			}
			models = ordered
		}
	}
	var lastErr error
	attempts := make([]modelAttempt, 0, len(models))

	// Context-window filter: skip models whose window can't fit the prompt —
	// they would just fail upstream and waste a fallback. Fail-open: without
	// context data (model not synced) nothing is filtered.
	if s.Pricing != nil {
		var fit []string
		est := 0
		hasEst := false
		for _, modelStr := range models {
			if m, ok := domain.SplitModelID(modelStr); ok {
				if ctx, ok2 := s.Pricing.Context(m); ok2 && ctx > 0 {
					if !hasEst {
						est = estimatePromptTokens(body)
						hasEst = true
					}
					if est > ctx {
						slog.Debug("combo: skipping model, prompt exceeds context window", "combo", combo.Name, "model", modelStr, "estimate", est, "context", ctx)
						continue
					}
				}
			}
			fit = append(fit, modelStr)
		}
		if len(fit) > 0 {
			models = fit
		}
	}

	// Phase 1: try the keys that were healthy when the request started,
	// walking the models in strategy order. Keys already marked unhealthy
	// are skipped here and watched by background probes.
	for _, modelStr := range models {
		m, ok := domain.SplitModelID(modelStr)
		if !ok {
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
			nestedStart := time.Now()
			res, err := s.routeCombo(ctx, nested, body, stream, apiKey, opts, endpoint, depth+1, nestedChain, requestID, attempt, ct)
			if err != nil {
				slog.Warn("combo fallback: nested combo failed, trying next", "parent_combo", combo.Name, "failed_combo", modelStr, "err", err)
				s.recordFailedUsage(domain.ModelID{}, nil, apiKey, endpoint, 0, err.Error(), nestedStart, nestedChain, requestID, *attempt, 0, 0)
				*attempt++
				lastErr = keepLastError(lastErr, err)
				continue
			}
			if res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
				slog.Warn("combo fallback: nested combo returned error status, trying next", "parent_combo", combo.Name, "failed_combo", modelStr, "status", res.StatusCode)
				s.recordFailedUsage(domain.ModelID{}, nil, apiKey, endpoint, res.StatusCode, fmt.Sprintf("upstream %d", res.StatusCode), nestedStart, nestedChain, requestID, *attempt, 0, 0)
				*attempt++
				lastErr = fmt.Errorf("upstream %d", res.StatusCode)
				if res.Body != nil {
					res.Body.Close()
				}
				continue
			}
			return res, nil
		}
		// Deterministic cache check: the strategy decides which models'
		// caches to check before attempting this model. Cache hits are
		// independent of health — a cached response from a previous
		// healthy call can be served even if the model is now unhealthy.
		if strat != nil {
			if cached, ok := s.checkComboCache(ctx, body, strat, combo, modelStr, models, opts, apiKey); ok {
				return cached, nil
			}
		}
		conns, err := s.Connections.ListByProvider(ctx, m.Provider)
		if err != nil {
			lastErr = err
			continue
		}
		startIdx := 0
		if s.Selector != nil {
			startIdx = s.Selector.StartIndex(conns)
		}
		childChain := append(append([]string{}, comboChain...), combo.Name)
		p := modelAttempt{
			m:         m,
			conns:     conns,
			startIdx:  startIdx,
			unhealthy: s.unhealthySnapshot(modelStr, conns),
		}
		attempts = append(attempts, p)
		res, err := s.tryModelWithConns(ctx, m, conns, body, stream, apiKey, opts, childChain, start, true, p.unhealthy, startIdx, requestID, attempt, ct)
		if err != nil {
			slog.Warn("combo fallback: model failed, trying next", "combo", combo.Name, "failed_model", modelStr, "err", err)
			lastErr = keepLastError(lastErr, err)
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

	// Phase 2: no healthy key worked anywhere — retry the keys that were
	// already unhealthy when the request started, in the same model order
	// and from the same rotation point as phase 1. Keys that failed during
	// phase 1 are not retried: each key is tried at most once per request.
	for i := range attempts {
		p := &attempts[i]
		modelStr := p.m.Provider + "/" + p.m.Model
		// Same cache check as Phase 1: a hit serves immediately, even
		// for a model whose connections are all unhealthy.
		if strat != nil {
			if cached, ok := s.checkComboCache(ctx, body, strat, combo, modelStr, models, opts, apiKey); ok {
				return cached, nil
			}
		}
		childChain := append(append([]string{}, comboChain...), combo.Name)
		res, err := s.tryModelWithConns(ctx, p.m, p.conns, body, stream, apiKey, opts, childChain, start, false, p.unhealthy, p.startIdx, requestID, attempt, ct)
		if err != nil {
			lastErr = keepLastError(lastErr, err)
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
		var ue *domain.UpstreamError
		if errors.As(lastErr, &ue) {
			return nil, ue // surface the last real upstream status
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrAllModelsFailed, lastErr)
	}
	return nil, domain.ErrAllModelsFailed
}

// modelAttempt carries the per-model data shared by the two combo phases so
// phase 2 (unhealthy keys) reuses the same connection order and the same
// health snapshot taken before phase 1.
type modelAttempt struct {
	m         domain.ModelID
	conns     []domain.Connection
	startIdx  int
	unhealthy map[string]bool
}

// keepLastError preserves a more meaningful failure. Phase 2 calls
// tryModelWithConns even for models whose connections were all healthy at
// request start — those are entirely skipped, so tryModelWithConns returns
// ErrNoConnection ("nothing to try") without a real upstream failure. That
// sentinel must not clobber a real error already recorded for the request.
func keepLastError(last, err error) error {
	if last != nil && errors.Is(err, domain.ErrNoConnection) {
		return last
	}
	return err
}

// estimatePromptTokens is a cheap heuristic for the prompt's token count
// (roughly 4 characters per token over the message content). Used by the
// combo router to skip models whose context window can't fit the request.
func estimatePromptTokens(body []byte) int {
	var probe struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return len(body) / 4
	}
	chars := 0
	for _, msg := range probe.Messages {
		switch c := msg.Content.(type) {
		case string:
			chars += len(c)
		case []any:
			for _, b := range c {
				if m, ok := b.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						chars += len(t)
					}
				}
			}
		}
	}
	return chars / 4
}

// unhealthySnapshot returns the set of connection IDs currently marked
// unhealthy for modelStr. It is taken once per request so keys that fail
// during phase 1 are not retried in phase 2.
func (s *RouterService) unhealthySnapshot(modelStr string, conns []domain.Connection) map[string]bool {
	snap := make(map[string]bool, len(conns))
	for i := range conns {
		if s.Health.IsUnhealthy(modelStr, conns[i].ID) {
			snap[conns[i].ID] = true
		}
	}
	return snap
}

// maybeProbe launches a background health probe for an unhealthy (model,
// connection) pair when it is unhealthy and no probe is already in flight.
func (s *RouterService) maybeProbe(modelStr string, m domain.ModelID, connID string) {
	if s.Prober != nil && s.Health.TryStartProbe(modelStr, connID) {
		go s.Prober.RunProbe(modelStr, m, connID)
	}
}

// tryModelWithConns tries the model's connections, walking them from startIdx.
// phaseHealthy selects which connections participate: true tries only the keys
// that were healthy when the request started, false tries only the keys that
// were unhealthy (the snapshot taken before phase 1). Keys that failed in the
// first pass are not retried in the second — each key is tried at most once
// per request.
func (s *RouterService) tryModelWithConns(ctx context.Context, m domain.ModelID, conns []domain.Connection, body []byte, stream bool, apiKey string, opts RouteOptions, comboChain []string, start time.Time, phaseHealthy bool, unhealthy map[string]bool, startIdx int, requestID string, attempt *int, contentType ...string) (*RouterResponse, error) {
	if len(conns) == 0 {
		return nil, fmt.Errorf("%w: provider %q", domain.ErrNoConnection, m.Provider)
	}
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	modelStr := m.Provider + "/" + m.Model
	var lastUpstream *domain.UpstreamError
	for i := 0; i < len(conns); i++ {
		conn := &conns[(startIdx+i)%len(conns)]
		if !conn.IsActive {
			continue
		}
		// Same rate-limit pause as routeSingle: skip connections still
		// cooling down instead of re-hammering them.
		if conn.RateLimitedUntil.After(time.Now()) {
			continue
		}
		if unhealthy[conn.ID] == phaseHealthy {
			if phaseHealthy {
				s.maybeProbe(modelStr, m, conn.ID)
			}
			continue
		}
		connStart := time.Now()
		res, err := s.executeOneWithRetry(ctx, m, conn, body, stream, opts, ct, phaseHealthy)
		if err != nil {
			s.recordFailedUsage(m, conn, apiKey, opts.Endpoint, 0, err.Error(), connStart, comboChain, requestID, *attempt, 0, 0)
			*attempt++
			s.Health.MarkUnhealthy(modelStr, conn.ID)
			s.maybeProbe(modelStr, m, conn.ID)
			continue
		}
		if res.StatusCode >= 400 {
			message := upstreamErrorMessage(res)
			if message == "" {
				message = fmt.Sprintf("upstream %d", res.StatusCode)
			}
			if !domain.ShouldFallback(res.StatusCode, nil) {
				s.recordFailedUsage(m, conn, apiKey, opts.Endpoint, res.StatusCode, message, connStart, comboChain, requestID, *attempt, 0, 0)
				return res, nil
			}
			s.recordFailedUsage(m, conn, apiKey, opts.Endpoint, res.StatusCode, message, connStart, comboChain, requestID, *attempt, 0, 0)
			*attempt++
			s.Health.MarkUnhealthy(modelStr, conn.ID)
			s.markRateLimited(ctx, conn, res)
			s.maybeProbe(modelStr, m, conn.ID)
			// Remember the last real upstream failure for the client.
			lastUpstream = &domain.UpstreamError{Status: res.StatusCode, Message: message}
			if res.Body != nil {
				res.Body.Close()
			}
			continue
		}
		// Blank-completion guard (combos only): a 2xx completion that ran out
		// of output budget ("length"/"max_tokens") with no content and no
		// tool calls is an upstream soft-failure, not a success — the client
		// asked for a working answer and got an empty one. Mirrors litellm's
		// Responses API incomplete_details fallback behavior. The provider
		// itself is healthy (the client's max_tokens is what cut it short),
		// so no health/probe action — just try the next connection/model.
		if opts.Endpoint == "" && !res.Stream {
			if reason, prompt, completion, berr := blankCompletionReason(res); reason != "" || berr != nil {
				msg := reason
				status := 0
				if berr != nil {
					msg = "read upstream response: " + berr.Error()
				}
				s.recordFailedUsage(m, conn, apiKey, opts.Endpoint, status, msg, connStart, comboChain, requestID, *attempt, prompt, completion)
				*attempt++
				lastUpstream = &domain.UpstreamError{Status: status, Message: msg}
				if res.Body != nil {
					res.Body.Close()
				}
				continue
			}
		}
		s.Health.MarkHealthy(modelStr, conn.ID)
		if err := s.finalizeSuccess(ctx, res, m, conn, apiKey, opts.Endpoint, comboChain, start, requestID, *attempt); err != nil {
			return nil, err
		}
		*attempt++
		res.Provider = m.Provider
		res.Model = m.Model
		res.ConnectionID = conn.ID
		res.Attempts = *attempt
		return res, nil
	}
	if lastUpstream != nil {
		return nil, lastUpstream
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
		// 2) OpenAI -> upstream format. For the common OpenAI→OpenAI case the
		// rewrites (model substitution, stream_options injection, empty
		// tool_calls strip) are applied in a single parse + marshal to avoid
		// re-parsing the body on every pass.
		var err error
		if inputFmt == domain.FormatOpenAI && targetFmt == domain.FormatOpenAI {
			body = prepareOpenAIBody(body, m.Model, stream)
			translated = body
		} else {
			if stream && targetFmt == domain.FormatOpenAI {
				body = injectStreamUsage(body)
			}
			// Strip empty tool_calls arrays — some providers (Qwen) reject them.
			body = sanitizeEmptyToolCalls(body)
			translated, err = s.Translator.TranslateRequest(domain.FormatOpenAI, targetFmt, m.Model, body)
			if err != nil {
				return nil, err
			}
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
			ProviderID:    m.Provider,
			Connection:    conn,
			Config:        cfg,
			UpstreamModel: m.Model,
			Body:          io.NopCloser(bytes.NewReader(translated)),
			Stream:        stream,
			Timeout:       upstreamTimeoutFromCtx(ctx),
		}
		slog.Debug("executing upstream request", "provider", m.Provider, "model", m.Model)
		res, err := s.Executor.Execute(ctx, execReq)
		if err != nil {
			return nil, err
		}
		if res.StatusCode >= 400 {
			bufErr, _ := io.ReadAll(res.Body)
			res.Body.Close()
			slog.Warn("upstream returned error status", "status", res.StatusCode, "provider", m.Provider, "model", m.Model, "response_body", string(bufErr), "request_payload", string(translated))
			res.Body = io.NopCloser(bytes.NewReader(bufErr))
			// Error responses are not SSE streams even when the upstream was
			// asked to stream — the body is a JSON error. Skip stream/JSON
			// translation so the raw error reaches the fallback layer intact
			// (upstreamErrorMessage can parse it, and the client sees the real
			// error instead of a synthesized response.completed).
			res.Stream = false
			respBody = res.Body
			return &RouterResponse{
				StatusCode:     res.StatusCode,
				Headers:        res.Headers,
				Body:           respBody,
				Stream:         false,
				RTKBytesSaved:  rtkBytesSaved,
				RTKTokensSaved: rtkTokensSaved,
				RTKCostSaved:   rtkCostSaved,
			}, nil
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
		ProviderID:    m.Provider,
		Connection:    conn,
		Config:        cfg,
		UpstreamModel: m.Model,
		Body:          io.NopCloser(bytes.NewReader(translated)),
		Stream:        false,
		Endpoint:      opts.Endpoint,
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

// executeOneWithRetry runs executeOne, retrying a failure on the same
// connection up to maxTransientRetries times — but only when the connection
// was healthy at request start (phase 1). An endpoint that was already
// unhealthy (phase-2 last resort) gets a single attempt: retrying a
// known-failing endpoint only wastes time. Retrying is safe because no bytes
// have been written to the client yet.
//
// The decision is driven by health, not the specific error class: any failure
// (network error or retryableStatus) is retried when the endpoint was healthy.
// Deterministic client errors (400/422/415) never retry. The final result is
// returned as-is so the caller's existing status/error handling applies.
func (s *RouterService) executeOneWithRetry(ctx context.Context, m domain.ModelID, conn *domain.Connection, body []byte, stream bool, opts RouteOptions, ct string, healthyAtStart bool) (*RouterResponse, error) {
	var res *RouterResponse
	var err error
	for attempt := 0; attempt <= maxTransientRetries; attempt++ {
		if attempt > 0 {
			if !healthyAtStart {
				break // not healthy at request start: single attempt, no retry
			}
			select {
			case <-ctx.Done():
				if err != nil {
					return nil, err
				}
				return nil, ctx.Err()
			case <-time.After(transientBackoff(attempt - 1)):
			}
		}
		res, err = s.executeOne(ctx, m, conn, body, stream, opts, ct)
		if err == nil && !retryableStatus(res.StatusCode) {
			return res, nil
		}
		if healthyAtStart && attempt < maxTransientRetries {
			reason := "network error"
			if err == nil {
				reason = fmt.Sprintf("upstream %d", res.StatusCode)
				if res.Body != nil {
					res.Body.Close()
				}
			}
			slog.Warn("upstream failure, retrying healthy endpoint", "provider", m.Provider, "model", m.Model, "conn", conn.ID, "attempt", attempt+1, "reason", reason)
		}
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

// wrapUsageTracking wraps res.Body with a tee reader that copies response
// bytes into an in-memory buffer. When the body is closed (after the HTTP
// handler has finished writing to the client) the buffer is parsed for token
// usage and a UsageEntry is recorded. This keeps the hot path (streaming to
// the client) untouched while still capturing usage asynchronously.
// On success, the buffered response is stored in the deterministic cache,
// keyed by the real model (m.Provider/m.Model) — not the combo name — so
// cache entries are shared across combos and direct model calls alike.
func (s *RouterService) wrapUsageTracking(ctx context.Context, res *RouterResponse, m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, comboChain []string, start time.Time, requestID string, attempt int) {
	cacheEnabled := s.Cache != nil && s.Cache.Enabled()
	cacheEligible := cacheEnabled && !isCacheDisabled(ctx) && endpoint == ""
	bufLimit := maxUsageBuf
	if cacheEligible {
		bufLimit = maxCacheBuf
	}
	// Determine the input format from the context (set in RouteChat).
	inputFmt := domain.FormatOpenAI
	if v, ok := ctx.Value(inputFormatCtxKey{}).(domain.Format); ok {
		inputFmt = v
	}
	tee := &teeReadCloser{
		r:     res.Body,
		limit: bufLimit,
		start: start,
		onClose: func(buf []byte, ttftMs int64) {
			s.recordUsage(ctx, m, conn, apiKey, endpoint, res.StatusCode, res.Stream, buf, comboChain, start, ttftMs, res.RTKBytesSaved, res.RTKTokensSaved, res.RTKCostSaved, requestID, attempt)
			if cacheEligible && res.StatusCode < 400 {
				actualModel := m.Provider + "/" + m.Model
				reqBody, _ := requestBodyFromCtx(ctx)
				key := s.Cache.ComputeKey(reqBody, actualModel, inputFmt)
				if res.Stream {
					s.Cache.StoreStream(ctx, key, res.StatusCode, res.Headers, buf)
				} else {
					s.Cache.Store(ctx, key, res.StatusCode, res.Headers, buf)
				}
			}
			// Store in semantic cache (both active and lazy modes store
			// on successful responses). We store the response in client
			// format so it can be replayed regardless of which model
			// generated it. The x-gr-cache: off opt-out also disables
			// semantic cache writes.
			if s.SemanticCache != nil && s.SemanticCache.Enabled() && endpoint == "" && res.StatusCode < 400 && len(buf) > 0 && !isCacheDisabled(ctx) {
				modelStr := m.Provider + "/" + m.Model
				cached := &domain.CachedResponse{
					StatusCode: res.StatusCode,
					Headers:    res.Headers,
				}
				if res.Stream {
					cached.StreamChunks = buf
					cached.Stream = true
				} else {
					cached.Body = buf
				}
				reqBody, _ := requestBodyFromCtx(ctx)
				s.SemanticCache.Store(ctx, reqBody, modelStr, inputFmt, cached)
			}
			// Streams are piped to the client verbatim, so post-call hooks
			// get a best-effort notification off the hot path once the body
			// is consumed (LiteLLM's async_log_success_event pattern).
			if res.Stream && s.Hooks != nil && s.Hooks.HasPostCall() {
				if hc, ok := hookContextFromCtx(ctx); ok {
					s.notifyPostCallStream(hc, res, m, conn, endpoint, buf, ttftMs, start)
				}
			}
		},
	}
	res.Body = tee
}

// finalizeSuccess records usage/cache and, for non-stream chat responses with a
// registered PostCallHook, hands the fully-buffered body to the hooks before it
// reaches the client. When no post-call hooks apply it is exactly
// wrapUsageTracking — the zero-overhead path.
func (s *RouterService) finalizeSuccess(ctx context.Context, res *RouterResponse, m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, comboChain []string, start time.Time, requestID string, attempt int) error {
	hc, _ := hookContextFromCtx(ctx)
	doPostCall := s.Hooks != nil && s.Hooks.HasPostCall() && hc != nil && !res.Stream && endpoint == "" && res.StatusCode < 400
	if !doPostCall {
		s.wrapUsageTracking(ctx, res, m, conn, apiKey, endpoint, comboChain, start, requestID, attempt)
		return nil
	}
	s.wrapUsageTracking(ctx, res, m, conn, apiKey, endpoint, comboChain, start, requestID, attempt)
	buf, rerr := io.ReadAll(res.Body)
	cerr := res.Body.Close()
	if rerr != nil || cerr != nil {
		return fmt.Errorf("read response for post-call hook: %v", firstErr(rerr, cerr))
	}
	hres := s.buildHookResponse(res, buf, m, conn, endpoint, 0, start)
	if err := s.Hooks.RunPostCall(ctx, hc, hres); err != nil {
		return err
	}
	res.Body = io.NopCloser(bytes.NewReader(hres.Body))
	if hres.Headers != nil {
		res.Headers = hres.Headers
	}
	return nil
}

// notifyPostCallStream fires post-call hooks asynchronously after a streaming
// response is fully consumed. Streaming bytes are piped verbatim to the client,
// so this is a best-effort notification only — modifications are discarded.
func (s *RouterService) notifyPostCallStream(hc *domain.HookContext, res *RouterResponse, m domain.ModelID, conn *domain.Connection, endpoint string, buf []byte, ttftMs int64, start time.Time) {
	hres := s.buildHookResponse(res, buf, m, conn, endpoint, ttftMs, start)
	go func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Hooks.RunPostCall(dctx, hc, hres); err != nil {
			slog.Warn("post-call hook failed", "model", hc.Model, "err", err)
		}
	}()
}

// buildHookResponse assembles a HookResponse from a completed response, parsing
// token usage from the buffered body and resolving cost from the pricing cache.
func (s *RouterService) buildHookResponse(res *RouterResponse, buf []byte, m domain.ModelID, conn *domain.Connection, endpoint string, ttftMs int64, start time.Time) *domain.HookResponse {
	var prompt, completion, cacheRead, cacheCreation int
	if res.Stream {
		prompt, completion, cacheRead, cacheCreation = parseUsageFromSSEFull(buf)
	} else {
		prompt, completion, cacheRead, cacheCreation = parseUsageFromJSONFull(buf)
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
	return &domain.HookResponse{
		StatusCode:          res.StatusCode,
		Headers:             res.Headers,
		Body:                buf,
		Stream:              res.Stream,
		Provider:            m.Provider,
		Model:               m.Model,
		ConnectionID:        connID,
		PromptTokens:        prompt,
		CompletionTokens:    completion,
		CacheReadTokens:     cacheRead,
		CacheCreationTokens: cacheCreation,
		Cost:                cost,
		LatencyMs:           time.Since(start).Milliseconds(),
		TTFTMs:              ttftMs,
	}
}

// newHookContext returns a HookContext for the request, or nil when no hooks
// are registered. Nil is the zero-cost fast path: every hook point is skipped
// without allocating anything.
func (s *RouterService) newHookContext(requestID, modelStr string, stream bool, apiKey string, opts RouteOptions, body []byte) *domain.HookContext {
	if s.Hooks == nil || s.Hooks.Empty() {
		return nil
	}
	return &domain.HookContext{
		RequestID:   requestID,
		Model:       modelStr,
		Stream:      stream,
		APIKey:      apiKey,
		InputFormat: opts.InputFormat,
		Endpoint:    opts.Endpoint,
		Body:        body,
	}
}

// finishRoute runs post-call failure hooks when a routed request errored or
// produced an upstream error response, then returns the result.
func (s *RouterService) finishRoute(ctx context.Context, hc *domain.HookContext, res *RouterResponse, err error) (*RouterResponse, error) {
	if hc == nil || s.Hooks == nil || !s.Hooks.HasPostCallFailure() {
		return res, err
	}
	if err != nil {
		status := 0
		var ue *domain.UpstreamError
		if errors.As(err, &ue) {
			status = ue.Status
		}
		if nerr := s.Hooks.RunPostCallFailure(ctx, hc, status, err); nerr != nil {
			return nil, nerr
		}
		return nil, err
	}
	if res != nil && res.StatusCode >= 400 {
		if nerr := s.Hooks.RunPostCallFailure(ctx, hc, res.StatusCode, nil); nerr != nil {
			return nil, nerr
		}
	}
	return res, err
}

// upstreamErrorMessage extracts a readable message from a failed upstream
// response body (best-effort; the body is consumed). Returns the upstream
// error.message when the body is JSON, otherwise the raw body, capped.
func upstreamErrorMessage(res *RouterResponse) string {
	body, _ := io.ReadAll(res.Body)
	res.Body = io.NopCloser(bytes.NewReader(body))
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
		return parsed.Error.Message
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	return msg
}

// blankCompletionReason inspects a non-stream chat completion response and
// reports when the upstream produced no usable content while exhausting its
// output budget (finish_reason "length"/"max_tokens" with empty message
// content and no tool calls). This mirrors litellm's Responses API handling
// of incomplete_details.reason = max_output_tokens, which raises so the
// router can fall back to the next model group instead of passing the empty
// response through. The body is preserved for the caller — fully read
// responses are rewinded, larger ones are spliced back onto the remainder —
// and no cache entry is created for blank completions.
// blankCompletionReason inspects a non-stream chat completion response and
// reports when the upstream produced no usable content while exhausting its
// output budget (finish_reason "length"/"max_tokens" with empty message
// content and no tool calls). This mirrors litellm's Responses API handling
// of incomplete_details.reason = max_output_tokens, which raises so the
// router can fall back to the next model group instead of passing the empty
// response through. The body is preserved for the caller — fully read
// responses are rewinded, larger ones are spliced back onto the remainder —
// and no cache entry is created for blank completions. When a blank
// completion is detected, the tokens the upstream actually consumed are
// returned so the failure is still accounted for.
func blankCompletionReason(res *RouterResponse) (reason string, prompt, completion int, err error) {
	const cap = 512 << 10 // 512 KiB: blank completions are tiny; larger bodies are left untouched
	if res.Body == nil {
		return "upstream response body is nil", 0, 0, nil
	}
	head, readErr := io.ReadAll(io.LimitReader(res.Body, cap))
	if readErr != nil {
		res.Body.Close()
		return "", 0, 0, readErr
	}
	if len(head) == cap {
		// Body is bigger than the inspection cap — reattach the tail so the
		// client still receives the full response untouched.
		res.Body = &readTailCloser{r: io.MultiReader(bytes.NewReader(head), res.Body), c: res.Body}
		return "", 0, 0, nil
	}
	res.Body = io.NopCloser(bytes.NewReader(head))
	var out struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls any    `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(head, &out); err != nil {
		return "", 0, 0, nil // not a JSON completion — pass through
	}
	for _, c := range out.Choices {
		if c.Message.ToolCalls != nil {
			return "", 0, 0, nil // empty content + tool calls is a valid response
		}
		if c.FinishReason == "length" || c.FinishReason == "max_tokens" {
			if strings.TrimSpace(c.Message.Content) == "" {
				prompt, completion = parseUsageFromJSON(head)
				return fmt.Sprintf("upstream exhausted tokens (%s) with empty content", c.FinishReason), prompt, completion, nil
			}
			return "", 0, 0, nil
		}
		return "", 0, 0, nil
	}
	return "upstream returned a completion with no choices", 0, 0, nil
}

// readTailCloser presents the head+tail of a partially-consumed body as one
// ReadCloser, closing the underlying stream once.
type readTailCloser struct {
	r io.Reader
	c io.Closer
}

func (b *readTailCloser) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *readTailCloser) Close() error               { return b.c.Close() }

// firstErr returns the first non-nil error in errs, or nil.
func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// recordUsage parses token counts from the buffered response body and writes
// a single UsageEntry. Uses a detached context (the request may be done by
// the time the body is closed). When the model has pricing data in the
// in-memory pricing cache, the dollar cost is calculated and recorded.
// comboChain is the list of combo names from root to leaf (e.g.
// ["coding", "medium"]). After inserting the usage entry, combo_executions
// rows are inserted so every combo in the chain gets credit.
// userIDFromCtx returns the authenticated dashboard user ID from the
// request context, or "" for API-key/unauth requests.
func userIDFromCtx(ctx context.Context) string {
	if scope := domain.UserScopeFrom(ctx); scope != nil {
		return scope.UserID
	}
	return ""
}

func (s *RouterService) recordUsage(ctx context.Context, m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, status int, stream bool, buf []byte, comboChain []string, start time.Time, ttftMs int64, rtkBytes int, rtkTokens int, rtkCost float64, requestID string, attempt int) {
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
		ApiKeyID:         apiKey,
		UserID:           userIDFromCtx(ctx),
		Endpoint:         endpoint,
		LatencyMs:        time.Since(start).Milliseconds(),
		TTFTMs:           ttftMs,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		Cost:             cost,
		Status:           status,
		RTKCompressed:    rtkBytes > 0,
		RTKBytesSaved:    rtkBytes,
		RTKTokensSaved:   rtkTokens,
		RTKCostSaved:     rtkCost,
		ComboChain:       comboChain,
		RequestID:        requestID,
		Attempt:          attempt,
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
		ApiKeyID:         apiKey,
		Endpoint:         "chat/completions",
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CacheHit:         true,
		CacheTokensSaved: prompt + completion,
		CacheCostSaved:   costSaved,
		Status:           200,
		RequestID:        uuid.New().String(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Usage.Record(ctx, &entry)
}

// recordSemanticCacheHitUsage writes a UsageEntry for a semantic cache hit.
// Runs in a goroutine — never blocks the request path.
func (s *RouterService) recordSemanticCacheHitUsage(m domain.ModelID, modelStr string, apiKey string, prompt, completion int, costSaved float64) {
	entry := domain.UsageEntry{
		Timestamp:           time.Now(),
		Provider:            m.Provider,
		Model:               m.Model,
		ApiKeyID:            apiKey,
		Endpoint:            "chat/completions",
		PromptTokens:        prompt,
		CompletionTokens:    completion,
		SemanticCacheHit:    true,
		SemanticTokensSaved: prompt + completion,
		SemanticCostSaved:   costSaved,
		Status:              200,
		RequestID:           uuid.New().String(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Usage.Record(ctx, &entry)
}

// buildCachedResponse constructs a RouterResponse from a cached entry, parses
// token usage, records the cost saved, and fires the usage + savings records
// asynchronously. Used by both the individual-model cache lookup in RouteChat
// and the per-strategy combo cache lookup in routeCombo.
func (s *RouterService) buildCachedResponse(ctx context.Context, cached *domain.CachedResponse, modelStr string, apiKey string) *RouterResponse {
	var prompt, completion, cacheRead, cacheCreation int
	if cached.Stream {
		prompt, completion, cacheRead, cacheCreation = parseUsageFromSSEFull(cached.StreamChunks)
	} else {
		prompt, completion, cacheRead, cacheCreation = parseUsageFromJSONFull(cached.Body)
	}
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
		}
	}
	return &RouterResponse{
		StatusCode: cached.StatusCode,
		Headers:    cached.Headers,
		Body:       io.NopCloser(bytes.NewReader(cached.Body)),
		Stream:     false,
		Cached:     true,
	}
}

// checkComboCache checks the deterministic cache for the candidate models
// determined by the combo's strategy. Returns a cached response on the first
// hit, or nil on miss. The strategy decides which models to check and in
// what order (see ComboStrategy.CacheCandidates).
func (s *RouterService) checkComboCache(ctx context.Context, body []byte, strat ComboStrategy, combo *domain.Combo, currentModel string, orderedModels []string, opts RouteOptions, apiKey string) (*RouterResponse, bool) {
	if s.Cache == nil || !s.Cache.Enabled() || opts.Endpoint != "" || isCacheDisabled(ctx) {
		return nil, false
	}
	for _, cand := range strat.CacheCandidates(combo, currentModel, orderedModels) {
		key := s.Cache.ComputeKey(body, cand, opts.InputFormat)
		if cached, ok := s.Cache.Lookup(ctx, key); ok {
			return s.buildCachedResponse(ctx, cached, cand, apiKey), true
		}
	}
	return nil, false
}

func (s *RouterService) recordFailedUsage(m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, status int, errMsg string, start time.Time, comboChain []string, requestID string, attempt int, promptTokens, completionTokens int) {
	if endpoint == "" {
		endpoint = "chat/completions"
	}
	var connID string
	if conn != nil {
		connID = conn.ID
	}
	var cost float64
	if s.Pricing != nil {
		if pricing, ok := s.Pricing.Get(m); ok {
			cost = CalculateCost(pricing, endpoint, promptTokens, completionTokens, 0, 0)
		}
	}
	entry := domain.UsageEntry{
		Timestamp:        time.Now(),
		Provider:         m.Provider,
		Model:            m.Model,
		ConnectionID:     connID,
		ApiKeyID:         apiKey,
		Endpoint:         endpoint,
		LatencyMs:        time.Since(start).Milliseconds(),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Cost:             cost,
		Status:           status,
		Error:            errMsg,
		ComboChain:       comboChain,
		RequestID:        requestID,
		Attempt:          attempt,
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
	start       time.Time // request start, for TTFT and total latency
	firstByteAt time.Time // zero until the first non-empty Read
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

// inputFormatCtxKey stores the client's input format in the context so
// the response path (wrapUsageTracking) knows what format the cached
// response body is in.
type inputFormatCtxKey struct{}

func withInputFormat(ctx context.Context, f domain.Format) context.Context {
	return context.WithValue(ctx, inputFormatCtxKey{}, f)
}

// requestBodyCtxKey stores the original request body (client format) so
// the response path can extract prompt text for the semantic cache.
type requestBodyCtxKey struct{}

func withRequestBody(ctx context.Context, body []byte) context.Context {
	return context.WithValue(ctx, requestBodyCtxKey{}, body)
}

func requestBodyFromCtx(ctx context.Context) ([]byte, bool) {
	body, ok := ctx.Value(requestBodyCtxKey{}).([]byte)
	return body, ok
}

// hookContextCtxKey stores the request's HookContext so the routing internals
// (finalizeSuccess, stream notification) can reach it without widening their
// signatures. hc is a pointer, so hook mutations are visible to all readers.
type hookContextCtxKey struct{}

func withHookContext(ctx context.Context, hc *domain.HookContext) context.Context {
	return context.WithValue(ctx, hookContextCtxKey{}, hc)
}

func hookContextFromCtx(ctx context.Context) (*domain.HookContext, bool) {
	hc, ok := ctx.Value(hookContextCtxKey{}).(*domain.HookContext)
	return hc, ok
}

// upstreamTimeoutCtxKey stores a per-request upstream timeout (x-gr-timeout).
// Zero means use the executor default.
type upstreamTimeoutCtxKey struct{}

// WithUpstreamTimeout marks the context with a per-request upstream timeout.
func WithUpstreamTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, upstreamTimeoutCtxKey{}, d)
}

func upstreamTimeoutFromCtx(ctx context.Context) time.Duration {
	d, _ := ctx.Value(upstreamTimeoutCtxKey{}).(time.Duration)
	return d
}

// allowedModelsCtxKey stores the authenticated key's allowed-models list.
type allowedModelsCtxKey struct{}

// WithAllowedModels marks the context with a key's allowed-models restriction
// (empty = all models allowed).
func WithAllowedModels(ctx context.Context, models []string) context.Context {
	return context.WithValue(ctx, allowedModelsCtxKey{}, models)
}

func allowedModelsFromCtx(ctx context.Context) []string {
	m, _ := ctx.Value(allowedModelsCtxKey{}).([]string)
	return m
}

// modelAllowed reports whether the key on this request may use modelStr. An
// empty allowed list allows everything. Matches the model id, its bare name,
// a combo name, or any combo member.
func (s *RouterService) modelAllowed(ctx context.Context, modelStr string) bool {
	allowed := allowedModelsFromCtx(ctx)
	if len(allowed) == 0 {
		return true
	}
	if containsStr(allowed, modelStr) {
		return true
	}
	if m, ok := domain.SplitModelID(modelStr); ok {
		if containsStr(allowed, m.Model) || containsStr(allowed, m.Provider+"/"+m.Model) {
			return true
		}
	}
	if combo, err := s.Combos.GetByName(ctx, modelStr); err == nil {
		for _, member := range combo.Models {
			if containsStr(allowed, member) {
				return true
			}
			if i := strings.LastIndexByte(member, '/'); i >= 0 && containsStr(allowed, member[i+1:]) {
				return true
			}
		}
	}
	return false
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

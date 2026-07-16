// Package app holds the application services (use cases). Each service is a
// thin orchestrator that depends only on domain ports; infrastructure adapters
// are injected at the composition root.
package app

import (
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
}

// probeCtxKey is used to mark a context as originating from a health probe
// so that test doubles (mock executors) can distinguish probe calls from
// real request calls and avoid polluting call snapshots.
type probeCtxKey struct{}

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
		Selector:    NewConnectionSelector(nil),
	}
	s.Prober = NewHealthProber(s.Health, conns, exec, tr, s.Selector)
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
			if s.Savings != nil {
				var prompt, completion int
				if cached.Stream {
					prompt, completion, _, _ = parseUsageFromSSEFull(cached.StreamChunks)
				} else {
					prompt, completion, _, _ = parseUsageFromJSONFull(cached.Body)
				}
				s.Savings.RecordCacheHit(prompt + completion)
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
	return s.routeCombo(ctx, combo, body, stream, apiKey, opts, "")
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
	return s.routeCombo(ctx, combo, body, false, apiKey, opts, endpoint, contentType)
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
		s.wrapUsageTracking(ctx, res, m, conn, apiKey, endpoint, "", start)
		res.Provider = m.Provider
		res.Model = m.Model
		res.ConnectionID = conn.ID
		return res, nil
	}
	return nil, fmt.Errorf("%w: provider %q", domain.ErrNoConnection, m.Provider)
}

func (s *RouterService) routeCombo(ctx context.Context, combo *domain.Combo, body []byte, stream bool, apiKey string, opts RouteOptions, endpoint string, contentType ...string) (*RouterResponse, error) {
	start := time.Now()
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	models := combo.Models
	if combo.Strategy == "round-robin" {
		models = s.rotatedModels(combo.Name, models)
	}
	var lastErr error
	var skipped []skipEntry // models where all connections are unhealthy
	for _, modelStr := range models {
		m, ok := domain.SplitModelID(modelStr)
		if !ok {
			lastErr = fmt.Errorf("combo model %q invalid", modelStr)
			continue
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
		res, err := s.tryModelWithConns(ctx, m, conns, body, stream, apiKey, opts, combo.Name, start, true, ct)
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
	// Last resort: every model's connections are all unhealthy (or no
	// healthy model worked). Retry the skipped models inline — a real
	// request can succeed where the probe hasn't run yet.
	for _, sk := range skipped {
		res, err := s.tryModelWithConns(ctx, sk.m, sk.conns, body, stream, apiKey, opts, combo.Name, start, false, ct)
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

func (s *RouterService) tryModel(ctx context.Context, m domain.ModelID, body []byte, stream bool, apiKey string, opts RouteOptions, comboName string, start time.Time, contentType ...string) (*RouterResponse, error) {
	conns, err := s.Connections.ListByProvider(ctx, m.Provider)
	if err != nil {
		return nil, err
	}
	return s.tryModelWithConns(ctx, m, conns, body, stream, apiKey, opts, comboName, start, true, contentType...)
}

// tryModelForceTries iterates connections without skipping unhealthy ones.
// Used in the last-resort pass: a real request can succeed where a probe
// hasn't run yet (e.g. transient failure already resolved).
func (s *RouterService) tryModelForceTries(ctx context.Context, m domain.ModelID, body []byte, stream bool, apiKey string, opts RouteOptions, comboName string, start time.Time, contentType ...string) (*RouterResponse, error) {
	conns, err := s.Connections.ListByProvider(ctx, m.Provider)
	if err != nil {
		return nil, err
	}
	return s.tryModelWithConns(ctx, m, conns, body, stream, apiKey, opts, comboName, start, false, contentType...)
}

// tryModelWithConns is the shared connection iteration logic. When skipUnhealthy
// is true, connections marked unhealthy are skipped and a background probe is
// launched. When false (last-resort), all active connections are tried inline
// regardless of health state.
func (s *RouterService) tryModelWithConns(ctx context.Context, m domain.ModelID, conns []domain.Connection, body []byte, stream bool, apiKey string, opts RouteOptions, comboName string, start time.Time, skipUnhealthy bool, contentType ...string) (*RouterResponse, error) {
	if len(conns) == 0 {
		return nil, fmt.Errorf("%w: provider %q", domain.ErrNoConnection, m.Provider)
	}
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
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
		s.wrapUsageTracking(ctx, res, m, conn, apiKey, opts.Endpoint, comboName, start)
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
		if s.Compressor != nil {
			before := len(translated)
			translated = s.Compressor.Compress(translated)
			if s.Savings != nil && len(translated) < before {
				s.Savings.RecordRTKCompression(before - len(translated))
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
		res, err := s.Executor.Execute(ctx, execReq)
		if err != nil {
			return nil, err
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
			StatusCode: res.StatusCode,
			Headers:    res.Headers,
			Body:       respBody,
			Stream:     res.Stream,
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
func (s *RouterService) wrapUsageTracking(ctx context.Context, res *RouterResponse, m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, comboName string, start time.Time) {
	cacheEnabled := s.Cache != nil && s.Cache.Enabled()
	_, hasCacheKey := cacheKeyFromCtx(ctx)
	bufLimit := maxUsageBuf
	if cacheEnabled && hasCacheKey {
		bufLimit = maxCacheBuf
	}
	tee := &teeReadCloser{
		r:    res.Body,
		limit: bufLimit,
		onClose: func(buf []byte) {
			s.recordUsage(m, conn, apiKey, endpoint, res.StatusCode, res.Stream, buf, comboName, start)
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
func (s *RouterService) recordUsage(m domain.ModelID, conn *domain.Connection, apiKey string, endpoint string, status int, stream bool, buf []byte, comboName string, start time.Time) {
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
	entry := domain.UsageEntry{
		Timestamp:         time.Now(),
		Provider:          m.Provider,
		Model:             m.Model,
		ComboName:         comboName,
		ConnectionID:      conn.ID,
		ApiKey:            apiKey,
		Endpoint:          endpoint,
		LatencyMs:         time.Since(start).Milliseconds(),
		PromptTokens:      prompt,
		CompletionTokens:  completion,
		Cost:              cost,
		Status:            status,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Usage.Record(ctx, entry)
}

// teeReadCloser wraps an io.ReadCloser, copying bytes into an internal buffer
// (up to limit). On Close it invokes onClose with the buffered data.
// Close is idempotent — sse.Write and the handler's defer both call Close.
type teeReadCloser struct {
	r       io.ReadCloser
	buf     bytes.Buffer
	limit   int
	closed  bool
	onClose func(buf []byte)
}

func (t *teeReadCloser) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
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
		t.onClose(t.buf.Bytes())
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
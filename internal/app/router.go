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
	// hot path reads pricing from an in-memory cache (pricingCache) that
	// is refreshed after each sync — no DB or registry lookup per request.
	Models domain.ModelRepo

	// comboRotation is in-memory state for round-robin combo strategy.
	// Not persisted; rotation resets on process restart (acceptable).
	rotationMu sync.Mutex
	rotation   map[string]int

	// pricingCache holds the pricing data for all models, keyed by
	// lowercase "provider/model". Refreshed by RefreshPricingCache after
	// each model sync. The hot path reads this with an RLock — zero DB
	// or registry overhead per request.
	pricingMu    sync.RWMutex
	pricingCache map[string]domain.ModelPricing

	// Health tracks per-(combo, model) failures so that subsequent requests
	// skip unhealthy models and a background probe restores them when they
	// recover. Not persisted; resets on process restart.
	Health *HealthTracker
}

// NewRouterService constructs a RouterService with the round-robin state
// initialised. Use this rather than a bare struct literal.
func NewRouterService(combos domain.ComboRepo, conns domain.ConnectionRepo, exec domain.Executor, tr domain.Translator, usage domain.UsageRepo) *RouterService {
	return &RouterService{
		Combos:      combos,
		Connections: conns,
		Executor:    exec,
		Translator:  tr,
		Usage:       usage,
		rotation:    map[string]int{},
		Health:      NewHealthTracker(),
	}
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
	for i := range conns {
		conn := pickConnection(conns, i)
		if !conn.IsActive || conn.RateLimitedUntil.After(time.Now()) {
			continue
		}
		res, err := s.executeOne(ctx, m, conn, body, stream, opts, ct)
		if err != nil {
			continue
		}
		// For passthrough (embeddings, audio, images) there is no fallback
		// and a 5xx from one endpoint doesn't mean the connection is bad for
		// chat. Return the response as-is; only rate-limit for chat (endpoint=="").
		if endpoint == "" && res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
			s.markRateLimited(ctx, conn, res)
			continue
		}
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
	var skipped []string // unhealthy models skipped in the first pass
	for _, modelStr := range models {
		m, ok := domain.SplitModelID(modelStr)
		if !ok {
			lastErr = fmt.Errorf("combo model %q invalid", modelStr)
			continue
		}
		if s.Health.IsUnhealthy(combo.Name, modelStr) {
			// Launch a background probe (at most one in flight per pair) so the
			// model can recover without a real request hitting it. The probe is
			// debounced by probeInFlight; subsequent requests that find it still
			// unhealthy while a probe is running simply skip it.
			if s.Health.TryStartProbe(combo.Name, modelStr) {
				go s.runHealthProbe(combo.Name, modelStr, m)
			}
			skipped = append(skipped, modelStr)
			continue
		}
		res, err := s.tryModel(ctx, m, body, stream, apiKey, opts, combo.Name, start, ct)
		if err != nil {
			s.Health.MarkUnhealthy(combo.Name, modelStr)
			lastErr = err
			continue
		}
		if res.StatusCode >= 400 && domain.ShouldFallback(res.StatusCode, nil) {
			s.Health.MarkUnhealthy(combo.Name, modelStr)
			lastErr = fmt.Errorf("upstream %d", res.StatusCode)
			if res.Body != nil {
				res.Body.Close()
			}
			continue
		}
		s.Health.MarkHealthy(combo.Name, modelStr)
		return res, nil
	}
	// Last resort: every model is unhealthy (or no healthy model worked).
	// Retry the skipped models inline — a real request can succeed where the
	// probe hasn't run yet (e.g. transient failure already resolved). On
	// success the model is marked healthy; on failure it stays unhealthy.
	for _, modelStr := range skipped {
		m, ok := domain.SplitModelID(modelStr)
		if !ok {
			continue
		}
		res, err := s.tryModel(ctx, m, body, stream, apiKey, opts, combo.Name, start, ct)
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
		s.Health.MarkHealthy(combo.Name, modelStr)
		return res, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrAllModelsFailed, lastErr)
	}
	return nil, domain.ErrAllModelsFailed
}

// runHealthProbe is a background goroutine that sends a minimal chat request
// to an unhealthy (combo, model) pair to check if it has recovered. It uses
// a detached context with a 20s timeout (longer than the executor's default
// upstream timeout, so probes don't race). On 2xx it marks the pair healthy;
// otherwise it leaves the unhealthy flag set and clears the probe-in-flight
// flag so the next request can launch a new probe. The probe does NOT go
// through wrapUsageTracking, so it does not pollute the usage table.
func (s *RouterService) runHealthProbe(comboName, modelStr string, m domain.ModelID) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conns, err := s.Connections.ListByProvider(ctx, m.Provider)
	if err != nil || len(conns) == 0 {
		s.Health.ProbeFailed(comboName, modelStr)
		slog.Debug("health probe: no connections for provider", "combo", comboName, "model", modelStr, "provider", m.Provider)
		return
	}
	var conn *domain.Connection
	for i := range conns {
		c := pickConnection(conns, i)
		if c.IsActive && !c.RateLimitedUntil.After(time.Now()) {
			conn = c
			break
		}
	}
	if conn == nil {
		s.Health.ProbeFailed(comboName, modelStr)
		slog.Debug("health probe: no active connection", "combo", comboName, "model", modelStr, "provider", m.Provider)
		return
	}

	probeBody := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"."}],"max_tokens":1,"stream":false}`, m.Model)
	targetFmt := conn.Format
	if targetFmt == "" {
		targetFmt = domain.FormatOpenAI
	}
	translated, err := s.Translator.TranslateRequest(domain.FormatOpenAI, targetFmt, m.Model, []byte(probeBody))
	if err != nil {
		s.Health.ProbeFailed(comboName, modelStr)
		slog.Debug("health probe: translate failed", "combo", comboName, "model", modelStr, "error", err)
		return
	}
	execReq := domain.ExecuteRequest{
		ProviderID:    m.Provider,
		Connection:    conn,
		UpstreamModel: m.Model,
		Body:          io.NopCloser(bytes.NewReader(translated)),
		Stream:        false,
	}
	res, err := s.Executor.Execute(ctx, execReq)
	if err != nil {
		s.Health.ProbeFailed(comboName, modelStr)
		slog.Debug("health probe: execute failed", "combo", comboName, "model", modelStr, "error", err)
		return
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)

	if res.StatusCode >= 200 && res.StatusCode < 400 {
		s.Health.MarkHealthy(comboName, modelStr)
		slog.Info("health probe: model recovered", "combo", comboName, "model", modelStr)
	} else {
		s.Health.ProbeFailed(comboName, modelStr)
		slog.Debug("health probe: still unhealthy", "combo", comboName, "model", modelStr, "status", res.StatusCode)
	}
}

func (s *RouterService) tryModel(ctx context.Context, m domain.ModelID, body []byte, stream bool, apiKey string, opts RouteOptions, comboName string, start time.Time, contentType ...string) (*RouterResponse, error) {
	conns, err := s.Connections.ListByProvider(ctx, m.Provider)
	if err != nil {
		return nil, err
	}
	if len(conns) == 0 {
		return nil, fmt.Errorf("%w: provider %q", domain.ErrNoConnection, m.Provider)
	}
	ct := ""
	if len(contentType) > 0 {
		ct = contentType[0]
	}
	for i := range conns {
		conn := pickConnection(conns, i)
		if !conn.IsActive || conn.RateLimitedUntil.After(time.Now()) {
			continue
		}
		res, err := s.executeOne(ctx, m, conn, body, stream, opts, ct)
		if err != nil {
			continue
		}
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
	translated := body
	var respBody io.ReadCloser
	if opts.Endpoint == "" {
		// Chat path: translate from client format to upstream format, then
		// back from upstream to client on the response.
		inputFmt := opts.InputFormat
		if inputFmt == "" {
			inputFmt = domain.FormatOpenAI
		}
		targetFmt := conn.Format
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
		bp, tp := body, translated
		if len(bp) > 300 {
			bp = bp[:300]
		}
		if len(tp) > 300 {
			tp = tp[:300]
		}
		slog.Info("executeOne translate", "inputFmt", inputFmt, "targetFmt", targetFmt, "model", m.Model, "body_preview", string(bp), "translated_preview", string(tp))
		execReq := domain.ExecuteRequest{
			ProviderID:   m.Provider,
			Connection:   conn,
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
	if pricing, ok := s.resolvePricing(m); ok {
		cost = CalculateCost(pricing, endpoint, prompt, completion, cacheRead, cacheCreation)
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

// resolvePricing reads the model's pricing from the in-memory cache.
// Pricing is resolved once during model sync and cached here. The hot
// path does no DB or registry lookup — just an RLock + map read.
func (s *RouterService) resolvePricing(m domain.ModelID) (domain.ModelPricing, bool) {
	s.pricingMu.RLock()
	defer s.pricingMu.RUnlock()
	if s.pricingCache == nil {
		return domain.ModelPricing{}, false
	}
	key := strings.ToLower(m.Provider + "/" + m.Model)
	p, ok := s.pricingCache[key]
	return p, ok
}

// RefreshPricingCache loads all model entries from the database and
// populates the in-memory pricing cache. Called at startup and after
// each model sync. Models without pricing data are skipped.
func (s *RouterService) RefreshPricingCache(ctx context.Context) {
	if s.Models == nil {
		return
	}
	entries, err := s.Models.List(ctx)
	if err != nil {
		slog.Error("pricing cache refresh failed", "err", err)
		return
	}
	m := make(map[string]domain.ModelPricing, len(entries))
	for _, e := range entries {
		if HasPricing(e.Pricing) {
			m[strings.ToLower(e.ID)] = e.Pricing
		}
	}
	s.pricingMu.Lock()
	s.pricingCache = m
	s.pricingMu.Unlock()
	slog.Info("pricing cache refreshed", "models", len(m))
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

func pickConnection(conns []domain.Connection, start int) *domain.Connection {
	for j := 0; j < len(conns); j++ {
		c := &conns[(start+j)%len(conns)]
		if c.IsActive {
			return c
		}
	}
	return &conns[0]
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
	Combos      domain.ComboRepo
	Connections domain.ConnectionRepo
	Fetcher     domain.ModelFetcher // kept for backward compat; not used in List
	Models      domain.ModelRepo
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
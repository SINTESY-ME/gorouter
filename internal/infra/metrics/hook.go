package metrics

import (
	"context"
	"strconv"
	"sync"

	"github.com/jhon/gorouter/internal/domain"
)

// Metric names. Counters carry the _total suffix explicitly because
// client_golang does not append it.
const (
	metricRequests       = "gorouter_requests_total"
	metricFailedRequests = "gorouter_failed_requests_total"
	metricDuration       = "gorouter_request_duration_seconds"
	metricTTFT           = "gorouter_request_ttft_seconds"
	metricInputTokens    = "gorouter_tokens_input_total"
	metricOutputTokens   = "gorouter_tokens_output_total"
	metricCacheRead      = "gorouter_cache_tokens_read_total"
	metricCacheCreation  = "gorouter_cache_tokens_creation_total"
	metricSpend          = "gorouter_spend_usd_total"
)

// latencyBuckets spans gorouter's sub-millisecond hot path out to slow
// upstreams, in seconds.
var latencyBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60}

var (
	registerOnce sync.Once
)

// ensureMetrics registers the standard metric set on Default once. The hook
// resolves metric pointers at construction, so the per-request path is a
// per-metric lock + map operation with no name lookups.
func ensureMetrics() {
	registerOnce.Do(func() {
		Default.Counter(metricRequests, "Total number of client requests (successes and failures)", "model", "endpoint")
		Default.Counter(metricFailedRequests, "Total number of failed client requests", "model", "status")
		Default.Histogram(metricDuration, "Total latency of a request (seconds)", latencyBuckets, "model", "endpoint")
		Default.Histogram(metricTTFT, "Time to first token for streaming responses (seconds)", latencyBuckets, "model")
		Default.Counter(metricInputTokens, "Total input tokens", "model")
		Default.Counter(metricOutputTokens, "Total output tokens", "model")
		Default.Counter(metricCacheRead, "Total cached input tokens read", "model")
		Default.Counter(metricCacheCreation, "Total cache-creation input tokens", "model")
		Default.Counter(metricSpend, "Total spend in USD", "model")
	})
}

// PrometheusHook implements domain.PostCallHook and domain.PostCallFailureHook
// and feeds the process-wide registry. It mirrors LiteLLM's PrometheusLogger,
// which is a callback updated by the success/failure events.
type PrometheusHook struct {
	requests    *Counter
	failed      *Counter
	duration    *Histogram
	ttft        *Histogram
	input       *Counter
	output      *Counter
	cacheRead   *Counter
	cacheCreate *Counter
	spend       *Counter
}

// NewHook builds a PrometheusHook wired to Default, registering the standard
// metric set on first use and resolving metric pointers once so the per-request
// path is a per-metric lock + map operation with no name lookups.
func NewHook() *PrometheusHook {
	ensureMetrics()
	return &PrometheusHook{
		requests:    Default.LookupCounter(metricRequests),
		failed:      Default.LookupCounter(metricFailedRequests),
		duration:    Default.LookupHistogram(metricDuration),
		ttft:        Default.LookupHistogram(metricTTFT),
		input:       Default.LookupCounter(metricInputTokens),
		output:      Default.LookupCounter(metricOutputTokens),
		cacheRead:   Default.LookupCounter(metricCacheRead),
		cacheCreate: Default.LookupCounter(metricCacheCreation),
		spend:       Default.LookupCounter(metricSpend),
	}
}

// PostCall records a successful request: total count, latency, tokens, spend,
// and time-to-first-token (streams only). Never errors.
func (h *PrometheusHook) PostCall(_ context.Context, hc *domain.HookContext, res *domain.HookResponse) error {
	labels := map[string]string{"model": res.Model, "endpoint": hc.Endpoint}
	h.requests.Add(labels, 1)
	h.duration.Observe(labels, millisecondsToSeconds(res.LatencyMs))
	if res.TTFTMs > 0 {
		h.ttft.Observe(map[string]string{"model": res.Model}, millisecondsToSeconds(res.TTFTMs))
	}
	model := map[string]string{"model": res.Model}
	h.input.Add(model, float64(res.PromptTokens))
	h.output.Add(model, float64(res.CompletionTokens))
	h.cacheRead.Add(model, float64(res.CacheReadTokens))
	h.cacheCreate.Add(model, float64(res.CacheCreationTokens))
	h.spend.Add(model, res.Cost)
	return nil
}

// PostCallFailure records a failed request: total count plus a failed counter
// broken down by model and upstream status. Never errors.
func (h *PrometheusHook) PostCallFailure(_ context.Context, hc *domain.HookContext, status int, _ error) error {
	h.requests.Add(map[string]string{"model": hc.Model, "endpoint": hc.Endpoint}, 1)
	h.failed.Add(map[string]string{"model": hc.Model, "status": strconv.Itoa(status)}, 1)
	return nil
}

func millisecondsToSeconds(ms int64) float64 {
	return float64(ms) / 1000.0
}

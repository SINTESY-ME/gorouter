package metrics

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jhon/gorouter/internal/domain"
)

func writeString(r *Registry) string {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	r.WritePrometheus(rec, req)
	return rec.Body.String()
}

func TestCounterExposition(t *testing.T) {
	r := NewRegistry()
	r.Counter("gorouter_requests_total", "Total requests", "model", "endpoint")
	c := r.LookupCounter("gorouter_requests_total")
	c.Add(map[string]string{"model": "gpt-4", "endpoint": ""}, 1)
	c.Add(map[string]string{"model": "gpt-4", "endpoint": ""}, 2)
	c.Add(map[string]string{"model": "claude", "endpoint": "embeddings"}, 1)

	out := writeString(r)
	for _, w := range []string{
		"# HELP gorouter_requests_total Total requests",
		"# TYPE gorouter_requests_total counter",
		`gorouter_requests_total{endpoint="",model="gpt-4"} 3`,
		`gorouter_requests_total{endpoint="embeddings",model="claude"} 1`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestHistogramExposition(t *testing.T) {
	r := NewRegistry()
	r.Histogram("gorouter_dur", "Latency", []float64{0.001, 0.01, 0.1}, "model")
	h := r.LookupHistogram("gorouter_dur")
	h.Observe(map[string]string{"model": "m"}, 0.005) // bucket: 0.01
	h.Observe(map[string]string{"model": "m"}, 0.2)   // bucket: +Inf
	h.Observe(map[string]string{"model": "m"}, 0.0005)

	out := writeString(r)
	for _, w := range []string{
		"# TYPE gorouter_dur histogram",
		`gorouter_dur_bucket{model="m",le="0.001"} 1`,
		`gorouter_dur_bucket{model="m",le="0.01"} 2`,
		`gorouter_dur_bucket{model="m",le="0.1"} 2`,
		`gorouter_dur_bucket{model="m",le="+Inf"} 3`,
		`gorouter_dur_sum{model="m"} 0.2055`,
		`gorouter_dur_count{model="m"} 3`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestGaugeExposition(t *testing.T) {
	r := NewRegistry()
	r.Gauge("gorouter_uptime_seconds", "Uptime", func() float64 { return 42 })
	out := writeString(r)
	if !strings.Contains(out, "# TYPE gorouter_uptime_seconds gauge") ||
		!strings.Contains(out, "gorouter_uptime_seconds 42") {
		t.Errorf("gauge output wrong\n---\n%s", out)
	}
}

func TestCounterValue(t *testing.T) {
	r := NewRegistry()
	r.Counter("gorouter_tokens_input", "help", "model")
	c := r.LookupCounter("gorouter_tokens_input")
	c.Add(map[string]string{"model": "m"}, 10)
	c.Add(map[string]string{"model": "m"}, 5)
	if got := testutil.ToFloat64(c.vec); got != 15 {
		t.Fatalf("counter value = %v, want 15", got)
	}
}

func TestNewHookWireUp(t *testing.T) {
	h := NewHook()
	if h.requests == nil || h.duration == nil || h.input == nil || h.spend == nil {
		t.Fatal("NewHook must resolve all metrics")
	}
}

func TestPrometheusHookPostCall(t *testing.T) {
	r := NewRegistry()
	r.Counter(metricRequests, "help", "model", "endpoint")
	r.Counter(metricInputTokens, "help", "model")
	r.Counter(metricOutputTokens, "help", "model")
	r.Counter(metricCacheRead, "help", "model")
	r.Counter(metricCacheCreation, "help", "model")
	r.Counter(metricSpend, "help", "model")
	r.Histogram(metricDuration, "help", latencyBuckets, "model", "endpoint")
	r.Histogram(metricTTFT, "help", latencyBuckets, "model")

	h := &PrometheusHook{
		requests:    r.LookupCounter(metricRequests),
		duration:    r.LookupHistogram(metricDuration),
		ttft:        r.LookupHistogram(metricTTFT),
		input:       r.LookupCounter(metricInputTokens),
		output:      r.LookupCounter(metricOutputTokens),
		cacheRead:   r.LookupCounter(metricCacheRead),
		cacheCreate: r.LookupCounter(metricCacheCreation),
		spend:       r.LookupCounter(metricSpend),
	}
	hc := &domain.HookContext{Endpoint: ""}
	res := &domain.HookResponse{
		Model: "gpt-4", Provider: "openai", StatusCode: 200, Stream: true,
		PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 5, CacheCreationTokens: 2,
		Cost: 0.0012, LatencyMs: 500, TTFTMs: 80,
	}
	if err := h.PostCall(context.Background(), hc, res); err != nil {
		t.Fatal(err)
	}

	out := writeString(r)
	for _, w := range []string{
		`gorouter_requests_total{endpoint="",model="gpt-4"} 1`,
		`gorouter_tokens_input_total{model="gpt-4"} 10`,
		`gorouter_tokens_output_total{model="gpt-4"} 20`,
		`gorouter_cache_tokens_read_total{model="gpt-4"} 5`,
		`gorouter_cache_tokens_creation_total{model="gpt-4"} 2`,
		`gorouter_spend_usd_total{model="gpt-4"} 0.0012`,
		`gorouter_request_ttft_seconds_bucket{model="gpt-4",le="0.1"} 1`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestPrometheusHookPostCallFailure(t *testing.T) {
	r := NewRegistry()
	r.Counter(metricRequests, "help", "model", "endpoint")
	r.Counter(metricFailedRequests, "help", "model", "status")
	h := &PrometheusHook{
		requests: r.LookupCounter(metricRequests),
		failed:   r.LookupCounter(metricFailedRequests),
	}
	hc := &domain.HookContext{Model: "gpt-4", Endpoint: "embeddings"}
	if err := h.PostCallFailure(context.Background(), hc, 503, nil); err != nil {
		t.Fatal(err)
	}
	out := writeString(r)
	for _, w := range []string{
		`gorouter_requests_total{endpoint="embeddings",model="gpt-4"} 1`,
		`gorouter_failed_requests_total{model="gpt-4",status="503"} 1`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", out, w)
		}
	}
}

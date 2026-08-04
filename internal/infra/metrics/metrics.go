// Package metrics exposes Prometheus metrics. It wraps the maintained
// prometheus/client_golang library (Counters, Histograms, GaugeFuncs) behind a
// small registry that keeps the composition root and the hook simple. Metrics
// are incremented in-process by the prometheus hook and served at GET /metrics.
package metrics

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Default is the process-wide registry: the prometheus hook writes to it and
// the /metrics handler reads it.
var Default = NewRegistry()

// Registry owns the registered metrics. All methods are safe for concurrent
// use. Registering the same metric name twice returns the existing metric.
type Registry struct {
	mu         sync.Mutex
	reg        *prometheus.Registry
	counters   map[string]*Counter
	histograms map[string]*Histogram
	gauges     map[string]struct{}
	handler    http.Handler
	start      time.Time
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		reg:        prometheus.NewRegistry(),
		counters:   map[string]*Counter{},
		histograms: map[string]*Histogram{},
		gauges:     map[string]struct{}{},
		start:      time.Now(),
	}
}

// Uptime returns the time elapsed since the registry was created.
func (r *Registry) Uptime() time.Duration { return time.Since(r.start) }

// Counter registers (or returns an already-registered) counter metric. Per
// Prometheus convention counters should be named with the _total suffix;
// client_golang does not append it.
func (r *Registry) Counter(name, help string, labels ...string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	vec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	r.reg.MustRegister(vec)
	c := &Counter{vec: vec}
	r.counters[name] = c
	return c
}

// LookupCounter returns the registered counter by name, or nil.
func (r *Registry) LookupCounter(name string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counters[name]
}

// Histogram registers (or returns an already-registered) histogram metric.
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h
	}
	vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
	r.reg.MustRegister(vec)
	h := &Histogram{vec: vec}
	r.histograms[name] = h
	return h
}

// LookupHistogram returns the registered histogram by name, or nil.
func (r *Registry) LookupHistogram(name string) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.histograms[name]
}

// Gauge registers a gauge evaluated at scrape time via fn (a GaugeFunc). fn
// must be safe for concurrent calls. Duplicate names are ignored.
func (r *Registry) Gauge(name, help string, fn func() float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.gauges[name]; ok {
		return
	}
	r.reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, fn))
	r.gauges[name] = struct{}{}
}

// WritePrometheus serves the Prometheus text exposition via the maintained
// promhttp handler.
func (r *Registry) WritePrometheus(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	if r.handler == nil {
		r.handler = promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
	}
	h := r.handler
	r.mu.Unlock()
	h.ServeHTTP(w, req)
}

// Counter is a monotonically increasing metric over labelled series.
type Counter struct {
	vec *prometheus.CounterVec
}

// Add increments the series identified by labels by v. Zero increments are
// skipped.
func (c *Counter) Add(labels map[string]string, v float64) {
	if v == 0 {
		return
	}
	c.vec.With(labels).Add(v)
}

// Histogram is a labelled set of fixed-bucket cumulative histograms.
type Histogram struct {
	vec *prometheus.HistogramVec
}

// Observe records a value.
func (h *Histogram) Observe(labels map[string]string, v float64) {
	h.vec.With(labels).Observe(v)
}

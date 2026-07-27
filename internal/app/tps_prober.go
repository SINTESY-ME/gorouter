package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// TPSProber measures the tokens-per-second of models that have no observed
// usage data, so the velocity strategy can rank them. It follows the same
// dedup pattern as HealthProber: at most one probe per model is in flight at
// a time. Probes run in the background and never block the request path.
//
// A probe sends a small, standardized non-streaming chat completion and
// measures wall-clock time against the completion_tokens reported by the
// upstream. The result is stored in TPSCache via SetProbe. Models with real
// usage data are never probed (NeedsProbe returns false for them).
type TPSProber struct {
	Cache   *TPSCache
	Router  *RouterService
	maxTPS  time.Duration // timeout for the probe request

	mu      sync.Mutex
	inFlight map[string]bool
}

// NewTPSProber constructs a TPSProber. Nil-safe: a nil TPSProber disables
// probing (the velocity strategy falls back to the configured order).
func NewTPSProber(cache *TPSCache, r *RouterService) *TPSProber {
	return &TPSProber{
		Cache:    cache,
		Router:   r,
		maxTPS:   30 * time.Second,
		inFlight: map[string]bool{},
	}
}

// MaybeProbe launches a background TPS probe for the given model when one is
// needed and none is already running. Non-blocking; safe to call on every
// velocity request.
func (p *TPSProber) MaybeProbe(modelStr string) {
	if p == nil || p.Router == nil {
		return
	}
	cache := p.Cache
	if cache == nil {
		cache = p.Router.TPS
	}
	if cache == nil || !cache.NeedsProbe(modelStr) {
		return
	}
	p.mu.Lock()
	if p.inFlight[modelStr] {
		p.mu.Unlock()
		return
	}
	p.inFlight[modelStr] = true
	p.mu.Unlock()

	go p.RunProbe(modelStr)
}

// RunProbe sends a standardized test prompt to the model, measures the
// response time and completion tokens, calculates TPS, and stores the
// result in the cache. Errors are logged but never propagated — a failed
// probe simply leaves the model with no TPS data (it will be retried on the
// next velocity request after the in-flight flag is cleared).
func (p *TPSProber) RunProbe(modelStr string) {
	defer func() {
		p.mu.Lock()
		delete(p.inFlight, modelStr)
		p.mu.Unlock()
	}()

	cache := p.Cache
	if cache == nil {
		cache = p.Router.TPS
	}
	if cache == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.maxTPS)
	defer cancel()

	start := time.Now()
	text, completionTokens, err := p.Router.measureModelTPS(ctx, modelStr, "")
	elapsed := time.Since(start)
	if err != nil {
		slog.Debug("tps probe failed", "model", modelStr, "err", err)
		return
	}
	if completionTokens <= 0 || elapsed <= 0 {
		slog.Debug("tps probe: no tokens or zero elapsed", "model", modelStr, "tokens", completionTokens, "elapsed", elapsed, "text_len", len(text))
		return
	}
	tps := float64(completionTokens) / elapsed.Seconds()
	cache.SetProbe(modelStr, tps)
	slog.Info("tps probe measured", "model", modelStr, "tps", fmt.Sprintf("%.1f", tps), "tokens", completionTokens, "elapsed", elapsed)
}

// tpsProbeMessages is the standardized prompt used to measure TPS. It
// requests a short paragraph — enough tokens for a stable measurement
// without being expensive.
var tpsProbeMessages = []map[string]any{
	{"role": "system", "content": "You are a helpful assistant. Respond concisely."},
	{"role": "user", "content": "Explain the concept of recursion in programming in 3-4 sentences."},
}

const tpsProbeMaxTokens = 150
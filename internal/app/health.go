package app

import "sync"

// HealthTracker keeps in-memory per-(combo, model) health state so that a
// model that has failed a real request is skipped on subsequent requests
// until a background probe confirms it is healthy again. State is not
// persisted; it resets on process restart (consistent with comboRotation).
type HealthTracker struct {
	mu     sync.Mutex
	states map[string]*healthState
}

type healthState struct {
	unhealthy     bool
	probeInFlight bool
}

func NewHealthTracker() *HealthTracker {
	return &HealthTracker{states: map[string]*healthState{}}
}

func (h *HealthTracker) state(comboName, modelStr string) *healthState {
	key := comboName + "|" + modelStr
	if s, ok := h.states[key]; ok {
		return s
	}
	s := &healthState{}
	h.states[key] = s
	return s
}

// IsUnhealthy reports whether the (combo, model) pair is currently marked
// unhealthy (i.e. should be skipped in the main fallback loop).
func (h *HealthTracker) IsUnhealthy(comboName, modelStr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state(comboName, modelStr).unhealthy
}

// MarkUnhealthy flags the (combo, model) pair as unhealthy. Idempotent.
func (h *HealthTracker) MarkUnhealthy(comboName, modelStr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state(comboName, modelStr).unhealthy = true
}

// MarkHealthy clears the unhealthy flag and the probe-in-flight flag for
// the (combo, model) pair, so the next request returns to using it.
// Idempotent; safe to call on a pair that was never unhealthy.
func (h *HealthTracker) MarkHealthy(comboName, modelStr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state(comboName, modelStr)
	s.unhealthy = false
	s.probeInFlight = false
}

// TryStartProbe attempts to launch a background probe for an unhealthy
// (combo, model) pair. Returns true iff the pair is unhealthy AND no probe
// is already in flight (in which case it sets probeInFlight=true). The
// caller must call ProbeFailed (or MarkHealthy on success) to release the
// in-flight flag.
func (h *HealthTracker) TryStartProbe(comboName, modelStr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state(comboName, modelStr)
	if !s.unhealthy || s.probeInFlight {
		return false
	}
	s.probeInFlight = true
	return true
}

// ProbeFailed releases the probe-in-flight flag without clearing the
// unhealthy flag, so a subsequent request can launch a new probe.
func (h *HealthTracker) ProbeFailed(comboName, modelStr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state(comboName, modelStr)
	s.probeInFlight = false
}

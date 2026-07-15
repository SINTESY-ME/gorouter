package app

import "sync"

// HealthTracker keeps in-memory per-(combo, model, connection) health state
// so that a specific key that has failed a real request is skipped on
// subsequent requests until a background probe confirms it is healthy
// again. State is not persisted; it resets on process restart (consistent
// with comboRotation).
//
// The key is "comboName|modelStr|connID". For single-model requests (no
// combo), comboName is "" (empty string). This allows fine-grained
// tracking: if key A fails for gpt-4o but key B works, only
// (combo, gpt-4o, keyA) is marked unhealthy — key B continues to serve.
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

func healthKey(comboName, modelStr, connID string) string {
	return comboName + "|" + modelStr + "|" + connID
}

func (h *HealthTracker) state(comboName, modelStr, connID string) *healthState {
	key := healthKey(comboName, modelStr, connID)
	if s, ok := h.states[key]; ok {
		return s
	}
	s := &healthState{}
	h.states[key] = s
	return s
}

// IsUnhealthy reports whether the (combo, model, connection) triple is
// currently marked unhealthy (i.e. should be skipped when iterating
// connections for this model).
func (h *HealthTracker) IsUnhealthy(comboName, modelStr, connID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state(comboName, modelStr, connID).unhealthy
}

// MarkUnhealthy flags the (combo, model, connection) triple as unhealthy.
// Idempotent.
func (h *HealthTracker) MarkUnhealthy(comboName, modelStr, connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state(comboName, modelStr, connID).unhealthy = true
}

// MarkHealthy clears the unhealthy flag and the probe-in-flight flag for
// the (combo, model, connection) triple, so the next request returns to
// using it. Idempotent; safe to call on a triple that was never unhealthy.
func (h *HealthTracker) MarkHealthy(comboName, modelStr, connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state(comboName, modelStr, connID)
	s.unhealthy = false
	s.probeInFlight = false
}

// TryStartProbe attempts to launch a background probe for an unhealthy
// (combo, model, connection) triple. Returns true iff the triple is
// unhealthy AND no probe is already in flight (in which case it sets
// probeInFlight=true). The caller must call ProbeFailed (or MarkHealthy on
// success) to release the in-flight flag.
func (h *HealthTracker) TryStartProbe(comboName, modelStr, connID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state(comboName, modelStr, connID)
	if !s.unhealthy || s.probeInFlight {
		return false
	}
	s.probeInFlight = true
	return true
}

// ProbeFailed releases the probe-in-flight flag without clearing the
// unhealthy flag, so a subsequent request can launch a new probe.
func (h *HealthTracker) ProbeFailed(comboName, modelStr, connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state(comboName, modelStr, connID).probeInFlight = false
}
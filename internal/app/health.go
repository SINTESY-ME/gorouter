package app

import "sync"

// HealthTracker keeps in-memory per-(model, connection) health state so that
// a specific key that has failed a real request is skipped on subsequent
// requests until a background probe confirms it is healthy again. State is
// not persisted; it resets on process restart (consistent with comboRotation).
//
// The key is "modelStr|connID". The combo is deliberately NOT part of the key:
// a connection serves any combo, and the upstream call for a given
// (model, connection) is identical regardless of which combo routed to it —
// if it fails for one combo it fails for all. This lets fine-grained tracking:
// if key A fails for gpt-4o but key B works, only (gpt-4o, keyA) is marked
// unhealthy — key B continues to serve.
type HealthTracker struct {
	mu     sync.RWMutex
	states map[string]*healthState
}

type healthState struct {
	unhealthy     bool
	probeInFlight bool
}

func NewHealthTracker() *HealthTracker {
	return &HealthTracker{states: map[string]*healthState{}}
}

func healthKey(modelStr, connID string) string {
	return modelStr + "|" + connID
}

// stateLocked must be called with h.mu held (write or read). If the entry
// doesn't exist it returns a zero healthState without creating one; callers
// that need to persist a new entry must call stateOrCreate.
func (h *HealthTracker) stateLocked(modelStr, connID string) healthState {
	if s, ok := h.states[healthKey(modelStr, connID)]; ok {
		return *s
	}
	return healthState{}
}

// stateOrCreate returns the existing state or creates a new one. Must be
// called with h.mu write-locked.
func (h *HealthTracker) stateOrCreate(modelStr, connID string) *healthState {
	key := healthKey(modelStr, connID)
	if s, ok := h.states[key]; ok {
		return s
	}
	s := &healthState{}
	h.states[key] = s
	return s
}

// IsUnhealthy reports whether the (model, connection) pair is currently
// marked unhealthy (i.e. should be skipped when iterating connections for
// this model). Uses RLock — hot path.
func (h *HealthTracker) IsUnhealthy(modelStr, connID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.stateLocked(modelStr, connID).unhealthy
}

// MarkUnhealthy flags the (model, connection) pair as unhealthy. Idempotent.
func (h *HealthTracker) MarkUnhealthy(modelStr, connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stateOrCreate(modelStr, connID).unhealthy = true
}

// MarkHealthy clears the unhealthy flag and the probe-in-flight flag for the
// (model, connection) pair, so the next request returns to using it.
// Idempotent; safe to call on a pair that was never unhealthy.
func (h *HealthTracker) MarkHealthy(modelStr, connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stateOrCreate(modelStr, connID)
	s.unhealthy = false
	s.probeInFlight = false
}

// TryStartProbe attempts to launch a background probe for an unhealthy
// (model, connection) pair. Returns true iff the pair is unhealthy AND no
// probe is already in flight (in which case it sets probeInFlight=true). The
// caller must call ProbeFailed (or MarkHealthy on success) to release the
// in-flight flag.
func (h *HealthTracker) TryStartProbe(modelStr, connID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stateOrCreate(modelStr, connID)
	if !s.unhealthy || s.probeInFlight {
		return false
	}
	s.probeInFlight = true
	return true
}

// ProbeFailed releases the probe-in-flight flag without clearing the
// unhealthy flag, so a subsequent request can launch a new probe.
func (h *HealthTracker) ProbeFailed(modelStr, connID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stateOrCreate(modelStr, connID)
	s.probeInFlight = false
}

// HealthSummary is a count snapshot of health states for the dashboard.
type HealthSummary struct {
	Unhealthy int `json:"unhealthy"`
	Probing   int `json:"probing"`
	Healthy   int `json:"healthy"`
	TotalKeys int `json:"total_keys"`
}

// Summary returns the count of (model, connection) pairs in each state.
// Unhealthy takes precedence over probing when both flags are set.
func (h *HealthTracker) Summary() HealthSummary {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var sum HealthSummary
	for _, s := range h.states {
		sum.TotalKeys++
		switch {
		case s.unhealthy && s.probeInFlight:
			sum.Unhealthy++
		case s.unhealthy:
			sum.Unhealthy++
		case s.probeInFlight:
			sum.Probing++
		default:
			sum.Healthy++
		}
	}
	return sum
}

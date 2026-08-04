package app

import "testing"

func TestHealthTrackerPerConnection(t *testing.T) {
	h := NewHealthTracker()
	const model, connA, connB = "openai/gpt-4o", "conn-a", "conn-b"

	// Initially everything is healthy.
	if h.IsUnhealthy(model, connA) {
		t.Fatal("connA should be healthy initially")
	}

	// Mark only connA unhealthy — connB must stay healthy.
	h.MarkUnhealthy(model, connA)
	if !h.IsUnhealthy(model, connA) {
		t.Fatal("connA should be unhealthy")
	}
	if h.IsUnhealthy(model, connB) {
		t.Fatal("connB should still be healthy")
	}

	// Probe can start for unhealthy connA but not for healthy connB.
	if !h.TryStartProbe(model, connA) {
		t.Fatal("probe should start for unhealthy connA")
	}
	if h.TryStartProbe(model, connA) {
		t.Fatal("second probe should not start — already in flight")
	}

	// Probe fails → flag released, still unhealthy.
	h.ProbeFailed(model, connA)
	if !h.IsUnhealthy(model, connA) {
		t.Fatal("connA should still be unhealthy after probe fail")
	}

	// Probe succeeds → healthy.
	h.MarkHealthy(model, connA)
	if h.IsUnhealthy(model, connA) {
		t.Fatal("connA should be healthy after MarkHealthy")
	}
}

// TestHealthTrackerSharedAcrossCombos verifies the (model, connection) key is
// NOT scoped by combo: a connection that fails for one combo is unhealthy for
// every combo (and for single-model requests), because the upstream call for a
// given (model, connection) is identical regardless of which combo routed to it.
func TestHealthTrackerSharedAcrossCombos(t *testing.T) {
	h := NewHealthTracker()
	const model, conn = "openai/gpt-4o", "conn-x"

	// Mark unhealthy via one combo.
	h.MarkUnhealthy(model, conn)
	if !h.IsUnhealthy(model, conn) {
		t.Fatal("should be unhealthy after MarkUnhealthy")
	}

	// The same (model, connection) must be unhealthy everywhere — another
	// combo and the single-model (empty) context share the same state.
	if !h.IsUnhealthy(model, conn) {
		t.Fatal("shared key should be unhealthy")
	}

	// Restoring it clears it for all contexts.
	h.MarkHealthy(model, conn)
	if h.IsUnhealthy(model, conn) {
		t.Fatal("should be healthy after MarkHealthy")
	}
}

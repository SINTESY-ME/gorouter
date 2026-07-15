package app

import "testing"

func TestHealthTrackerPerConnection(t *testing.T) {
	h := NewHealthTracker()
	const combo, model, connA, connB = "smart", "openai/gpt-4o", "conn-a", "conn-b"

	// Initially everything is healthy.
	if h.IsUnhealthy(combo, model, connA) {
		t.Fatal("connA should be healthy initially")
	}

	// Mark only connA unhealthy — connB must stay healthy.
	h.MarkUnhealthy(combo, model, connA)
	if !h.IsUnhealthy(combo, model, connA) {
		t.Fatal("connA should be unhealthy")
	}
	if h.IsUnhealthy(combo, model, connB) {
		t.Fatal("connB should still be healthy")
	}

	// Probe can start for unhealthy connA but not for healthy connB.
	if !h.TryStartProbe(combo, model, connA) {
		t.Fatal("probe should start for unhealthy connA")
	}
	if h.TryStartProbe(combo, model, connA) {
		t.Fatal("second probe should not start — already in flight")
	}

	// Probe fails → flag released, still unhealthy.
	h.ProbeFailed(combo, model, connA)
	if !h.IsUnhealthy(combo, model, connA) {
		t.Fatal("connA should still be unhealthy after probe fail")
	}

	// Probe succeeds → healthy.
	h.MarkHealthy(combo, model, connA)
	if h.IsUnhealthy(combo, model, connA) {
		t.Fatal("connA should be healthy after MarkHealthy")
	}
}

func TestHealthTrackerSingleModel(t *testing.T) {
	h := NewHealthTracker()
	// Single model requests use empty combo name.
	const combo, model, conn = "", "openai/gpt-4o", "conn-x"

	h.MarkUnhealthy(combo, model, conn)
	if !h.IsUnhealthy(combo, model, conn) {
		t.Fatal("should be unhealthy for single model")
	}

	// Must not collide with the same model in a combo.
	h.MarkUnhealthy("mycombo", model, conn)
	if !h.IsUnhealthy("mycombo", model, conn) {
		t.Fatal("should be unhealthy in combo context")
	}
	// Single model entry must be independent.
	if !h.IsUnhealthy(combo, model, conn) {
		t.Fatal("single model entry should be independent of combo entry")
	}
}
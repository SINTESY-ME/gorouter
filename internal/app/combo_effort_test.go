package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// A combo member can pin the reasoning level it runs at. The pin has to hold
// on the fallback path too — the candidate that actually serves is not always
// the first in the list — and it must survive the per-model adaptation.
func TestComboMemberEffortPinsPerCandidate(t *testing.T) {
	const response = `{"id":"ok","choices":[{"message":{"content":"ok"}}]}`
	exec := &mockExecutor{
		status: 200,
		body:   response,
		failModels: map[string]int{
			"pinned-low":    503, // caller pinned low, fails anyway
			"pinned-max":    503, // pinned max but the model stops at high
			"pinned-unseen": 503, // pinned on a model with no known ladder
		},
	}
	comboRepo := &mockComboRepo{combos: map[string]*domain.Combo{
		"pinned-combo": {
			Name:     "pinned-combo",
			Models:   []string{"p/pinned-low", "p/pinned-max", "p/pinned-unseen", "p/free"},
			Strategy: StrategyOrderedFallback,
			ModelMeta: map[string]domain.ComboModelMeta{
				"p/pinned-low":    {Effort: "low"},
				"p/pinned-max":    {Effort: "max"},
				"p/pinned-unseen": {Effort: "high"},
			},
		},
	}}
	connRepo := &mockConnectionRepo{conns: []domain.Connection{
		{ID: "cx", ProviderID: "p", Name: "p", IsActive: true},
	}}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	srv.Pricing = &PricingCache{reasoning: map[string]domain.ReasoningCapabilities{
		"p/pinned-low": {Known: true, SupportsReasoning: true, Efforts: []string{"low", "high"}, EffortsStated: true},
		"p/pinned-max": {Known: true, SupportsReasoning: true},
		"p/free":       {Known: true, SupportsReasoning: true, SupportsXHighReasoningEffort: true},
	}}

	// The caller asks for max; the pins must outrank it for pinned members and
	// leave the unpinned one alone.
	body := []byte(`{"model":"pinned-combo","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"max"}`)
	res, err := srv.RouteChat(context.Background(), body, "pinned-combo", false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	seen := map[string]map[string]any{}
	for i, raw := range exec.sentBodies {
		var wire map[string]any
		if err := json.Unmarshal([]byte(raw), &wire); err != nil {
			t.Fatalf("sent body %d is invalid JSON: %v", i, err)
		}
		if model, _ := wire["model"].(string); model != "" {
			if _, exists := seen[model]; !exists {
				seen[model] = wire
			}
		}
	}

	if got := seen["pinned-low"]["reasoning_effort"]; got != "low" {
		t.Fatalf("pinned candidate sent effort %v, want low (the pin must outrank the caller's max)", got)
	}
	if got := seen["pinned-max"]["reasoning_effort"]; got != "high" {
		t.Fatalf("candidate pinned to max sent %v, want high (degraded to its ladder)", got)
	}
	if got := seen["free"]["reasoning_effort"]; got != "xhigh" {
		t.Fatalf("unpinned candidate sent %v, want the caller's xhigh", got)
	}
	// A pin on a model nobody has metadata for must not leak an unsupported
	// provider parameter: the field is dropped instead.
	if _, ok := seen["pinned-unseen"]["reasoning_effort"]; ok {
		t.Fatalf("pin leaked onto a model with no known ladder: %v", seen["pinned-unseen"])
	}
}

// A combo that pins nothing keeps the old behaviour exactly: the caller's own
// effort reaches the model untouched.
func TestComboWithoutPinsLeavesEffortAlone(t *testing.T) {
	const response = `{"id":"ok","choices":[{"message":{"content":"ok"}}]}`
	exec := &mockExecutor{status: 200, body: response}
	comboRepo := &mockComboRepo{combos: map[string]*domain.Combo{
		"plain": {
			Name:      "plain",
			Models:    []string{"p/one"},
			Strategy:  StrategyOrderedFallback,
			ModelMeta: map[string]domain.ComboModelMeta{"p/one": {Description: "only member"}},
		},
	}}
	connRepo := &mockConnectionRepo{conns: []domain.Connection{
		{ID: "cx", ProviderID: "p", Name: "p", IsActive: true},
	}}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	// The member states a ladder that contains the level the caller asked for,
	// so nothing in the chain has a reason to change it.
	srv.Pricing = &PricingCache{reasoning: map[string]domain.ReasoningCapabilities{
		"p/one": {Known: true, SupportsReasoning: true, Efforts: []string{"low", "medium", "high"}, EffortsStated: true},
	}}

	body := []byte(`{"model":"plain","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"low"}`)
	res, err := srv.RouteChat(context.Background(), body, "plain", false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	var wire map[string]any
	if err := json.Unmarshal([]byte(exec.sentBodies[0]), &wire); err != nil {
		t.Fatal(err)
	}
	if got := wire["reasoning_effort"]; got != "low" {
		t.Fatalf("unpinned combo changed the caller's effort to %v", got)
	}
}

// A pin that is not a level of the canonical ladder is rejected where it was
// typed, instead of being silently degraded at request time.
func TestComboValidationRejectsUnknownEffortPin(t *testing.T) {
	svc := &ComboService{Repo: &mockComboRepo{}}
	combo := &domain.Combo{
		Name:   "typo",
		Models: []string{"p/one"},
		ModelMeta: map[string]domain.ComboModelMeta{
			"p/one": {Effort: "turbo"},
		},
	}
	if err := svc.Create(context.Background(), combo); err == nil {
		t.Fatal("an unknown reasoning effort must be rejected")
	}
}

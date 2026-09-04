package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestRouteComboAdaptsMaxPerCandidate(t *testing.T) {
	const response = `{"id":"ok","choices":[{"message":{"content":"ok"}}]}`
	exec := &mockExecutor{
		status: 200,
		body:   response,
		failModels: map[string]int{
			"model-xhigh": 503,
			"model-high":  503,
		},
	}
	comboRepo := &mockComboRepo{combos: map[string]*domain.Combo{
		"reasoning-combo": {
			Name:     "reasoning-combo",
			Models:   []string{"p/model-xhigh", "p/model-high", "p/model-none"},
			Strategy: StrategyOrderedFallback,
		},
	}}
	connRepo := &mockConnectionRepo{conns: []domain.Connection{
		{ID: "cx", ProviderID: "p", Name: "p", IsActive: true},
	}}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	srv.Pricing = &PricingCache{reasoning: map[string]domain.ReasoningCapabilities{
		"p/model-xhigh": {Known: true, SupportsReasoning: true, SupportsXHighReasoningEffort: true},
		"p/model-high":  {Known: true, SupportsReasoning: true},
		"p/model-none":  {Known: true},
	}}

	body := []byte(`{"model":"reasoning-combo","messages":[{"role":"user","content":"solve"}],"reasoning_effort":"max"}`)
	res, err := srv.RouteChat(context.Background(), body, "reasoning-combo", false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
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
		model, _ := wire["model"].(string)
		if _, exists := seen[model]; !exists {
			seen[model] = wire
		}
	}
	if got := seen["model-xhigh"]["reasoning_effort"]; got != "xhigh" {
		t.Fatalf("xhigh candidate received effort %v, want xhigh", got)
	}
	if got := seen["model-high"]["reasoning_effort"]; got != "high" {
		t.Fatalf("high candidate received effort %v, want high", got)
	}
	if _, ok := seen["model-none"]["reasoning_effort"]; ok {
		t.Fatalf("unsupported candidate received reasoning_effort: %v", seen["model-none"])
	}
}

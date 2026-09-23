package app

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// routeBodyForCodexCase routes one non-streaming chat request to a single
// provider and returns the body the executor actually sent upstream.
func routeBodyForCodexCase(t *testing.T, provider, model, body string) string {
	t.Helper()
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID:         "c1",
			ProviderID: provider,
			Name:       "test",
			IsActive:   true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	payload := []byte(body)
	modelStr, _ := extractModel(payload)
	res, err := srv.RouteChat(context.Background(), payload, modelStr, false, "test-key", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("RouteChat: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	if len(exec.sentBodies) == 0 {
		t.Fatal("no upstream body captured")
	}
	return exec.sentBodies[len(exec.sentBodies)-1]
}

// The Codex route pins the level it runs at instead of letting the upstream
// default apply, so the behaviour is a recorded decision. Medium is the level
// the reference client uses as its connection default.
func TestCodexRouteAppliesDefaultReasoningEffort(t *testing.T) {
	sent := routeBodyForCodexCase(t, "codex", "codex/gpt-5-codex",
		`{"model":"codex/gpt-5-codex","messages":[{"role":"user","content":"hi"}]}`)
	if !strings.Contains(sent, `"reasoning_effort":"medium"`) {
		t.Fatalf("codex route must pin the default effort, sent body: %s", sent)
	}
}

// A caller that names its own level keeps it: the default fills a silence, it
// does not override an instruction.
//
// The level asserted is "high" because the ladder here comes from the name
// heuristic (no Pricing in this harness), which knows none/medium/high. Asking
// for "low" would exercise the pre-existing never-upgrade rule (a ladder
// without "low" drops the field) instead of the default-vs-caller precedence
// this test is about.
func TestCodexRouteKeepsCallerReasoningEffort(t *testing.T) {
	sent := routeBodyForCodexCase(t, "codex", "codex/gpt-5-codex",
		`{"model":"codex/gpt-5-codex","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`)
	if strings.Contains(sent, `"medium"`) {
		t.Fatalf("caller effort must survive, sent body: %s", sent)
	}
	if !strings.Contains(sent, `"reasoning_effort":"high"`) {
		t.Fatalf("caller effort high must be preserved, sent body: %s", sent)
	}
}

// Only the Codex transport gets the default: every other provider must see the
// request untouched, or the gateway would inject a reasoning parameter into
// providers that never asked for one.
func TestNonCodexRouteDoesNotGainReasoningEffort(t *testing.T) {
	sent := routeBodyForCodexCase(t, "openai", "openai/gpt-4",
		`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	if bytes.Contains([]byte(sent), []byte("reasoning")) {
		t.Fatalf("non-codex route must not gain a reasoning field, sent body: %s", sent)
	}
}

// A model the catalog does not describe as reasoning-capable must not receive a
// reasoning parameter at all: the default is adapted per candidate like any
// other request value.
func TestCodexRouteDropsDefaultForNonReasoningModel(t *testing.T) {
	sent := routeBodyForCodexCase(t, "codex", "codex/llama-local",
		`{"model":"codex/llama-local","messages":[{"role":"user","content":"hi"}]}`)
	if bytes.Contains([]byte(sent), []byte("reasoning_effort")) {
		t.Fatalf("non-reasoning model must not receive reasoning_effort, sent body: %s", sent)
	}
}

// The caller's Codex version reaches the executor's context, so the identity
// decision travels with the request instead of being re-derived at the wire.
func TestCallerCodexVersionReachesRouteContext(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	connRepo := &mockConnectionRepo{conns: []domain.Connection{{ID: "c1", ProviderID: "codex", IsActive: true}}}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})

	ctx := domain.WithCodexClientVersion(context.Background(), "0.154.0")
	payload := []byte(`{"model":"codex/gpt-5-codex","messages":[{"role":"user","content":"hi"}]}`)
	modelStr, _ := extractModel(payload)
	res, err := srv.RouteChat(ctx, payload, modelStr, false, "test-key", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("RouteChat: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	if got := domain.CodexClientVersionFromCtx(ctx); got != "0.154.0" {
		t.Fatalf("context lost the caller version: %q", got)
	}
}

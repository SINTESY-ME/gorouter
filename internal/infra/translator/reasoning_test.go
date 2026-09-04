package translator

import (
	"encoding/json"
	"testing"
)

func TestReasoningEffortFromRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
		ok   bool
		err  bool
	}{
		{name: "string", body: `{"reasoning_effort":"high"}`, want: "high", ok: true},
		{name: "responses object", body: `{"reasoning_effort":{"effort":"medium","summary":"detailed"}}`, want: "medium", ok: true},
		{name: "none", body: `{"reasoning_effort":"none"}`, want: "none", ok: true},
		{name: "omitted", body: `{}`, want: "", ok: false},
		{name: "invalid", body: `{"reasoning_effort":"turbo"}`, want: "", ok: false, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatal(err)
			}
			got, ok, err := parseReasoningEffort(req["reasoning_effort"])
			if err != nil {
				if !tt.err {
					t.Fatal(err)
				}
				return
			}
			if tt.err {
				t.Fatal("expected invalid reasoning_effort error")
			}
			if got != tt.want || ok != tt.ok {
				t.Fatalf("parseReasoningEffort() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestReasoningEffortToResponses(t *testing.T) {
	got, err := reasoningForResponses("high")
	if err != nil {
		t.Fatal(err)
	}
	if got["effort"] != "high" {
		t.Fatalf("effort = %v, want high", got["effort"])
	}
}

func TestReasoningEffortToAnthropic(t *testing.T) {
	got, maxTokens, err := reasoningForAnthropic("high", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if got["type"] != "enabled" || got["budget_tokens"] != 4096 {
		t.Fatalf("thinking = %#v, want enabled/4096", got)
	}
	if maxTokens != 10000 {
		t.Fatalf("max_tokens = %d, want 10000", maxTokens)
	}
	adaptive, output, _, err := reasoningForAnthropicModel("claude-opus-4-6", "high", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if adaptive["type"] != "adaptive" || output["effort"] != "high" {
		t.Fatalf("adaptive thinking/output = %#v/%#v", adaptive, output)
	}
}

func TestReasoningEffortToGemini2(t *testing.T) {
	got := reasoningForGemini("gemini-2.5-pro", "high")
	if got["thinkingBudget"] != 4096 || got["includeThoughts"] != true {
		t.Fatalf("thinkingConfig = %#v, want budget 4096 and includeThoughts=true", got)
	}
	minimal := reasoningForGemini("gemini-2.5-flash-lite", "minimal")
	if minimal["thinkingBudget"] != 512 {
		t.Fatalf("minimal flash-lite budget = %v, want 512", minimal["thinkingBudget"])
	}
}

func TestReasoningEffortToGemini3(t *testing.T) {
	got := reasoningForGemini("gemini-3.1-pro-preview", "medium")
	if got["thinkingLevel"] != "medium" || got["includeThoughts"] != true {
		t.Fatalf("thinkingConfig = %#v, want level medium and includeThoughts=true", got)
	}
}

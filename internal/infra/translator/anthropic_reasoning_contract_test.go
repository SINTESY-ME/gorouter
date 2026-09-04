package translator

import (
	"encoding/json"
	"testing"
)

func TestAnthropicAdaptiveReasoningAndSmallBudget(t *testing.T) {
	adaptiveBody := `{"model":"client","messages":[{"role":"user","content":"solve"}],"reasoning_effort":"high","max_tokens":1000}`
	out, err := translateOpenAIToAnthropicRequest("claude-opus-4-6", []byte(adaptiveBody))
	if err != nil {
		t.Fatal(err)
	}
	var adaptive map[string]any
	if err := json.Unmarshal(out, &adaptive); err != nil {
		t.Fatal(err)
	}
	if adaptive["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Fatalf("thinking = %#v", adaptive["thinking"])
	}
	if adaptive["output_config"].(map[string]any)["effort"] != "high" {
		t.Fatalf("output_config = %#v", adaptive["output_config"])
	}

	legacyBody := `{"model":"client","messages":[{"role":"user","content":"solve"}],"reasoning_effort":"high","max_tokens":1000}`
	out, err = translateOpenAIToAnthropicRequest("claude-sonnet-4", []byte(legacyBody))
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(out, &legacy); err != nil {
		t.Fatal(err)
	}
	if _, exists := legacy["thinking"]; exists {
		t.Fatalf("small max_tokens should omit invalid thinking, got %#v", legacy["thinking"])
	}
	if legacy["max_tokens"] != float64(1000) {
		t.Fatalf("max_tokens was inflated: %v", legacy["max_tokens"])
	}
}

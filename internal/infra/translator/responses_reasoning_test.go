package translator

import (
	"encoding/json"
	"testing"
)

func TestResponsesToOpenAIRequestCarriesReasoningEffort(t *testing.T) {
	body := `{"model":"gpt-5","input":"solve","reasoning":{"effort":"high"}}`
	out, err := translateResponsesToOpenAIRequest("gpt-4o", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", wire["reasoning_effort"])
	}
}

package translator

import (
	"encoding/json"
	"testing"
)

func TestReasoningUsesMaxCompletionTokens(t *testing.T) {
	body := `{"model":"client","messages":[{"role":"user","content":"solve"}],"reasoning_effort":"high","max_completion_tokens":10000}`
	out, err := translateOpenAIToResponsesRequest("gpt-5", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["max_output_tokens"] != float64(10000) {
		t.Fatalf("max_output_tokens = %v, want 10000", wire["max_output_tokens"])
	}
}

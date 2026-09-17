package translator

import (
	"encoding/json"
	"testing"
)

// The ChatGPT Codex Responses backend rejects max_output_tokens with a
// deterministic 400 ("Unsupported parameter: max_output_tokens"), which
// terminates the combo cascade (ShouldFallback(400)=false). The translator
// must therefore omit the parameter on the Responses wire, regardless of
// which max-token field the client used (max_tokens or max_completion_tokens).
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
	if _, ok := wire["max_output_tokens"]; ok {
		t.Fatalf("max_output_tokens must be omitted on the Responses wire (Codex backend rejects it), got %v", wire["max_output_tokens"])
	}
	if _, ok := wire["reasoning"]; !ok {
		t.Fatalf("reasoning object must still be forwarded, got none")
	}
}

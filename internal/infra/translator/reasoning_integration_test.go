package translator

import (
	"encoding/json"
	"testing"
)

func TestOpenAIToProviderReasoningHigh(t *testing.T) {
	body := `{"model":"client","messages":[{"role":"user","content":"solve"}],"reasoning_effort":"high","max_tokens":10000}`

	responses, err := translateOpenAIToResponsesRequest("gpt-5", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var responseWire map[string]any
	if err := json.Unmarshal(responses, &responseWire); err != nil {
		t.Fatal(err)
	}
	if got := responseWire["reasoning"].(map[string]any)["effort"]; got != "high" {
		t.Fatalf("Responses reasoning.effort = %v, want high", got)
	}

	anthropic, err := translateOpenAIToAnthropicRequest("claude-sonnet-4", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var anthropicWire map[string]any
	if err := json.Unmarshal(anthropic, &anthropicWire); err != nil {
		t.Fatal(err)
	}
	thinking := anthropicWire["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(4096) {
		t.Fatalf("Anthropic thinking = %#v", thinking)
	}
	if anthropicWire["max_tokens"] != float64(10000) {
		t.Fatalf("Anthropic max_tokens = %v, want 10000", anthropicWire["max_tokens"])
	}

	gemini, err := translateOpenAIToGeminiRequest("gemini-2.5-pro", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var geminiWire map[string]any
	if err := json.Unmarshal(gemini, &geminiWire); err != nil {
		t.Fatal(err)
	}
	thinkingConfig := geminiWire["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
	if thinkingConfig["thinkingBudget"] != float64(4096) || thinkingConfig["includeThoughts"] != true {
		t.Fatalf("Gemini thinkingConfig = %#v", thinkingConfig)
	}
}

func TestAnthropicReasoningSummaryIsExposed(t *testing.T) {
	body := `{"id":"msg_1","model":"claude-sonnet-4","content":[{"type":"thinking","thinking":"private summary"},{"type":"text","text":"answer"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`
	out, err := translateAnthropicToOpenAIResponseJSONImpl([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatal(err)
	}
	message := wire["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "answer" || message["reasoning_content"] != "private summary" {
		t.Fatalf("message = %#v", message)
	}
}

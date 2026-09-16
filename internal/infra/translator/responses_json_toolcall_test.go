package translator

import (
	"encoding/json"
	"testing"
)

// TestResponsesJSONKeepsToolCalls covers the non-streamed path: a provider
// that answers a Responses request with a plain (non-SSE) chat completion can
// reply with tool calls and no text. Dropping them left Codex with an empty
// turn, which reads as "stopped at the tool call".
func TestResponsesJSONKeepsToolCalls(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-ns","model":"glm-5.3-flash",
		"choices":[{"index":0,"message":{"content":"","tool_calls":[
			{"index":0,"id":"call_ns1","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"ls\"}"}}
		]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5}
	}`)

	out, err := translateOpenAIToResponsesResponseJSON(body)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	var resp struct {
		Status string `json:"status"`
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("expected 1 output item (the function call), got %d: %s", len(resp.Output), out)
	}
	got := resp.Output[0]
	if got.Type != "function_call" {
		t.Errorf("output type = %q, want function_call", got.Type)
	}
	if got.Name != "shell" {
		t.Errorf("name = %q, want shell", got.Name)
	}
	if got.CallID != "call_ns1" {
		t.Errorf("call_id = %q, want call_ns1 (Codex matches the tool result on this)", got.CallID)
	}
	if got.Arguments != `{"cmd":"ls"}` {
		t.Errorf("arguments = %q, want the JSON string verbatim", got.Arguments)
	}
}

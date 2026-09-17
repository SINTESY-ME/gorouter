package app

import (
	"encoding/json"
	"testing"
)

func decodeJSONMap(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	return out
}

// --- /v1/responses -------------------------------------------------------

func TestResponsesAgentToolCallsAndTurn(t *testing.T) {
	resp := []byte(`{"id":"resp_1","output":[
		{"type":"reasoning","id":"rs_1","summary":[]},
		{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"checking"}]},
		{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"github__create_issue","arguments":"{\"title\":\"x\"}"}
	]}`)
	proto := responsesAgent{}
	calls, err := proto.ToolCalls(resp)
	if err != nil {
		t.Fatalf("ToolCalls: %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "github__create_issue" || calls[0].ID != "call_abc" {
		t.Fatalf("unexpected calls: %+v", calls)
	}

	// input as a plain string (Codex always sends an array, but the API allows
	// a string and the adapter must normalize it).
	prev := []byte(`{"model":"coding","instructions":"be brief","input":"hello"}`)
	next, err := proto.AppendTurn(prev, resp, []agentToolResult{{CallID: "call_abc", Text: "issue #42"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	fields := decodeJSONMap(t, next)
	if string(fields["instructions"]) != `"be brief"` {
		t.Fatalf("instructions must survive the turn: %s", next)
	}
	var items []map[string]any
	if err := json.Unmarshal(fields["input"], &items); err != nil {
		t.Fatalf("input is not an item array: %v (%s)", err, next)
	}
	// user message + reasoning + message + function_call + function_call_output
	if len(items) != 5 {
		t.Fatalf("expected 5 input items, got %d: %s", len(items), next)
	}
	if items[0]["role"] != "user" {
		t.Fatalf("string input must become a user message: %+v", items[0])
	}
	last := items[len(items)-1]
	if last["type"] != "function_call_output" || last["call_id"] != "call_abc" || last["output"] != "issue #42" {
		t.Fatalf("unexpected function_call_output: %+v", last)
	}
	if items[3]["type"] != "function_call" || items[3]["name"] != "github__create_issue" {
		t.Fatalf("the assistant's function_call must be replayed: %+v", items[3])
	}
}

func TestResponsesAgentAppendsToItemArray(t *testing.T) {
	proto := responsesAgent{}
	prev := []byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	resp := []byte(`{"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"a__b","arguments":"{}"}]}`)
	next, err := proto.AppendTurn(prev, resp, []agentToolResult{{CallID: "call_1", Text: "ok"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal(decodeJSONMap(t, next)["input"], &items); err != nil {
		t.Fatalf("input is not an item array: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected user+function_call+function_call_output, got %d: %s", len(items), next)
	}
}

// --- /v1/messages --------------------------------------------------------

func TestAnthropicAgentToolCallsAndTurn(t *testing.T) {
	resp := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[
		{"type":"text","text":"checking the stock"},
		{"type":"tool_use","id":"toolu_7","name":"github__create_issue","input":{"title":"x"}}
	]}`)
	proto := anthropicAgent{}
	calls, err := proto.ToolCalls(resp)
	if err != nil {
		t.Fatalf("ToolCalls: %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "github__create_issue" || calls[0].ID != "toolu_7" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if calls[0].Args != `{"title":"x"}` {
		t.Fatalf("arguments must be the raw input object: %q", calls[0].Args)
	}

	prev := []byte(`{"model":"coding","max_tokens":1024,"messages":[{"role":"user","content":"create an issue"}]}`)
	next, err := proto.AppendTurn(prev, resp, []agentToolResult{{CallID: "toolu_7", Text: "issue #42"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	fields := decodeJSONMap(t, next)
	if string(fields["max_tokens"]) != "1024" {
		t.Fatalf("max_tokens must survive the turn: %s", next)
	}
	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(fields["messages"], &msgs); err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected user+assistant+user, got %d: %s", len(msgs), next)
	}
	var assistantBlocks []map[string]any
	if err := json.Unmarshal(msgs[1].Content, &assistantBlocks); err != nil {
		t.Fatalf("assistant content: %v", err)
	}
	if msgs[1].Role != "assistant" || len(assistantBlocks) != 2 || assistantBlocks[1]["type"] != "tool_use" {
		t.Fatalf("assistant turn must replay the tool_use block: %+v", assistantBlocks)
	}
	var resultBlocks []map[string]any
	if err := json.Unmarshal(msgs[2].Content, &resultBlocks); err != nil {
		t.Fatalf("tool result content: %v", err)
	}
	block := resultBlocks[0]
	if msgs[2].Role != "user" || block["type"] != "tool_result" || block["tool_use_id"] != "toolu_7" || block["content"] != "issue #42" {
		t.Fatalf("unexpected tool_result message: %+v", resultBlocks)
	}
}

// --- ownership -----------------------------------------------------------

// A turn that asks for a tool gorouter does not own belongs to the client:
// executing it here would answer a tool the client is responsible for.
func TestAgentLoopStopsOnClientOwnedTool(t *testing.T) {
	owned := map[string]bool{"github__create_issue": true}
	calls := []agentToolCall{
		{ID: "call_1", Name: "github__create_issue"},
		{ID: "call_2", Name: "shell"},
	}
	if allOwned(calls, owned) {
		t.Fatalf("a foreign tool must stop the loop")
	}
	if !allOwned(calls[:1], owned) {
		t.Fatalf("a purely MCP turn must run the loop")
	}
	if allOwned([]agentToolCall{{Name: "shell"}}, owned) {
		t.Fatalf("an unknown tool is not owned")
	}
}

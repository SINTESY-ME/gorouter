package app

import (
	"encoding/json"
	"testing"
)

// The loop replays the assistant turn it just answered. Upstreams routinely
// put a whitespace-only text block next to the tool call, and providers reject
// or answer empty when it comes back; these tests pin that the blank parts are
// dropped and the tool call survives.

func TestAnthropicAgentDropsBlankTextBlock(t *testing.T) {
	prev := []byte(`{"messages":[{"role":"user","content":"quanto custa o ABC-123?"}]}`)
	resp := []byte(`{"content":[{"type":"text","text":"  "},{"type":"tool_use","id":"toolu_1","name":"lojateste__consultar_preco","input":{"sku":"ABC-123"}}]}`)

	out, err := anthropicAgent{}.AppendTurn(prev, resp, []agentToolResult{{CallID: "toolu_1", Text: "4321 reais"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("want 3 messages (user, assistant, tool results), got %d", len(body.Messages))
	}
	assistant := body.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("want assistant turn, got %q", assistant.Role)
	}
	var blocks []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(assistant.Content, &blocks); err != nil {
		t.Fatalf("decode assistant content: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Type != "tool_use" {
		t.Fatalf("want a single tool_use block, got %+v", blocks)
	}
	if blocks[0].Name != "lojateste__consultar_preco" {
		t.Fatalf("tool call lost its name: %+v", blocks[0])
	}
	var last []json.RawMessage
	if err := json.Unmarshal(body.Messages[2].Content, &last); err != nil || body.Messages[2].Role != "user" || len(last) != 1 {
		t.Fatalf("want one user message carrying the tool result, got %+v", body.Messages[2])
	}
}

func TestAnthropicAgentKeepsRealText(t *testing.T) {
	prev := []byte(`{"messages":[{"role":"user","content":"quanto custa?"}]}`)
	resp := []byte(`{"content":[{"type":"text","text":"Deixa eu checar."},{"type":"tool_use","id":"toolu_9","name":"lojateste__consultar_preco","input":{}}]}`)

	out, err := anthropicAgent{}.AppendTurn(prev, resp, []agentToolResult{{CallID: "toolu_9", Text: "4321"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	var body struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body.Messages[1].Content, &blocks); err != nil {
		t.Fatalf("decode assistant content: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want text + tool_use kept, got %+v", blocks)
	}
	if blocks[0].Text != "Deixa eu checar." {
		t.Fatalf("text block was altered: %+v", blocks[0])
	}
}

func TestResponsesAgentDropsEmptyMessageItem(t *testing.T) {
	prev := []byte(`{"input":"quanto custa o ABC-123?"}`)
	resp := []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"  "}]},{"type":"function_call","call_id":"call_1","name":"lojateste__consultar_preco","arguments":"{\"sku\":\"ABC-123\"}"}]}`)

	out, err := responsesAgent{}.AppendTurn(prev, resp, []agentToolResult{{CallID: "call_1", Text: "4321 reais"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	var body struct {
		Input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// user item + function_call + function_call_output
	if len(body.Input) != 3 {
		t.Fatalf("want 3 input items (empty message dropped), got %d: %+v", len(body.Input), body.Input)
	}
	if body.Input[1].Type != "function_call" {
		t.Fatalf("want the function_call replayed, got %+v", body.Input[1])
	}
	if body.Input[2].Type != "function_call_output" || body.Input[2].CallID != "call_1" {
		t.Fatalf("tool output must carry the original call_id, got %+v", body.Input[2])
	}
	if body.Input[2].Output != "4321 reais" {
		t.Fatalf("tool output text lost: %+v", body.Input[2])
	}
}

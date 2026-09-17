package translator

import (
	"encoding/json"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// Claude Code sends its tool definitions with every request (Anthropic
// `tools`/`input_schema`) and replays the conversation with `tool_use`
// (assistant) and `tool_result` (user) content blocks. An OpenAI upstream
// needs all three translated: dropping the tools means the model can never
// call one, and raw tool_result blocks poison the next turn.
func TestAnthropicRequestForwardsToolsAndToolBlocks(t *testing.T) {
	body := `{
	  "model":"coding","max_tokens":1024,"stream":false,
	  "system":"agent",
	  "tools":[{"name":"shell","description":"run a command",
	            "input_schema":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}],
	  "tool_choice":{"type":"auto"},
	  "messages":[
	    {"role":"user","content":"liste os arquivos"},
	    {"role":"assistant","content":[{"type":"text","text":"vou listar"},{"type":"tool_use","id":"toolu_1","name":"shell","input":{"cmd":"ls"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a.txt\nb.txt"}]}
	  ]}`

	tr := New()
	out, err := tr.TranslateRequest(domain.FormatAnthropic, domain.FormatOpenAI, "upstream-model", []byte(body))
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	var req struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice json.RawMessage `json:"tool_choice"`
		Messages   []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal translated request: %v (%s)", err, out)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("tools = %#v, want the client's tool definition forwarded", req.Tools)
	}
	if req.Tools[0].Type != "function" || req.Tools[0].Function.Name != "shell" {
		t.Errorf("tools[0] = %+v, want a function tool named shell", req.Tools[0])
	}
	if req.Tools[0].Function.Parameters["type"] != "object" {
		t.Errorf("tools[0].function.parameters = %#v, want the input_schema as parameters", req.Tools[0].Function.Parameters)
	}
	if len(req.ToolChoice) == 0 || string(req.ToolChoice) == "null" {
		t.Error("tool_choice was dropped")
	}

	if len(req.Messages) != 4 { // system + user + assistant + tool
		t.Fatalf("got %d messages, want 4 (system, user, assistant with tool_calls, tool result): %+v", len(req.Messages), req.Messages)
	}
	assistant := req.Messages[2]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls = %#v, want the tool_use block as a tool call", assistant.ToolCalls)
	}
	if assistant.ToolCalls[0].ID != "toolu_1" || assistant.ToolCalls[0].Function.Name != "shell" {
		t.Errorf("assistant tool_call = %+v, want id toolu_1 / name shell", assistant.ToolCalls[0])
	}
	if assistant.ToolCalls[0].Function.Arguments != `{"cmd":"ls"}` {
		t.Errorf("assistant tool_call arguments = %q, want {\"cmd\":\"ls\"}", assistant.ToolCalls[0].Function.Arguments)
	}

	tool := req.Messages[3]
	if tool.Role != "tool" {
		t.Fatalf("last message role = %q, want tool", tool.Role)
	}
	if tool.ToolCallID != "toolu_1" {
		t.Errorf("tool message tool_call_id = %q, want toolu_1", tool.ToolCallID)
	}
	if s, ok := tool.Content.(string); !ok || s != "a.txt\nb.txt" {
		t.Errorf("tool message content = %#v, want the flattened tool_result text", tool.Content)
	}
}

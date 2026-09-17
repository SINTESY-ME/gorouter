package translator

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// Claude Code reads /v1/messages SSE strictly: it needs the complete Anthropic
// event sequence (message_start -> content_block_start -> deltas ->
// content_block_stop -> message_delta -> message_stop) and it aborts the turn
// with "Streaming response ended before any complete data was received" when a
// block is never closed or the stop_reason/usage never arrive. Tool calls must
// come out as tool_use blocks with input_json_delta partials, otherwise a
// coding CLI gets a turn with no tool to run.
const openAIStreamWithToolCall = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"deepseek-v4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello "},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"deepseek-v4","choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"deepseek-v4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a1","type":"function","function":{"name":"shell","arguments":"{\"cmd\":"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"deepseek-v4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","model":"deepseek-v4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}

data: [DONE]

`

// collectAnthropicEvents parses the outgoing SSE into (eventName, payload) pairs
// and fails if any payload lacks its Anthropic "type" discriminator.
func collectAnthropicEvents(t *testing.T, raw string) []struct {
	Event string
	Data  map[string]any
} {
	t.Helper()
	var out []struct {
		Event string
		Data  map[string]any
	}
	for _, block := range strings.Split(raw, "\n\n") {
		var ev string
		var data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if ev == "" && data == "" {
			continue
		}
		if ev == "" {
			t.Errorf("event block without an `event:` line: %q", block)
			continue
		}
		if data == "" {
			t.Errorf("event %q has no data payload", ev)
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Errorf("event %q payload is not JSON: %v (%q)", ev, err, data)
			continue
		}
		if payload["type"] == nil {
			t.Errorf("event %q payload has no \"type\" field: %s", ev, data)
		}
		if payload["type"] != nil && payload["type"] != ev {
			t.Errorf("event name %q != payload type %v", ev, payload["type"])
		}
		out = append(out, struct {
			Event string
			Data  map[string]any
		}{ev, payload})
	}
	return out
}

func streamAnthropic(t *testing.T, upstream string) string {
	t.Helper()
	tr := New()
	rc, err := tr.TranslateResponseStream(context.Background(), domain.FormatOpenAI, domain.FormatAnthropic, io.NopCloser(strings.NewReader(upstream)))
	if err != nil {
		t.Fatalf("TranslateResponseStream: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	return string(b)
}

func TestAnthropicStreamClosesBlocksAndSendsStopReason(t *testing.T) {
	raw := streamAnthropic(t, openAIStreamWithToolCall)
	events := collectAnthropicEvents(t, raw)

	var order []string
	for _, e := range events {
		order = append(order, e.Event)
	}

	// message_start must open the message with an EMPTY content array
	// (Anthropic never sends a block inside message_start).
	if len(events) == 0 || events[0].Event != "message_start" {
		t.Fatalf("stream must start with message_start, got %v", order)
	}
	msg, _ := events[0].Data["message"].(map[string]any)
	if msg == nil {
		t.Fatalf("message_start has no message object")
	}
	if c, ok := msg["content"].([]any); !ok || len(c) != 0 {
		t.Errorf("message_start.message.content = %#v, want []", msg["content"])
	}
	if msg["role"] != "assistant" {
		t.Errorf("message_start.message.role = %v, want assistant", msg["role"])
	}

	// Every opened content block must be closed.
	var opened, closed []int
	var toolUse map[string]any
	for _, e := range events {
		switch e.Event {
		case "content_block_start":
			opened = append(opened, int(e.Data["index"].(float64)))
			cb, _ := e.Data["content_block"].(map[string]any)
			if cb != nil && cb["type"] == "tool_use" {
				toolUse = cb
			}
		case "content_block_stop":
			closed = append(closed, int(e.Data["index"].(float64)))
		}
	}
	if len(opened) != len(closed) {
		t.Fatalf("opened blocks %v but closed %v — Claude Code aborts a turn with an unclosed block", opened, closed)
	}
	for i := range opened {
		if opened[i] != closed[i] {
			t.Errorf("block index %d opened but index %d closed", opened[i], closed[i])
		}
	}

	// Tool call must arrive as a tool_use block carrying name and id.
	if toolUse == nil {
		t.Fatal("no tool_use content block — a coding CLI would run no tool")
	}
	if toolUse["name"] != "shell" {
		t.Errorf("tool_use.name = %v, want shell", toolUse["name"])
	}
	if toolUse["id"] != "call_a1" {
		t.Errorf("tool_use.id = %v, want call_a1", toolUse["id"])
	}

	// message_delta must carry the stop_reason, then message_stop closes it.
	var sawMessageDelta, sawMessageStop bool
	for _, e := range events {
		switch e.Event {
		case "message_delta":
			sawMessageDelta = true
			d, _ := e.Data["delta"].(map[string]any)
			if d == nil || d["stop_reason"] != "tool_use" {
				t.Errorf("message_delta.delta = %#v, want stop_reason tool_use", e.Data["delta"])
			}
		case "message_stop":
			sawMessageStop = true
		}
	}
	if !sawMessageDelta {
		t.Error("no message_delta event — Claude Code never learns the stop_reason")
	}
	if !sawMessageStop {
		t.Error("no message_stop event")
	}
	if order[len(order)-1] != "message_stop" {
		t.Errorf("stream must end with message_stop, got %v", order)
	}
}

func TestAnthropicJSONKeepsToolUse(t *testing.T) {
	// Claude Code's retry after a failed stream is a NON-streaming request:
	// this path must also carry the tool call, or the retry silently returns
	// an empty assistant turn.
	upstream := `{"id":"chatcmpl-3","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_b2","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"ls\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`
	tr := New()
	out, err := tr.TranslateResponseJSON(domain.FormatOpenAI, domain.FormatAnthropic, []byte(upstream))
	if err != nil {
		t.Fatalf("TranslateResponseJSON: %v", err)
	}
	var resp struct {
		Type    string `json:"type"`
		Content []struct {
			Type  string         `json:"type"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Type != "message" {
		t.Errorf("type = %q, want message", resp.Type)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" {
		t.Fatalf("content = %#v, want a single tool_use block", resp.Content)
	}
	if resp.Content[0].Name != "shell" || resp.Content[0].ID != "call_b2" {
		t.Errorf("tool_use = %+v, want name shell / id call_b2", resp.Content[0])
	}
	if resp.Content[0].Input["cmd"] != "ls" {
		t.Errorf("tool_use.input = %#v, want cmd=ls", resp.Content[0].Input)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 4 {
		t.Errorf("usage = %+v, want 9/4", resp.Usage)
	}
}

func TestAnthropicStreamTextOnlyClosesItsBlock(t *testing.T) {
	upstream := `data: {"id":"chatcmpl-2","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}

data: {"id":"chatcmpl-2","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}

data: [DONE]

`
	events := collectAnthropicEvents(t, streamAnthropic(t, upstream))
	var text, stopReason string
	for _, e := range events {
		switch e.Event {
		case "content_block_delta":
			d, _ := e.Data["delta"].(map[string]any)
			if d != nil && d["type"] == "text_delta" {
				text += d["text"].(string)
			}
		case "message_delta":
			d, _ := e.Data["delta"].(map[string]any)
			if d != nil {
				stopReason, _ = d["stop_reason"].(string)
			}
		}
	}
	if text != "hi" {
		t.Errorf("text = %q, want %q", text, "hi")
	}
	if stopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", stopReason)
	}
}

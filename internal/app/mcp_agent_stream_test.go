package app

import (
	"bufio"
	"bytes"
	"encoding/json"

	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/sse"
)

// runTurn drives one upstream turn through the adapter, exactly as the loop
// does, and returns what the client would see.
func runTurn(t *testing.T, ad agentStreamAdapter, stream string) string {
	t.Helper()
	var out bytes.Buffer
	ad.StartTurn()
	readTurn(strings.NewReader(stream), ad, &out)
	return out.String()
}

func assertNotContains(t *testing.T, got, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("client must not see %q, got:\n%s", unwanted, got)
	}
}

func assertContains(t *testing.T, got, wanted string) {
	t.Helper()
	if !strings.Contains(got, wanted) {
		t.Fatalf("client should see %q, got:\n%s", wanted, got)
	}
}

// --- /v1/chat/completions -----------------------------------------------

func TestOpenAIStreamHidesGatewayToolCall(t *testing.T) {
	ad := newAgentStreamAdapter(domain.FormatOpenAI, map[string]bool{"github__create_issue": true})
	first := runTurn(t, ad, strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":""}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"content":"let me check"}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"github__create_issue","arguments":""}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"title\":\"x\"}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
		`data: [DONE]` + "\n\n",
	}, ""))
	assertContains(t, first, "let me check")
	assertNotContains(t, first, "github__create_issue")
	assertNotContains(t, first, `"finish_reason":"tool_calls"`)
	assertNotContains(t, first, "[DONE]")

	if !ad.SawMCPCall() || ad.ClientCall() {
		t.Fatalf("turn must be gateway-owned: mcp=%v client=%v", ad.SawMCPCall(), ad.ClientCall())
	}
	calls := ad.Calls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "github__create_issue" || calls[0].Args != `{"title":"x"}` {
		t.Fatalf("unexpected calls: %+v", calls)
	}

	// The assistant turn replayed upstream carries the tool call, and the
	// results answer it by id.
	next, err := ad.AppendTurn([]byte(`{"model":"coding","messages":[{"role":"user","content":"hi"}]}`), []agentToolResult{{CallID: "call_1", Text: "issue #42"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	assertContains(t, string(next), `"tool_call_id":"call_1"`)

	ad.NextTurn()
	second := runTurn(t, ad, strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":""}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"content":"issue #42 created"}}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
		`data: [DONE]` + "\n\n",
	}, ""))
	assertContains(t, second, "issue #42 created")
	assertContains(t, second, `"finish_reason":"stop"`)
	assertContains(t, second, "[DONE]")
	assertNotContains(t, second, `"role":"assistant"`)
}

func TestOpenAIStreamReleasesClientToolCall(t *testing.T) {
	ad := newAgentStreamAdapter(domain.FormatOpenAI, map[string]bool{"github__create_issue": true})
	// The gateway-owned call is withheld, then the turn reveals a tool the
	// client owns: everything must be released, in order, so the client can
	// finish the turn itself.
	got := runTurn(t, ad, strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"checking"}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"github__create_issue","arguments":"{}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","function":{"name":"shell","arguments":"{\"cmd\":\"ls\"}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
		`data: [DONE]` + "\n\n",
	}, ""))
	if !ad.ClientCall() {
		t.Fatalf("a client-owned tool must flag the turn")
	}
	assertContains(t, got, "github__create_issue")
	assertContains(t, got, "shell")
	assertContains(t, got, "[DONE]")
	if idx := strings.Index(got, "github__create_issue"); idx > strings.Index(got, "shell") {
		t.Fatalf("events must keep their order:\n%s", got)
	}
}

// --- /v1/messages -------------------------------------------------------

func TestAnthropicStreamHidesToolUseBlock(t *testing.T) {
	ad := newAgentStreamAdapter(domain.FormatAnthropic, map[string]bool{"github__create_issue": true})
	first := runTurn(t, ad, strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":9}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"checking\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"github__create_issue\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"title\\\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\":\\\"x\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":20}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, ""))
	assertContains(t, first, "message_start")
	assertContains(t, first, "checking")
	assertNotContains(t, first, "tool_use")
	assertNotContains(t, first, "message_stop")

	calls := ad.Calls()
	if len(calls) != 1 || calls[0].ID != "toolu_1" || calls[0].Name != "github__create_issue" || calls[0].Args != `{"title":"x"}` {
		t.Fatalf("unexpected calls: %+v", calls)
	}

	next, err := ad.AppendTurn([]byte(`{"model":"coding","messages":[{"role":"user","content":"hi"}]}`), []agentToolResult{{CallID: "toolu_1", Text: "issue #42"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	body := string(next)
	assertContains(t, body, `"type":"tool_use"`)
	assertContains(t, body, `"tool_use_id":"toolu_1"`)
	assertContains(t, body, `"text":"checking"`)

	// The continuation turn keeps the client's message open and moves the
	// block numbering past the blocks it has already seen.
	ad.NextTurn()
	second := runTurn(t, ad, strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_2\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, ""))
	assertNotContains(t, second, "message_start")
	assertContains(t, second, `"index":1`)
	assertContains(t, second, "message_stop")
	assertContains(t, second, "done")
}

// --- /v1/responses ------------------------------------------------------

func TestResponsesStreamFoldsContinuationIntoOneResponse(t *testing.T) {
	ad := newAgentStreamAdapter(domain.FormatResponses, map[string]bool{"github__create_issue": true})
	first := runTurn(t, ad, strings.Join([]string{
		"event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\"}}\n\n",
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\"}}\n\n",
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":2,\"output_index\":0,\"item_id\":\"msg_1\",\"delta\":\"checking\"}\n\n",
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"sequence_number\":3,\"output_index\":0,\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\"}}\n\n",
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"sequence_number\":4,\"output_index\":1,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"name\":\"github__create_issue\",\"call_id\":\"call_1\"}}\n\n",
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":5,\"output_index\":1,\"item_id\":\"fc_1\",\"delta\":\"{\\\"title\\\"\"}\n\n",
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"sequence_number\":6,\"output_index\":1,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"name\":\"github__create_issue\",\"call_id\":\"call_1\",\"arguments\":\"{\\\"title\\\":\\\"x\\\"}\"}}\n\n",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":7,\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n",
	}, ""))
	assertContains(t, first, "response.created")
	assertContains(t, first, "checking")
	assertNotContains(t, first, "github__create_issue")
	assertNotContains(t, first, "function_call")
	assertNotContains(t, first, "response.completed")

	calls := ad.Calls()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "github__create_issue" || calls[0].Args != `{"title":"x"}` {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	next, err := ad.AppendTurn([]byte(`{"model":"coding","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`), []agentToolResult{{CallID: "call_1", Text: "issue #42"}})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	assertContains(t, string(next), `"call_id":"call_1"`)

	ad.NextTurn()
	second := runTurn(t, ad, strings.Join([]string{
		"event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_2\"}}\n\n",
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"id\":\"msg_2\",\"type\":\"message\",\"role\":\"assistant\"}}\n\n",
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":2,\"output_index\":0,\"item_id\":\"msg_2\",\"delta\":\"created\"}\n\n",
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"sequence_number\":3,\"output_index\":0,\"item\":{\"id\":\"msg_2\",\"type\":\"message\",\"role\":\"assistant\"}}\n\n",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":4,\"response\":{\"id\":\"resp_2\",\"output\":[]}}\n\n",
	}, ""))
	// One response for the client: no second response.created, ids and
	// numbering carried over from the turn it already saw.
	assertNotContains(t, second, "response.created")
	assertNotContains(t, second, "resp_2")
	assertContains(t, second, `"id":"resp_1"`)
	assertContains(t, second, "created")
	assertContains(t, second, "response.completed")

	var completed struct {
		Response struct {
			Output []map[string]any `json:"output"`
		} `json:"response"`
	}
	for _, ev := range strings.Split(second, "\n\n") {
		if strings.Contains(ev, "response.completed") {
			payload := ev[strings.Index(ev, "data: ")+len("data: "):]
			payload = strings.TrimPrefix(payload, "event: response.completed\n")
			if err := json.Unmarshal([]byte(payload), &completed); err != nil {
				t.Fatalf("completed payload: %v (%s)", err, payload)
			}
		}
	}
	if len(completed.Response.Output) != 2 {
		t.Fatalf("completed output must list every item the client saw, got %d: %s", len(completed.Response.Output), second)
	}
}

// --- sse plumbing -------------------------------------------------------

func TestSSEReadEventPreservesFraming(t *testing.T) {
	stream := "event: response.created\ndata: {\"a\":1}\n\n: keepalive\n\ndata: [DONE]\n\n"
	r := bufio.NewReader(strings.NewReader(stream))
	ev, err := sse.ReadEvent(r)
	if err != nil || ev.Name != "response.created" || ev.Data != `{"a":1}` {
		t.Fatalf("unexpected event: %+v err=%v", ev, err)
	}
	if string(ev.Raw) != "event: response.created\ndata: {\"a\":1}\n\n" {
		t.Fatalf("raw framing changed: %q", ev.Raw)
	}
	ev, err = sse.ReadEvent(r)
	if err != nil || string(ev.Raw) != ": keepalive\n\n" {
		t.Fatalf("comment event: %+v err=%v", ev, err)
	}
	ev, err = sse.ReadEvent(r)
	if err != nil || ev.Data != "[DONE]" {
		t.Fatalf("done event: %+v err=%v", ev, err)
	}
}

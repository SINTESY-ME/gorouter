package translator

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// collectResponsesEvents parses an emitted Responses SSE stream into the
// events Codex actually consumes.
func collectResponsesEvents(t *testing.T, out string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, block := range strings.Split(out, "\n\n") {
		for _, ln := range strings.Split(block, "\n") {
			if !strings.HasPrefix(ln, "data: ") {
				continue
			}
			var e map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(ln, "data: ")), &e); err != nil {
				continue
			}
			events = append(events, e)
		}
	}
	return events
}

// TestResponsesParallelToolCallArgumentsComplete is the Codex-facing contract:
// Codex's SSE reader treats response.function_call_arguments.delta/done as
// UNHANDLED (codex-api/src/sse/responses.rs) and builds the executable tool
// call from the whole item in response.output_item.done. So the arguments in
// that final item must be complete. When a second tool call opens while the
// first still has argument deltas in flight, closing the first one early
// truncates its arguments and Codex stalls at the tool call.
func TestResponsesParallelToolCallArgumentsComplete(t *testing.T) {
	input := strings.Join([]string{
		// tool 0 opens with a partial argument string
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"shell","arguments":"{\"command\":"}}]},"finish_reason":null}]}`,
		"",
		// tool 1 opens before tool 0 finished streaming its arguments
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"shell","arguments":"{\"command\":"}}]},"finish_reason":null}]}`,
		"",
		// remaining arguments for tool 0
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls -la\"}"}}]},"finish_reason":null}]}`,
		"",
		// remaining arguments for tool 1
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"pwd\"}"}}]},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	pr, pw := io.Pipe()
	go func() {
		err := streamOpenAIToResponses(context.Background(), newBufReader(strings.NewReader(input)), pw)
		_ = pw.CloseWithError(err)
	}()
	raw, _ := io.ReadAll(pr)
	out := string(raw)

	want := map[string]string{
		"call_a": `{"command":"ls -la"}`,
		"call_b": `{"command":"pwd"}`,
	}
	done := map[string]string{}
	for _, e := range collectResponsesEvents(t, out) {
		if e["type"] != "response.output_item.done" {
			continue
		}
		item, _ := e["item"].(map[string]any)
		if item == nil || item["type"] != "function_call" {
			continue
		}
		callID, _ := item["call_id"].(string)
		args, _ := item["arguments"].(string)
		done[callID] = args
	}

	for callID, wantArgs := range want {
		got, ok := done[callID]
		if !ok {
			t.Errorf("no response.output_item.done for %s — Codex never receives the tool call", callID)
			continue
		}
		if got != wantArgs {
			t.Errorf("tool call %s closed with incomplete arguments: got %q, want %q (Codex executes this verbatim and stalls)", callID, got, wantArgs)
		}
	}
}

package translator

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// assembledToolArguments drains an OpenAI-format SSE stream and concatenates
// the tool_call arguments deltas, the way an OpenAI client assembles a call.
func assembledToolArguments(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	var sb strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Function struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, c := range chunk.Choices {
			for _, tc := range c.Delta.ToolCalls {
				sb.WriteString(tc.Function.Arguments)
			}
		}
	}
	return sb.String()
}

// The production incident: the Codex stream opens a reasoning item first, so
// the function_call sits at output_index 1. The index is only on the event
// envelope — the item payload has no output_index field — so reading it from
// the item registers the call under index 0 and every real delta (index 1)
// misses. The old ordinal fallback then emitted the FIRST delta and dropped
// the remaining thousands, delivering `{"` to the client.
func TestResponsesStreamToolCallAfterReasoningItemKeepsAllArguments(t *testing.T) {
	full := `{"section_0":"body zero","section_1":"body one","summary":"done"}`
	tr := New()
	rc, err := tr.TranslateResponseStream(context.Background(), domain.FormatResponses, domain.FormatOpenAI,
		responsesStream(
			`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5.6-luna"}}`,
			// reasoning item occupies output_index 0
			`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`,
			// function_call on output_index 1; the item itself carries NO output_index
			`{"type":"response.output_item.added","sequence_number":4,"output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"write_report","arguments":"","status":"in_progress"}}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":5,"output_index":1,"item_id":"fc_1","delta":"{\"section_0\":\"body zero\","}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":6,"output_index":1,"item_id":"fc_1","delta":"\"section_1\":\"body one\","}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":7,"output_index":1,"item_id":"fc_1","delta":"\"summary\":\"done\"}"}`,
			`{"type":"response.function_call_arguments.done","sequence_number":8,"output_index":1,"item_id":"fc_1","arguments":"`+strings.ReplaceAll(full, `"`, `\"`)+`"}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":810,"output_tokens":2900}}}`,
		))
	if err != nil {
		t.Fatalf("TranslateResponseStream: %v", err)
	}
	got := assembledToolArguments(t, rc)
	if got != full {
		t.Fatalf("assembled arguments = %q, want %q (deltas must not be dropped)", got, full)
	}
}

// Safety net: if the deltas never arrive, the closed item still carries the
// complete arguments and the client must receive them.
func TestResponsesStreamRepairsMissingArgumentsFromDoneEvent(t *testing.T) {
	full := `{"summary":"only the done event carried me"}`
	tr := New()
	rc, err := tr.TranslateResponseStream(context.Background(), domain.FormatResponses, domain.FormatOpenAI,
		responsesStream(
			`{"type":"response.created","response":{"id":"resp_2","model":"gpt-5.6-luna"}}`,
			`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"fc_2","type":"function_call","call_id":"call_2","name":"write_report"}}`,
			`{"type":"response.function_call_arguments.done","sequence_number":3,"output_index":0,"item_id":"fc_2","arguments":"`+strings.ReplaceAll(full, `"`, `\"`)+`"}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":20}}}`,
		))
	if err != nil {
		t.Fatalf("TranslateResponseStream: %v", err)
	}
	if got := assembledToolArguments(t, rc); got != full {
		t.Fatalf("assembled arguments = %q, want the complete %q", got, full)
	}
}

// Deltas and the closing item agree: nothing is emitted twice.
func TestResponsesStreamDoesNotDuplicateArgumentsWhenDeltasAreComplete(t *testing.T) {
	full := `{"a":1}`
	tr := New()
	rc, err := tr.TranslateResponseStream(context.Background(), domain.FormatResponses, domain.FormatOpenAI,
		responsesStream(
			`{"type":"response.created","response":{"id":"resp_3","model":"gpt-5.6-luna"}}`,
			`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"fc_3","type":"function_call","call_id":"call_3","name":"f"}}`,
			`{"type":"response.function_call_arguments.delta","sequence_number":3,"output_index":0,"item_id":"fc_3","delta":"{\"a\":1}"}`,
			`{"type":"response.function_call_arguments.done","sequence_number":4,"output_index":0,"item_id":"fc_3","arguments":"{\"a\":1}"}`,
			`{"type":"response.output_item.done","sequence_number":5,"output_index":0,"item":{"id":"fc_3","type":"function_call","call_id":"call_3","name":"f","arguments":"{\"a\":1}"}}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
		))
	if err != nil {
		t.Fatalf("TranslateResponseStream: %v", err)
	}
	if got := assembledToolArguments(t, rc); got != full {
		t.Fatalf("assembled arguments = %q, want exactly %q once", got, full)
	}
}

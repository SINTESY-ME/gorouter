package translator

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// responsesStream renders a Responses-format SSE stream.
func responsesStream(events ...string) io.ReadCloser {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return io.NopCloser(strings.NewReader(b.String()))
}

// lastFinishReason drains an OpenAI-format SSE stream and returns the
// finish_reason of the last chunk that carries one.
func lastFinishReason(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	finish := ""
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
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, c := range chunk.Choices {
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
	}
	return finish
}

// An upstream that says response.incomplete is reporting truncation, and that
// must reach the client even when a function call was already opened. The
// observable production symptom was the opposite: a tool call whose arguments
// were cut mid-JSON was reported as tool_calls, so the client treated an
// unfinished argument object as a completed turn.
func TestResponsesStreamIncompleteWithPartialToolCallReportsLength(t *testing.T) {
	tr := New()
	rc, err := tr.TranslateResponseStream(context.Background(), domain.FormatResponses, domain.FormatOpenAI,
		responsesStream(
			`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5-codex"}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","output_index":0,"call_id":"call_1","name":"write_report"}}`,
			`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\""}`,
			`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":120,"output_tokens":6729}}}`,
		))
	if err != nil {
		t.Fatalf("TranslateResponseStream: %v", err)
	}
	if got := lastFinishReason(t, rc); got != "length" {
		t.Fatalf("finish_reason = %q, want \"length\" (truncated tool call must not look complete)", got)
	}
}

// The guard against over-reaching: a response that genuinely completed with a
// tool call still reports tool_calls.
func TestResponsesStreamCompletedToolCallStillReportsToolCalls(t *testing.T) {
	tr := New()
	rc, err := tr.TranslateResponseStream(context.Background(), domain.FormatResponses, domain.FormatOpenAI,
		responsesStream(
			`{"type":"response.created","response":{"id":"resp_2","model":"gpt-5-codex"}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","output_index":0,"call_id":"call_2","name":"write_report"}}`,
			`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"summary\":\"ok\"}"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":12}}}`,
		))
	if err != nil {
		t.Fatalf("TranslateResponseStream: %v", err)
	}
	if got := lastFinishReason(t, rc); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want \"tool_calls\"", got)
	}
}

// A pure text answer that completes keeps reporting tool_calls only when a tool
// call existed; with none, the terminal chunk carries no finish reason of its
// own (the client reads end-of-stream as a normal stop).
func TestResponsesStreamCompletedTextHasNoToolCalls(t *testing.T) {
	tr := New()
	rc, err := tr.TranslateResponseStream(context.Background(), domain.FormatResponses, domain.FormatOpenAI,
		responsesStream(
			`{"type":"response.created","response":{"id":"resp_3","model":"gpt-5-codex"}}`,
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":2}}}`,
		))
	if err != nil {
		t.Fatalf("TranslateResponseStream: %v", err)
	}
	if got := lastFinishReason(t, rc); got == "tool_calls" {
		t.Fatalf("text-only completion must not report tool_calls, got %q", got)
	}
}

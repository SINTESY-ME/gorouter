package app

import (
	"io"
	"strings"
	"testing"
)

// The blank-completion guard looked for `choices` in the response body after it
// had already been translated into the client's format. Anthropic and
// Responses bodies have no `choices`, so every combo request in those formats
// was read as an empty completion: the router burned through the whole cascade
// and answered 500 "upstream returned a completion with no choices". The guard
// now runs on the OpenAI-format body and travels with the response.

func TestInspectBlankCompletionReadsOpenAIBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "usable turn is not blank",
			body: `{"choices":[{"message":{"content":"42 unidades"},"finish_reason":"stop"}]}`,
			want: "",
		},
		{
			name: "tool call with empty content is not blank",
			body: `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1"}]},"finish_reason":"tool_calls"}]}`,
			want: "",
		},
		{
			name: "exhausted budget with empty content is blank",
			body: `{"choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
			want: "upstream exhausted tokens (length) with empty content",
		},
		{
			name: "no choices at all is blank",
			body: `{"choices":[]}`,
			want: "upstream returned a completion with no choices",
		},
		{
			name: "an Anthropic body is never inspected here",
			body: `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`,
			want: "", // no `choices` key at all — not parseable as a completion
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, blank := inspectBlankCompletion(io.NopCloser(strings.NewReader(tc.body)))
			if blank.Reason != tc.want {
				t.Fatalf("reason = %q, want %q", blank.Reason, tc.want)
			}
			// The body must survive inspection untouched: the client and the
			// loop both read it afterwards.
			got, err := io.ReadAll(body)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if string(got) != tc.body {
				t.Fatalf("body was altered:\n got %s\nwant %s", got, tc.body)
			}
		})
	}
}

func TestInspectBlankCompletionKeepsTokens(t *testing.T) {
	body := `{"choices":[{"message":{"content":""},"finish_reason":"max_tokens"}],"usage":{"prompt_tokens":123,"completion_tokens":456}}`
	_, blank := inspectBlankCompletion(io.NopCloser(strings.NewReader(body)))
	if blank.PromptTokens != 123 || blank.CompletionTokens != 456 {
		t.Fatalf("tokens not accounted: prompt=%d completion=%d", blank.PromptTokens, blank.CompletionTokens)
	}
}

// A response exactly at the inspection cap is handed back whole, so a large
// completion is never truncated for the client.
func TestInspectBlankCompletionPassesThroughOversizedBody(t *testing.T) {
	body := `{"choices":[{"message":{"content":"` + strings.Repeat("x", 512<<10) + `"},"finish_reason":"stop"}]}`
	rc, blank := inspectBlankCompletion(io.NopCloser(strings.NewReader(body)))
	if blank.Reason != "" {
		t.Fatalf("oversized body must not be judged: %q", blank.Reason)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != len(body) {
		t.Fatalf("oversized body was truncated: got %d bytes, want %d", len(got), len(body))
	}
}

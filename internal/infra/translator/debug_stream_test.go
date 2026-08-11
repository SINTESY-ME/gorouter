package translator

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
)

func TestOpenAIStreamToResponsesEmitsCompleted(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	pr, pw := io.Pipe()
	go func() {
		err := streamOpenAIToResponses(context.Background(), newBufReader(strings.NewReader(input)), pw)
		_ = pw.CloseWithError(err)
	}()

	output, _ := io.ReadAll(pr)
	got := string(output)
	t.Logf("stream output:\n%s", got)

	if !strings.Contains(got, "response.completed") {
		t.Errorf("expected response.completed event, got:\n%s", got)
	}
}

func TestOpenAIStreamToResponsesEmitsCompletedNoID(t *testing.T) {
	input := strings.Join([]string{
		`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		"",
		`data: {"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	pr, pw := io.Pipe()
	go func() {
		err := streamOpenAIToResponses(context.Background(), newBufReader(strings.NewReader(input)), pw)
		_ = pw.CloseWithError(err)
	}()

	output, _ := io.ReadAll(pr)
	got := string(output)
	t.Logf("stream output (no id):\n%s", got)

	if !strings.Contains(got, "response.completed") {
		t.Errorf("expected response.completed event even without chunk id, got:\n%s", got)
	}
}

func TestOpenAIStreamToResponsesEmitsCompletedEmptyStream(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		err := streamOpenAIToResponses(context.Background(), newBufReader(strings.NewReader("")), pw)
		_ = pw.CloseWithError(err)
	}()

	output, _ := io.ReadAll(pr)
	got := string(output)
	t.Logf("stream output (empty):\n%s", got)

	if !strings.Contains(got, "response.completed") {
		t.Errorf("expected response.completed event even for an empty stream, got:\n%s", got)
	}
}

func newBufReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}
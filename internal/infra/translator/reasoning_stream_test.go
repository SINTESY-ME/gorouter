package translator

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestAnthropicReasoningStreamIsExposed(t *testing.T) {
	sseBody := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-sonnet-4\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	out, err := anthropicStreamToOpenAI(context.Background(), io.NopCloser(strings.NewReader(sseBody)))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(data)
	if !strings.Contains(wire, `"reasoning_content":"plan"`) {
		t.Fatalf("stream did not expose reasoning_content: %s", wire)
	}
	if !strings.Contains(wire, `"content":"answer"`) {
		t.Fatalf("stream did not expose answer: %s", wire)
	}
}

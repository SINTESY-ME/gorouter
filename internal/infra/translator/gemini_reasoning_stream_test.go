package translator

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestGeminiThoughtStreamIsExposed(t *testing.T) {
	sseBody := "data: {\"responseId\":\"resp_1\",\"modelVersion\":\"gemini-2.5-pro\",\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"text\":\"plan\"}]}}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}]}\n\n"
	out, err := geminiStreamToOpenAI(context.Background(), io.NopCloser(strings.NewReader(sseBody)))
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

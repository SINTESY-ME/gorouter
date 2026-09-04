package translator

import (
	"encoding/json"
	"testing"
)

func TestGeminiThoughtIsExposed(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"thought":true,"text":"plan"},{"text":"answer"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`
	out, err := translateGeminiToOpenAIResponseJSON([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatal(err)
	}
	message := wire["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "answer" || message["reasoning_content"] != "plan" {
		t.Fatalf("message = %#v", message)
	}
}

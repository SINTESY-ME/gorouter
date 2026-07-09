package translator

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestOpenAIToAnthropicRequest(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"system","content":"be nice"},{"role":"user","content":"hello"}],"max_tokens":100,"temperature":0.5,"stream":true}`
	out, err := translateOpenAIToAnthropicRequest("claude-3", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var r anthropicRequest
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	if r.Model != "claude-3" {
		t.Errorf("model: got %q want claude-3", r.Model)
	}
	if r.MaxTokens != 100 {
		t.Errorf("max_tokens: got %d want 100", r.MaxTokens)
	}
	if r.Stream != true {
		t.Error("stream should be true")
	}
	if r.System == nil {
		t.Error("system should be set")
	}
	if len(r.Messages) != 1 {
		t.Fatalf("messages: got %d want 1", len(r.Messages))
	}
	if r.Messages[0].Role != "user" {
		t.Errorf("role: got %q want user", r.Messages[0].Role)
	}
}

func TestAnthropicToOpenAIResponseJSON(t *testing.T) {
	body := `{"id":"msg_1","model":"claude-3","role":"assistant","content":[{"type":"text","text":"hello world"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`
	out, err := translateAnthropicToOpenAIResponseJSONImpl([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	choices := r["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "hello world" {
		t.Errorf("content: got %v want 'hello world'", msg["content"])
	}
	usage := r["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 10 {
		t.Errorf("prompt_tokens: got %v want 10", usage["prompt_tokens"])
	}
}

func TestOpenAIToGeminiRequest(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"system","content":"be nice"},{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"how are you"}],"max_tokens":200,"temperature":0.7}`
	out, err := translateOpenAIToGeminiRequest("gemini-pro", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	contents := r["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents: got %d want 3", len(contents))
	}
	first := contents[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("role 0: got %v want user", first["role"])
	}
	second := contents[1].(map[string]any)
	if second["role"] != "model" {
		t.Errorf("role 1: got %v want model", second["role"])
	}
	sysInst := r["systemInstruction"].(map[string]any)
	parts := sysInst["parts"].([]any)
	part := parts[0].(map[string]any)
	if part["text"] != "be nice" {
		t.Errorf("system text: got %v want 'be nice'", part["text"])
	}
	genCfg := r["generationConfig"].(map[string]any)
	if genCfg["maxOutputTokens"].(float64) != 200 {
		t.Errorf("maxOutputTokens: got %v want 200", genCfg["maxOutputTokens"])
	}
}

func TestGeminiToOpenAIResponseJSON(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"text":"hello from gemini"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":8,"totalTokenCount":13}}`
	out, err := translateGeminiToOpenAIResponseJSON([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	choices := r["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "hello from gemini" {
		t.Errorf("content: got %v want 'hello from gemini'", msg["content"])
	}
	usage := r["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 5 {
		t.Errorf("prompt_tokens: got %v want 5", usage["prompt_tokens"])
	}
	if usage["completion_tokens"].(float64) != 8 {
		t.Errorf("completion_tokens: got %v want 8", usage["completion_tokens"])
	}
}

func TestOpenAIToResponsesRequest(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"system","content":"be nice"},{"role":"user","content":"hello"}],"max_tokens":50}`
	out, err := translateOpenAIToResponsesRequest("gpt-4o", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	if r["model"] != "gpt-4o" {
		t.Errorf("model: got %v want gpt-4o", r["model"])
	}
	if r["instructions"] != "be nice" {
		t.Errorf("instructions: got %v want 'be nice'", r["instructions"])
	}
	input := r["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input: got %d want 1", len(input))
	}
	item := input[0].(map[string]any)
	if item["role"] != "user" {
		t.Errorf("role: got %v want user", item["role"])
	}
	if r["max_output_tokens"].(float64) != 50 {
		t.Errorf("max_output_tokens: got %v want 50", r["max_output_tokens"])
	}
}

func TestResponsesToOpenAIResponseJSON(t *testing.T) {
	body := `{"id":"resp_1","model":"gpt-4o","output":[{"type":"message","content":[{"type":"output_text","text":"hello from responses"}]}],"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`
	out, err := translateResponsesToOpenAIResponseJSON([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	choices := r["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "hello from responses" {
		t.Errorf("content: got %v want 'hello from responses'", msg["content"])
	}
	usage := r["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 3 {
		t.Errorf("prompt_tokens: got %v want 3", usage["prompt_tokens"])
	}
}

func TestGeminiFinishMapping(t *testing.T) {
	cases := map[string]string{
		"STOP":                     "stop",
		"MAX_TOKENS":               "length",
		"SAFETY":                   "content_filter",
		"FINISH_REASON_UNSPECIFIED": "stop",
	}
	for in, want := range cases {
		if got := geminiFinishToOpenAI(in); got != want {
			t.Errorf("geminiFinishToOpenAI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistrySupports(t *testing.T) {
	tr := New()
	pairs := [][2]domain.Format{
		{domain.FormatOpenAI, domain.FormatAnthropic},
		{domain.FormatAnthropic, domain.FormatOpenAI},
		{domain.FormatOpenAI, domain.FormatGemini},
		{domain.FormatGemini, domain.FormatOpenAI},
		{domain.FormatOpenAI, domain.FormatResponses},
		{domain.FormatResponses, domain.FormatOpenAI},
		{domain.FormatOpenAI, domain.FormatOpenAI},
	}
	for _, p := range pairs {
		if !tr.Supports(p[0], p[1]) {
			t.Errorf("translator should support %s->%s", p[0], p[1])
		}
	}
}

func TestGeminiStreamToOpenAI(t *testing.T) {
	sseBody := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":5}}\n\n"
	rc := io.NopCloser(strings.NewReader(sseBody))
	out, err := geminiStreamToOpenAI(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	buf, _ := io.ReadAll(out)
	s := string(buf)
	if !strings.Contains(s, "hello") {
		t.Errorf("output should contain 'hello': %s", s)
	}
	if !strings.Contains(s, "prompt_tokens") {
		t.Errorf("output should contain usage: %s", s)
	}
	if !strings.Contains(s, "[DONE]") {
		t.Error("output should contain [DONE]")
	}
}

func TestAnthropicStreamToOpenAI(t *testing.T) {
	sseBody := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude-3\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rc := io.NopCloser(strings.NewReader(sseBody))
	out, err := anthropicStreamToOpenAI(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	buf, _ := io.ReadAll(out)
	s := string(buf)
	if !strings.Contains(s, "hi") {
		t.Errorf("output should contain 'hi': %s", s)
	}
	if !strings.Contains(s, "prompt_tokens") {
		t.Errorf("output should contain usage: %s", s)
	}
}

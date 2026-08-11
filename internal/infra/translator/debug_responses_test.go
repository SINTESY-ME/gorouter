package translator

import (
	"encoding/json"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestDebugResponsesToOpenAI(t *testing.T) {
	tr := New()
	cases := []string{
		`{"model":"coding","input":[{"role":"user","content":"say hi"}],"stream":true}`,
		`{"model":"coding","input":[{"role":"user","content":[{"type":"input_text","text":"say hi"}]}],"stream":true}`,
	}
	for _, body := range cases {
		out, err := tr.TranslateRequest(domain.FormatResponses, domain.FormatOpenAI, "glm-5.2", []byte(body))
		t.Logf("in=%s\nerr=%v\nout=%s\n", body, err, string(out))
	}
}

func TestResponsesReasoningItemBecomesReasoningContent(t *testing.T) {
	tr := New()
	body := `{"model":"coding","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]},
		{"type":"reasoning","content":[{"type":"summary_text","text":"I will use exec to list files"}]},
		{"type":"function_call","call_id":"call_0","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_0","output":"file1\nfile2"}
	],"stream":true,"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}]}`
	out, err := tr.TranslateRequest(domain.FormatResponses, domain.FormatOpenAI, "deepseek-v4-flash", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var req openaiRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	// Expect: user msg, assistant msg with reasoning_content, assistant msg with tool_calls, tool msg
	if len(req.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[1].ReasoningContent == "" {
		t.Errorf("expected reasoning_content on message[1], got: %+v", req.Messages[1])
	}
	if req.Messages[1].Role != "assistant" {
		t.Errorf("expected role assistant for reasoning, got %q", req.Messages[1].Role)
	}
	if len(req.Messages[2].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call on message[2], got %d", len(req.Messages[2].ToolCalls))
	}
	t.Logf("translated: %s", string(out))
}

func TestResponsesReasoningItemStringContent(t *testing.T) {
	tr := New()
	body := `{"model":"coding","input":[
		{"type":"message","role":"user","content":"hi"},
		{"type":"reasoning","content":"thinking about it"},
		{"type":"message","role":"user","content":"again"}
	],"stream":false}`
	out, err := tr.TranslateRequest(domain.FormatResponses, domain.FormatOpenAI, "deepseek-v4-flash", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var req openaiRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}
	if req.Messages[1].ReasoningContent != "thinking about it" {
		t.Errorf("expected reasoning_content 'thinking about it', got %q", req.Messages[1].ReasoningContent)
	}
}

func TestResponsesReasoningItemEmptySkipped(t *testing.T) {
	tr := New()
	body := `{"model":"coding","input":[
		{"type":"message","role":"user","content":"hi"},
		{"type":"reasoning","content":""},
		{"type":"message","role":"user","content":"again"}
	],"stream":false}`
	out, err := tr.TranslateRequest(domain.FormatResponses, domain.FormatOpenAI, "deepseek-v4-flash", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var req openaiRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	// Empty reasoning should be skipped — only 2 messages.
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages (empty reasoning skipped), got %d: %+v", len(req.Messages), req.Messages)
	}
}

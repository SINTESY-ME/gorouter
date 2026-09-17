package translator

import (
	"bufio"
	"context"
	"encoding/json"
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

func TestOpenAIStreamToResponsesUsageAfterFinishReason(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"chatcmpl-x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl-x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl-x","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":500,"completion_tokens":50,"total_tokens":550}}`,
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
	t.Logf("stream output (usage after finish):\n%s", got)

	if !strings.Contains(got, "response.completed") {
		t.Errorf("expected response.completed event, got:\n%s", got)
	}
	if !strings.Contains(got, `"input_tokens":500`) {
		t.Errorf("expected input_tokens=500 in response.completed, got:\n%s", got)
	}
	if !strings.Contains(got, `"output_tokens":50`) {
		t.Errorf("expected output_tokens=50 in response.completed, got:\n%s", got)
	}
}

// TestResponsesStreamToOpenAIWithReasoning verifies a Responses stream's
// reasoning_summary deltas are surfaced as OpenAI reasoning_content chunks
// (the muse-spark thinking-flow regression).
func TestResponsesStreamToOpenAIWithReasoning(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"muse-spark-1.2","status":"in_progress"}}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"Let me think about this"}`,
		"",
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"delta":"Answer."}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}`,
		"",
	}, "\n")

	var sb strings.Builder
	err := streamResponsesToOpenAI(context.Background(), newBufReader(strings.NewReader(input)), &sb)
	if err != nil {
		t.Fatalf("streamResponsesToOpenAI: %v", err)
	}
	got := sb.String()
	t.Logf("stream output:\n%s", got)

	if !strings.Contains(got, `"reasoning_content":"Let me think about this"`) {
		t.Errorf("expected reasoning_content delta, got:\n%s", got)
	}
	if !strings.Contains(got, `"content":"Answer."`) {
		t.Errorf("expected answer content, got:\n%s", got)
	}
}

// TestResponsesJSONToOpenAIWithReasoning covers the non-streaming path for the
// same muse-spark reasoning regression.
func TestResponsesJSONToOpenAIWithReasoning(t *testing.T) {
	body := `{"id":"resp_1","object":"response","model":"muse-spark-1.2","status":"completed","output":[
		{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"Thinking step one"},{"type":"summary_text","text":"step two"}]},
		{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Final answer"}]}
	],"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}`
	out, err := translateResponsesToOpenAIResponseJSON([]byte(body))
	if err != nil {
		t.Fatalf("translateResponsesToOpenAIResponseJSON: %v", err)
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Choices[0].Message.ReasoningContent != "Thinking step one step two" {
		t.Fatalf("expected reasoning_content, got %q in %s", resp.Choices[0].Message.ReasoningContent, string(out))
	}
	if resp.Choices[0].Message.Content != "Final answer" {
		t.Fatalf("expected content, got %q", resp.Choices[0].Message.Content)
	}
}

func newBufReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}

// TestResponsesStreamToOpenAIWithToolCall guards the muse-spark regression:
// a Responses stream carrying function_call items must be translated into
// OpenAI chunks with tool_calls, otherwise the client sees the reply "stop"
// exactly where the model asked for a tool.
func TestResponsesStreamToOpenAIWithToolCall(t *testing.T) {
	input := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"resp_1","model":"muse-spark-1.2","status":"in_progress"}}`,
		"",
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_42","name":"list_files","arguments":""}}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"path\":\""}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"/home\"}"}`,
		"",
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_42","name":"list_files","arguments":"{\"path\":\"/home\"}"}],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}`,
		"",
	}, "\n")

	var sb strings.Builder
	err := streamResponsesToOpenAI(context.Background(), newBufReader(strings.NewReader(input)), &sb)
	if err != nil {
		t.Fatalf("streamResponsesToOpenAI: %v", err)
	}
	got := sb.String()
	t.Logf("stream output:\n%s", got)

	if !strings.Contains(got, `"tool_calls"`) {
		t.Errorf("expected tool_calls in stream, got:\n%s", got)
	}
	if !strings.Contains(got, `"name":"list_files"`) {
		t.Errorf("expected tool name in stream, got:\n%s", got)
	}
	if !strings.Contains(got, `"arguments":"{\"path\":\"`) {
		t.Errorf("expected tool arguments fragments in stream, got:\n%s", got)
	}
	if !strings.Contains(got, `"finish_reason":"tool_calls"`) {
		t.Errorf("expected finish_reason tool_calls, got:\n%s", got)
	}
}

// TestResponsesJSONToOpenAIWithToolCall covers the non-streaming path for the
// same muse-spark regression.
func TestResponsesJSONToOpenAIWithToolCall(t *testing.T) {
	body := `{"id":"resp_1","object":"response","model":"muse-spark-1.2","status":"completed","output":[
		{"id":"fc_1","type":"function_call","call_id":"call_42","name":"list_files","arguments":"{\"path\":\"/home\"}"}
	],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}`
	out, err := translateResponsesToOpenAIResponseJSON([]byte(body))
	if err != nil {
		t.Fatalf("translateResponsesToOpenAIResponseJSON: %v", err)
	}
	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got: %s", string(out))
	}
	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.ID != "call_42" || tc.Function.Name != "list_files" || tc.Function.Arguments != `{"path":"/home"}` {
		t.Fatalf("unexpected tool call: %+v", tc)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected finish_reason tool_calls, got %q", resp.Choices[0].FinishReason)
	}
}
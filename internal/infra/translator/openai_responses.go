package translator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
)

func init() {
	register(domain.FormatOpenAI, domain.FormatResponses, pair{
		translateRequest:        translateOpenAIToResponsesRequest,
		translateResponseJSON:   translateOpenAIToResponsesResponseJSON,
		translateResponseStream: openAIStreamToResponses,
	})
	register(domain.FormatResponses, domain.FormatOpenAI, pair{
		translateRequest:        translateResponsesToOpenAIRequest,
		translateResponseJSON:   translateResponsesToOpenAIResponseJSON,
		translateResponseStream: responsesStreamToOpenAI,
	})
}

func translateOpenAIToResponsesRequest(upstreamModel string, body []byte) ([]byte, error) {
	var r openaiRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("openai->responses: parse: %w", err)
	}
	out := map[string]any{
		"model":  upstreamModel,
		"stream": r.Stream,
	}
	var input []map[string]any
	for _, m := range r.Messages {
		if m.Role == "system" {
			out["instructions"] = asStringContent(m.Content)
			continue
		}
		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		input = append(input, map[string]any{
			"role":    role,
			"content": asStringContent(m.Content),
		})
	}
	out["input"] = input
	if r.MaxTokens != nil {
		out["max_output_tokens"] = *r.MaxTokens
	} else {
		out["max_output_tokens"] = 4096
	}
	if r.Temperature != nil {
		out["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		out["top_p"] = *r.TopP
	}
	return json.Marshal(out)
}

func translateResponsesToOpenAIResponseJSON(body []byte) ([]byte, error) {
	var in struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("responses->openai response: parse: %w", err)
	}
	var text string
	for _, item := range in.Output {
		if item.Type == "message" {
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text += c.Text
				}
			}
		}
	}
	out := map[string]any{
		"id":     in.ID,
		"object": "chat.completion",
		"model":  in.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     in.Usage.InputTokens,
			"completion_tokens": in.Usage.OutputTokens,
			"total_tokens":      in.Usage.TotalTokens,
		},
	}
	return json.Marshal(out)
}

func responsesStreamToOpenAI(ctx context.Context, r io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	pr, pw := io.Pipe()
	go func() {
		defer r.Close()
		err := streamResponsesToOpenAI(ctx, br, pw)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

func streamResponsesToOpenAI(ctx context.Context, br *bufio.Reader, w io.Writer) error {
	first := true
	id := ""
	model := ""
	var promptTokens, completionTokens int
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, done, err := readEvent(&sseReader{r: br})
		if err != nil {
			return err
		}
		if done {
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return nil
		}
		if data == "" {
			continue
		}
		var ev struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
			Delta    string          `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.created":
			var resp struct {
				ID    string `json:"id"`
				Model string `json:"model"`
			}
			_ = json.Unmarshal(ev.Response, &resp)
			id = resp.ID
			model = resp.Model
		case "response.output_text.delta":
			chunk := openAIStreamChunk(id, model, ev.Delta, first, nil)
			first = false
			if _, err := w.Write([]byte("data: " + chunk + "\n\n")); err != nil {
				return err
			}
		case "response.completed":
			var resp struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			_ = json.Unmarshal(ev.Response, &resp)
			promptTokens = resp.Usage.InputTokens
			completionTokens = resp.Usage.OutputTokens
			usage := map[string]any{
				"prompt_tokens":     promptTokens,
				"completion_tokens": completionTokens,
				"total_tokens":      promptTokens + completionTokens,
			}
			chunk := openAIStreamChunk(id, model, "", first, usage)
			if _, err := w.Write([]byte("data: " + chunk + "\n\n")); err != nil {
				return err
			}
		}
	}
}

func translateResponsesToOpenAIRequest(upstreamModel string, body []byte) ([]byte, error) {
	var in struct {
		Model           string          `json:"model"`
		Input           json.RawMessage `json:"input"`
		Instructions    string          `json:"instructions"`
		MaxOutputTokens *int            `json:"max_output_tokens"`
		Temperature     *float64        `json:"temperature"`
		TopP            *float64        `json:"top_p"`
		Stream          bool            `json:"stream"`
		Tools           json.RawMessage `json:"tools"`
		ToolChoice      json.RawMessage `json:"tool_choice"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("responses->openai: parse: %w", err)
	}
	out := openaiRequest{
		Model:       upstreamModel,
		Stream:      in.Stream,
		MaxTokens:   in.MaxOutputTokens,
		Temperature: in.Temperature,
		TopP:        in.TopP,
		Tools:       translateResponsesTools(in.Tools),
		ToolChoice:  in.ToolChoice,
	}
	if in.Instructions != "" {
		b, _ := json.Marshal(in.Instructions)
		out.Messages = append(out.Messages, openaiMessage{Role: "system", Content: b})
	}
	messages, err := parseResponsesInput(in.Input)
	if err != nil {
		return nil, err
	}
	out.Messages = append(out.Messages, messages...)
	return json.Marshal(out)
}

// translateResponsesTools converts Responses API tools (array of {type:"function",name,parameters})
// to OpenAI Chat Completions tools (array of {type:"function",function:{name,parameters}}).
// Returns nil if input is empty (omitted from JSON).
func translateResponsesTools(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var tools []struct {
		Type       string          `json:"type"`
		Name       string          `json:"name"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return raw // passthrough on parse failure
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":       t.Name,
				"parameters": json.RawMessage(t.Parameters),
			},
		})
	}
	b, _ := json.Marshal(out)
	return b
}

func parseResponsesInput(raw json.RawMessage) ([]openaiMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		b, _ := json.Marshal(s)
		return []openaiMessage{{Role: "user", Content: b}}, nil
	}
	var arr []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		// function_call fields
		CallID   string `json:"call_id"`
		Name     string `json:"name"`
		Arguments string `json:"arguments"`
		// function_call_output fields
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("responses->openai: parse input: %w", err)
	}
	var out []openaiMessage
	for _, m := range arr {
		switch m.Type {
		case "function_call":
			out = append(out, openaiMessage{
				Role: "assistant",
				ToolCalls: []openaiToolCall{{
					ID:   m.CallID,
					Type: "function",
					Function: openaiFunction{
						Name:      m.Name,
						Arguments: m.Arguments,
					},
				}},
			})
		case "function_call_output":
			out = append(out, openaiMessage{
				Role:      "tool",
				Content:   m.Output,
				ToolCallID: m.CallID,
			})
		default:
			role := m.Role
			if role != "user" && role != "assistant" && role != "system" {
				role = "user"
			}
			content := asStringContent(m.Content)
			if content == "" {
				var s string
				if json.Unmarshal(m.Content, &s) == nil {
					content = s
				}
			}
			b, _ := json.Marshal(content)
			out = append(out, openaiMessage{Role: role, Content: b})
		}
	}
	return out, nil
}

func translateOpenAIToResponsesResponseJSON(body []byte) ([]byte, error) {
	var in struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("openai->responses response: parse: %w", err)
	}
	text := ""
	if len(in.Choices) > 0 {
		text = in.Choices[0].Message.Content
	}
	out := map[string]any{
		"id":     in.ID,
		"object": "response",
		"model":  in.Model,
		"output": []map[string]any{{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": text,
			}},
		}},
		"usage": map[string]any{
			"input_tokens":  in.Usage.PromptTokens,
			"output_tokens": in.Usage.CompletionTokens,
			"total_tokens":  in.Usage.PromptTokens + in.Usage.CompletionTokens,
		},
	}
	return json.Marshal(out)
}

func openAIStreamToResponses(ctx context.Context, r io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	pr, pw := io.Pipe()
	go func() {
		defer r.Close()
		err := streamOpenAIToResponses(ctx, br, pw)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

func streamOpenAIToResponses(ctx context.Context, br *bufio.Reader, w io.Writer) error {
	st := &responsesStreamState{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, done, err := readEvent(&sseReader{r: br})
		if err != nil {
			if err == io.EOF {
				return st.finish(w)
			}
			return err
		}
		if done {
			return st.finish(w)
		}
		if data == "" {
			continue
		}
		if err := st.handleChunk(data, w); err != nil {
			return err
		}
	}
}

// responsesStreamState tracks the lifecycle of a single response so we emit
// output_item/content_part open/close events in the right order.
type responsesStreamState struct {
	id       string
	model    string
	created  bool
	outputIdx int
	reasoning struct {
		open   bool
		id     string
		buf    strings.Builder
	}
	message struct {
		open   bool
		id     string
		buf    strings.Builder
	}
	toolCalls map[int]*toolCallState
}

type toolCallState struct {
	id        string
	callID    string
	name      string
	open      bool
	arguments strings.Builder
}

func (s *responsesStreamState) handleChunk(data string, w io.Writer) error {
	var ev struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Delta        struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil
	}
	if !s.created && ev.ID != "" {
		s.id = "resp_" + ev.ID
		s.model = ev.Model
		s.created = true
		if err := writeSSE(w, "response.created", map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":         s.id,
				"object":     "response",
				"status":     "in_progress",
				"output":     []any{},
			},
		}); err != nil {
			return err
		}
		if err := writeSSE(w, "response.in_progress", map[string]any{
			"type": "response.in_progress",
			"response": map[string]any{
				"id":     s.id,
				"object": "response",
				"status": "in_progress",
			},
		}); err != nil {
			return err
		}
	}
	if len(ev.Choices) == 0 {
		return nil
	}
	choice := &ev.Choices[0]
	if choice.FinishReason != "" {
		return s.closeAll(w)
	}
	d := &choice.Delta
	if d.Reasoning != "" {
		if err := s.handleReasoning(d.Reasoning, w); err != nil {
			return err
		}
	}
	if d.Content != "" {
		if err := s.handleContent(d.Content, w); err != nil {
			return err
		}
	}
	if len(d.ToolCalls) > 0 {
		if err := s.handleToolCalls(d.ToolCalls, w); err != nil {
			return err
		}
	}
	return nil
}

func (s *responsesStreamState) handleReasoning(text string, w io.Writer) error {
	if !s.reasoning.open {
		s.reasoning.open = true
		s.reasoning.id = "rs_" + s.id
		if err := writeSSE(w, "response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": s.outputIdx,
			"item": map[string]any{
				"id":      s.reasoning.id,
				"type":    "reasoning",
				"summary": []any{},
			},
		}); err != nil {
			return err
		}
	}
	s.reasoning.buf.WriteString(text)
	return writeSSE(w, "response.reasoning_summary_text.delta", map[string]any{
		"type":          "response.reasoning_summary_text.delta",
		"item_id":       s.reasoning.id,
		"output_index":  s.outputIdx,
		"summary_index": 0,
		"delta":         text,
	})
}

func (s *responsesStreamState) handleContent(text string, w io.Writer) error {
	if !s.message.open {
		s.closeReasoning(w)
		s.message.open = true
		s.message.id = "msg_" + s.id
		if err := writeSSE(w, "response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": s.outputIdx,
			"item": map[string]any{
				"id":      s.message.id,
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
			},
		}); err != nil {
			return err
		}
		if err := writeSSE(w, "response.content_part.added", map[string]any{
			"type":          "response.content_part.added",
			"item_id":       s.message.id,
			"output_index":  s.outputIdx,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		}); err != nil {
			return err
		}
	}
	s.message.buf.WriteString(text)
	return writeSSE(w, "response.output_text.delta", map[string]any{
		"type":          "response.output_text.delta",
		"item_id":       s.message.id,
		"output_index":  s.outputIdx,
		"content_index": 0,
		"delta":         text,
	})
}

func (s *responsesStreamState) closeReasoning(w io.Writer) {
	if !s.reasoning.open {
		return
	}
	s.reasoning.open = false
	text := s.reasoning.buf.String()
	writeSSE(w, "response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": s.outputIdx,
		"item": map[string]any{
			"id":   s.reasoning.id,
			"type": "reasoning",
			"summary": []map[string]any{{
				"type": "summary_text",
				"text": text,
			}},
		},
	})
	s.outputIdx++
}

func (s *responsesStreamState) closeMessage(w io.Writer) {
	if !s.message.open {
		return
	}
	s.message.open = false
	text := s.message.buf.String()
	writeSSE(w, "response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"item_id":       s.message.id,
		"output_index":  s.outputIdx,
		"content_index": 0,
		"text":          text,
	})
	writeSSE(w, "response.content_part.done", map[string]any{
		"type":          "response.content_part.done",
		"item_id":       s.message.id,
		"output_index":  s.outputIdx,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": text,
		},
	})
	writeSSE(w, "response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": s.outputIdx,
		"item": map[string]any{
			"id":      s.message.id,
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": text,
			}},
		},
	})
	s.outputIdx++
}

func (s *responsesStreamState) handleToolCalls(toolCalls []struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}, w io.Writer) error {
	if s.toolCalls == nil {
		s.toolCalls = make(map[int]*toolCallState)
	}
	for _, tc := range toolCalls {
		tcState, ok := s.toolCalls[tc.Index]
		if !ok {
			// Close message/reasoning before starting a function call
			s.closeReasoning(w)
			s.closeMessage(w)
			tcState = &toolCallState{
				id:     "fc_" + s.id + "_" + strconv.Itoa(tc.Index),
				callID: tc.ID,
				name:   tc.Function.Name,
				open:   true,
			}
			s.toolCalls[tc.Index] = tcState
			if err := writeSSE(w, "response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": s.outputIdx,
				"item": map[string]any{
					"id":       tcState.id,
					"type":     "function_call",
					"call_id":  tcState.callID,
					"name":     tcState.name,
					"arguments": "",
				},
			}); err != nil {
				return err
			}
		}
		if tc.Function.Arguments != "" {
			tcState.arguments.WriteString(tc.Function.Arguments)
			if err := writeSSE(w, "response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      tcState.id,
				"output_index": s.outputIdx,
				"delta":        tc.Function.Arguments,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *responsesStreamState) closeToolCalls(w io.Writer) {
	for _, tc := range s.toolCalls {
		if !tc.open {
			continue
		}
		tc.open = false
		args := tc.arguments.String()
		writeSSE(w, "response.function_call_arguments.done", map[string]any{
			"type":         "response.function_call_arguments.done",
			"item_id":      tc.id,
			"output_index": s.outputIdx,
			"arguments":    args,
		})
		writeSSE(w, "response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": s.outputIdx,
			"item": map[string]any{
				"id":        tc.id,
				"type":      "function_call",
				"call_id":   tc.callID,
				"name":      tc.name,
				"arguments": args,
			},
		})
		s.outputIdx++
	}
}

func (s *responsesStreamState) closeAll(w io.Writer) error {
	s.closeReasoning(w)
	s.closeMessage(w)
	s.closeToolCalls(w)
	return nil
}

func (s *responsesStreamState) finish(w io.Writer) error {
	if !s.created {
		return nil
	}
	s.closeAll(w)
	return writeSSE(w, "response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":      s.id,
			"object":  "response",
			"status":  "completed",
		},
	})
}

func writeSSE(w io.Writer, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	return err
}

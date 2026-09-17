package translator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jhon/gorouter/internal/infra/sse"
)

// translateAnthropicToOpenAIRequestImpl converts an Anthropic /v1/messages
// request body into an OpenAI chat/completions request.
func translateAnthropicToOpenAIRequestImpl(upstreamModel string, body []byte) ([]byte, error) {
	var in struct {
		Model       string             `json:"model"`
		System      json.RawMessage    `json:"system,omitempty"`
		Messages    []anthropicMessage `json:"messages"`
		MaxTokens   int                `json:"max_tokens"`
		Temperature *float64           `json:"temperature,omitempty"`
		TopP        *float64           `json:"top_p,omitempty"`
		Stop        []string           `json:"stop_sequences,omitempty"`
		Stream      bool               `json:"stream"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("anthropic->openai: parse: %w", err)
	}
	out := openaiRequest{Model: upstreamModel, Stream: in.Stream, MaxTokens: &in.MaxTokens, Temperature: in.Temperature, TopP: in.TopP}
	if in.System != nil {
		out.Messages = append(out.Messages, openaiMessage{Role: "system", Content: systemToOpenAIContent(in.System)})
	}
	for _, m := range in.Messages {
		out.Messages = append(out.Messages, openaiMessage{Role: m.Role, Content: m.Content})
	}
	if len(in.Stop) > 0 {
		raw, _ := json.Marshal(in.Stop)
		out.Stop = raw
	}
	return json.Marshal(out)
}

// systemToOpenAIContent turns an Anthropic system field (string or array of
// {text} blocks) into an OpenAI-style content (string).
func systemToOpenAIContent(raw json.RawMessage) json.RawMessage {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		b, _ := json.Marshal(s)
		return b
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if t, ok := blk["text"].(string); ok {
				b.WriteString(t)
			}
		}
		out, _ := json.Marshal(b.String())
		return out
	}
	return raw
}

// translateAnthropicToOpenAIResponseJSONImpl converts an Anthropic /v1/messages
// JSON response into an OpenAI chat/completions JSON response.
func translateAnthropicToOpenAIResponseJSONImpl(body []byte) ([]byte, error) {
	var in struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("anthropic->openai response: parse: %w", err)
	}
	var text strings.Builder
	var reasoning strings.Builder
	for _, c := range in.Content {
		switch c.Type {
		case "text":
			text.WriteString(c.Text)
		case "thinking":
			reasoning.WriteString(c.Thinking)
		}
	}
	message := map[string]any{"role": "assistant", "content": text.String()}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	out := map[string]any{
		"id":     in.ID,
		"object": "chat.completion",
		"model":  in.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": anthropicStopToOpenAI(in.StopReason),
		}},
		"usage": map[string]any{
			"prompt_tokens":     in.Usage.InputTokens,
			"completion_tokens": in.Usage.OutputTokens,
			"total_tokens":      in.Usage.InputTokens + in.Usage.OutputTokens,
		},
	}
	return json.Marshal(out)
}

// translateOpenAIToAnthropicResponseJSONImpl converts an OpenAI
// chat/completions JSON response into an Anthropic /v1/messages JSON
// response (for the rare Anthropic-client -> OpenAI-upstream case).
func translateOpenAIToAnthropicResponseJSONImpl(body []byte) ([]byte, error) {
	var in struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message      map[string]json.RawMessage `json:"message"`
			FinishReason string                     `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("openai->anthropic response: parse: %w", err)
	}
	content := make([]map[string]any, 0, 2)
	if len(in.Choices) > 0 {
		if raw, ok := in.Choices[0].Message["content"]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil && s != "" {
				content = append(content, map[string]any{"type": "text", "text": s})
			}
		}
		// A turn that only calls a tool has no text content. Without the
		// tool_use block an Anthropic client that retried without streaming
		// (Claude Code does exactly that) sees an empty answer and stops.
		if raw, ok := in.Choices[0].Message["tool_calls"]; ok {
			var calls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal(raw, &calls); err == nil {
				for i, tc := range calls {
					input := map[string]any{}
					if strings.TrimSpace(tc.Function.Arguments) != "" {
						_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
					}
					id := tc.ID
					if id == "" {
						id = fmt.Sprintf("toolu_%d", i)
					}
					content = append(content, map[string]any{
						"type":  "tool_use",
						"id":    id,
						"name":  tc.Function.Name,
						"input": input,
					})
				}
			}
		}
	}
	stop := openAIToAnthropicStop(firstOr(in.Choices, "finish_reason"))
	out := map[string]any{
		"id":          in.ID,
		"type":        "message",
		"role":        "assistant",
		"model":       in.Model,
		"content":     content,
		"stop_reason": stop,
		"usage": map[string]any{
			"input_tokens":  in.Usage.PromptTokens,
			"output_tokens": in.Usage.CompletionTokens,
		},
	}
	return json.Marshal(out)
}

func firstOr(cs []struct {
	Message      map[string]json.RawMessage `json:"message"`
	FinishReason string                     `json:"finish_reason"`
}, key string) string {
	if len(cs) == 0 {
		return ""
	}
	return cs[0].FinishReason
}

// ----- Streaming adapters -----
//
// The stream adapters consume upstream SSE and produce SSE in the client's
// format. Each event is parsed, the relevant fields picked out, and the
// translated event written.

type sseReader struct {
	r *bufio.Reader
}

// newAnthropicToOpenAIStreamReader wraps an Anthropic SSE body and emits
// OpenAI chat.completion.chunk events.
func newAnthropicToOpenAIStreamReader(ctx context.Context, body io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReader(body)
	pr, pw := io.Pipe()
	go func() {
		defer body.Close()
		id := ""
		model := ""
		err := streamAnthropicToOpenAI(ctx, br, pw, &id, &model)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

func streamAnthropicToOpenAI(ctx context.Context, br *bufio.Reader, w io.Writer, id, model *string) error {
	first := true
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
		var ev map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		typ, _ := asString(ev["type"])
		switch typ {
		case "message_start":
			var msg struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			_ = json.Unmarshal(ev["message"], &msg)
			*id = msg.ID
			*model = msg.Model
			promptTokens = msg.Usage.InputTokens
		case "content_block_delta":
			var d struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			}
			_ = json.Unmarshal(ev["delta"], &d)
			if d.Type == "thinking_delta" {
				if d.Thinking == "" {
					continue
				}
				chunk := openAIStreamReasoningChunk(*id, *model, d.Thinking)
				if _, err := w.Write([]byte("data: " + chunk + "\n\n")); err != nil {
					return err
				}
				continue
			}
			if d.Type != "text_delta" {
				continue
			}
			chunk := openAIStreamChunk(*id, *model, d.Text, first, nil, "")
			first = false
			if _, err := w.Write([]byte("data: " + chunk + "\n\n")); err != nil {
				return err
			}
		case "message_delta":
			if ev["usage"] != nil {
				var u struct {
					OutputTokens int `json:"output_tokens"`
				}
				_ = json.Unmarshal(ev["usage"], &u)
				completionTokens = u.OutputTokens
			}
		case "message_stop":
			usage := map[string]any{
				"prompt_tokens":     promptTokens,
				"completion_tokens": completionTokens,
				"total_tokens":      promptTokens + completionTokens,
			}
			chunk := openAIStreamChunk(*id, *model, "", first, usage, "")
			if _, err := w.Write([]byte("data: " + chunk + "\n\n")); err != nil {
				return err
			}
		}
	}
}

func openAIStreamChunk(id, model, content string, includeRole bool, usage map[string]any, finishReason string) string {
	choices := []map[string]any{{
		"index":         0,
		"delta":         map[string]any{"content": content},
		"finish_reason": nil,
	}}
	if includeRole {
		choices[0]["delta"] = map[string]any{"role": "assistant", "content": content}
	}
	if finishReason != "" {
		choices[0]["finish_reason"] = finishReason
	}
	out := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": choices,
	}
	if usage != nil {
		out["usage"] = usage
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// openAIStreamToolCallHeader emits the first chunk of a tool call: the
// assistant role + tool_calls entry with id/name and empty arguments.
func openAIStreamToolCallHeader(id, model string, idx int, callID, name string) string {
	out := map[string]any{
		"id":     id,
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{{
					"index": idx, "id": callID, "type": "function",
					"function": map[string]any{"name": name, "arguments": ""},
				}},
			},
			"finish_reason": nil,
		}},
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// openAIStreamToolCallDelta emits an incremental arguments fragment for an
// already-declared tool call.
func openAIStreamToolCallDelta(id, model string, idx int, arguments string) string {
	out := map[string]any{
		"id":     id,
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index":    idx,
					"function": map[string]any{"arguments": arguments},
				}},
			},
			"finish_reason": nil,
		}},
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// openAIStreamReasoningChunk emits an incremental reasoning_content fragment
// (the OpenAI convention for models that expose chain-of-thought, e.g.
// DeepSeek). Used when a Responses upstream sends reasoning summary deltas.
func openAIStreamReasoningChunk(id, model, reasoning string) string {
	out := map[string]any{
		"id":     id,
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"reasoning_content": reasoning,
			},
			"finish_reason": nil,
		}},
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// newOpenAIToAnthropicStreamReader wraps an OpenAI SSE body and emits
// Anthropic-style events (message_start, content_block_delta, message_stop).
func newOpenAIToAnthropicStreamReader(ctx context.Context, body io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReader(body)
	pr, pw := io.Pipe()
	go func() {
		defer body.Close()
		err := streamOpenAIToAnthropic(ctx, br, pw)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

// anthropicStreamState tracks the Anthropic content blocks opened while
// translating an OpenAI SSE stream. Every opened block must be closed in order
// before message_delta/message_stop: Claude Code aborts the turn
// ("Streaming response ended before any complete data was received") when a
// block is left open or the closing events never arrive.
type anthropicStreamState struct {
	w         io.Writer
	started   bool
	finished  bool
	nextIndex int
	textIndex int

	stopReason   string
	inputTokens  int
	outputTokens int

	toolOrder  []int
	toolBlocks map[int]int // upstream tool_call index -> anthropic block index
	toolArgs   map[int]*strings.Builder
	toolNames  map[int]string
	toolIDs    map[int]string

	opened []int // anthropic block indexes, in the order they were opened
}

func (s *anthropicStreamState) writeEvent(name string, payload map[string]any) {
	b, _ := json.Marshal(payload)
	_, _ = io.WriteString(s.w, "event: "+name+"\ndata: "+string(b)+"\n\n")
}

// startMessage emits message_start exactly once. Anthropic opens a message
// with an EMPTY content array — blocks are announced by content_block_start.
func (s *anthropicStreamState) startMessage(id, model string) {
	if s.started {
		return
	}
	s.started = true
	if id == "" {
		id = nextAnthropicID("msg_")
	}
	s.writeEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            id,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  s.inputTokens,
				"output_tokens": 0,
			},
		},
	})
}

func (s *anthropicStreamState) openText() {
	if s.textIndex >= 0 {
		return
	}
	s.textIndex = s.nextIndex
	s.nextIndex++
	s.opened = append(s.opened, s.textIndex)
	s.writeEvent("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         s.textIndex,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
}

// noteTool records a streamed tool call and opens its tool_use block as soon
// as the function name is known (name usually arrives with the first chunk and
// arguments follow it).
func (s *anthropicStreamState) noteTool(idx int, id, name string) {
	if _, ok := s.toolBlocks[idx]; !ok {
		if _, seen := s.toolIDs[idx]; !seen {
			s.toolOrder = append(s.toolOrder, idx)
		}
	}
	if id != "" {
		s.toolIDs[idx] = id
	}
	if name != "" {
		s.toolNames[idx] = name
		s.openTool(idx)
	}
}

func (s *anthropicStreamState) openTool(idx int) {
	if _, ok := s.toolBlocks[idx]; ok {
		return
	}
	block := s.nextIndex
	s.nextIndex++
	s.toolBlocks[idx] = block
	s.opened = append(s.opened, block)

	id := s.toolIDs[idx]
	if id == "" {
		id = nextAnthropicID("toolu_")
	}
	s.writeEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": block,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  s.toolNames[idx],
			"input": map[string]any{},
		},
	})
	// Flush arguments that arrived before the name did.
	if buf := s.toolArgs[idx]; buf != nil && buf.Len() > 0 {
		s.emitToolArgs(idx, buf.String())
		buf.Reset()
	}
}

func (s *anthropicStreamState) writeToolArgs(idx int, args string) {
	if _, open := s.toolBlocks[idx]; !open {
		if s.toolArgs[idx] == nil {
			s.toolArgs[idx] = &strings.Builder{}
		}
		s.toolArgs[idx].WriteString(args)
		return
	}
	s.emitToolArgs(idx, args)
}

func (s *anthropicStreamState) emitToolArgs(idx int, args string) {
	s.writeEvent("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.toolBlocks[idx],
		"delta": map[string]any{"type": "input_json_delta", "partial_json": args},
	})
}

// finish closes every open block and terminates the message. A client that
// never sees message_delta/message_stop treats the turn as truncated and
// retries the whole request without streaming.
func (s *anthropicStreamState) finish() {
	if s.finished {
		return
	}
	s.finished = true
	for _, idx := range s.toolOrder {
		s.openTool(idx)
	}
	for _, idx := range s.opened {
		s.writeEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": idx,
		})
	}
	reason := s.stopReason
	if reason == "" {
		reason = "end_turn"
	}
	s.writeEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": s.outputTokens},
	})
	s.writeEvent("message_stop", map[string]any{"type": "message_stop"})
}

func streamOpenAIToAnthropic(ctx context.Context, br *bufio.Reader, w io.Writer) error {
	st := &anthropicStreamState{
		w:          w,
		textIndex:  -1,
		toolBlocks: map[int]int{},
		toolArgs:   map[int]*strings.Builder{},
		toolNames:  map[int]string{},
		toolIDs:    map[int]string{},
	}
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
			// The upstream may end without ever sending a usable chunk: emit a
			// complete (empty) message instead of a bare [DONE].
			st.startMessage("", "")
			st.finish()
			return nil
		}
		if data == "" {
			continue
		}
		var ev struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Role      string `json:"role"`
					Content   string `json:"content"`
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
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if ev.Usage.PromptTokens > 0 {
			st.inputTokens = ev.Usage.PromptTokens
		}
		if ev.Usage.CompletionTokens > 0 {
			st.outputTokens = ev.Usage.CompletionTokens
		}
		st.startMessage(ev.ID, ev.Model)
		if len(ev.Choices) == 0 {
			continue
		}
		c := ev.Choices[0]
		if c.Delta.Content != "" {
			st.openText()
			st.writeEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": st.textIndex,
				"delta": map[string]any{"type": "text_delta", "text": c.Delta.Content},
			})
		}
		for _, tc := range c.Delta.ToolCalls {
			st.noteTool(tc.Index, tc.ID, tc.Function.Name)
			if tc.Function.Arguments != "" {
				st.writeToolArgs(tc.Index, tc.Function.Arguments)
			}
		}
		if c.FinishReason != "" {
			st.stopReason = openAIToAnthropicStop(c.FinishReason)
		}
	}
}

// nextAnthropicID synthesizes an id when the upstream omitted one; Anthropic
// clients key blocks and messages by id.
var anthropicIDSeq uint64

func nextAnthropicID(prefix string) string {
	return prefix + strconv.FormatUint(atomic.AddUint64(&anthropicIDSeq, 1), 36)
}

// asString extracts a JSON string field; empty on parse failure.
func asString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err
}

// sse.ParseEvent is used via readEvent; ensure sse import isn't dropped.
var _ = sse.Headers

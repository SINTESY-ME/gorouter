package translator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
		TopP        *float64            `json:"top_p,omitempty"`
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
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("anthropic->openai response: parse: %w", err)
	}
	var text strings.Builder
	for _, c := range in.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	out := map[string]any{
		"id":      in.ID,
		"object":  "chat.completion",
		"model":   in.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text.String()},
			"finish_reason": anthropicStopToOpenAI(in.StopReason),
		}},
		"usage": map[string]any{
			"prompt_tokens":     in.Usage.InputTokens,
			"completion_tokens": in.Usage.OutputTokens,
			"total_tokens":       in.Usage.InputTokens + in.Usage.OutputTokens,
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
	var textContent []map[string]string
	if len(in.Choices) > 0 {
		if raw, ok := in.Choices[0].Message["content"]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil && s != "" {
				textContent = []map[string]string{{"type": "text", "text": s}}
			}
		}
	}
	stop := openAIToAnthropicStop(firstOr(in.Choices, "finish_reason"))
	out := map[string]any{
		"id":      in.ID,
		"type":    "message",
		"role":    "assistant",
		"model":   in.Model,
		"content": textContent,
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
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(ev["delta"], &d)
			if d.Type != "text_delta" {
				continue
			}
			chunk := openAIStreamChunk(*id, *model, d.Text, first, nil)
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
			chunk := openAIStreamChunk(*id, *model, "", first, usage)
			if _, err := w.Write([]byte("data: " + chunk + "\n\n")); err != nil {
				return err
			}
		}
	}
}

func openAIStreamChunk(id, model, content string, includeRole bool, usage map[string]any) string {
	choices := []map[string]any{{
		"index":         0,
		"delta":         map[string]any{"content": content},
		"finish_reason": nil,
	}}
	if includeRole {
		choices[0]["delta"] = map[string]any{"role": "assistant", "content": content}
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

func streamOpenAIToAnthropic(ctx context.Context, br *bufio.Reader, w io.Writer) error {
	started := false
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
			_, _ = w.Write([]byte("event: message_stop\ndata:{}\n\n"))
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
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if !started && ev.ID != "" {
			started = true
			_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[{\"type\":\"text\",\"text\":\"\"}],\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n", ev.ID, ev.Model)
			_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		}
		if len(ev.Choices) > 0 {
			c := ev.Choices[0].Delta.Content
			if c != "" {
				payload := map[string]any{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]any{"type": "text_delta", "text": c},
				}
				b, _ := json.Marshal(payload)
				_, _ = w.Write([]byte("event: content_block_delta\ndata: " + string(b) + "\n\n"))
			}
		}
	}
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
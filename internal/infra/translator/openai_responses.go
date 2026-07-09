package translator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

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
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("responses->openai: parse: %w", err)
	}
	out := openaiRequest{Model: upstreamModel, Stream: in.Stream, MaxTokens: in.MaxOutputTokens, Temperature: in.Temperature, TopP: in.TopP}
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
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("responses->openai: parse input: %w", err)
	}
	var out []openaiMessage
	for _, m := range arr {
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
	started := false
	id := ""
	model := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, done, err := readEvent(&sseReader{r: br})
		if err != nil {
			if err == io.EOF {
				// Stream ended without [DONE] — emit response.completed so
				// clients waiting for it (e.g. codex) can finish cleanly.
				if started {
					_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"model\":%q}}\n\n", id, model)
				}
				return nil
			}
			return err
		}
		if done {
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"model\":%q}}\n\n", id, model)
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
					Reasoning string `json:"reasoning"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if !started && ev.ID != "" {
			started = true
			id = ev.ID
			model = ev.Model
			_, _ = fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":%q,\"model\":%q}}\n\n", id, model)
		}
		if len(ev.Choices) > 0 {
			d := &ev.Choices[0].Delta
			if d.Reasoning != "" {
				payload, _ := json.Marshal(map[string]any{
					"type":  "response.reasoning_text.delta",
					"delta": d.Reasoning,
				})
				_, _ = fmt.Fprintf(w, "event: response.reasoning_text.delta\ndata: %s\n\n", payload)
			}
			if d.Content != "" {
				payload, _ := json.Marshal(map[string]any{
					"type":  "response.output_text.delta",
					"delta": d.Content,
				})
				_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", payload)
			}
		}
	}
}

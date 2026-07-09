package translator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
)

func init() {
	register(domain.FormatOpenAI, domain.FormatGemini, pair{
		translateRequest:        translateOpenAIToGeminiRequest,
		translateResponseJSON:   translateGeminiToOpenAIResponseJSON,
		translateResponseStream: geminiStreamToOpenAI,
	})
	register(domain.FormatGemini, domain.FormatOpenAI, pair{
		translateRequest:        translateGeminiToOpenAIRequest,
		translateResponseJSON:   translateOpenAIToGeminiResponseJSON,
		translateResponseStream: openAIStreamToGemini,
	})
}

func translateOpenAIToGeminiRequest(upstreamModel string, body []byte) ([]byte, error) {
	var r openaiRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("openai->gemini: parse: %w", err)
	}
	out := map[string]any{}
	for _, m := range r.Messages {
		if m.Role == "system" {
			out["systemInstruction"] = map[string]any{
				"parts": []map[string]any{{"text": asStringContent(m.Content)}},
			}
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents, _ := out["contents"].([]map[string]any)
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": asStringContent(m.Content)}},
		})
		out["contents"] = contents
	}
	genCfg := map[string]any{}
	if r.MaxTokens != nil {
		genCfg["maxOutputTokens"] = *r.MaxTokens
	} else {
		genCfg["maxOutputTokens"] = 4096
	}
	if r.Temperature != nil {
		genCfg["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		genCfg["topP"] = *r.TopP
	}
	if stops := parseStop(r.Stop); len(stops) > 0 {
		genCfg["stopSequences"] = stops
	}
	out["generationConfig"] = genCfg
	return json.Marshal(out)
}

func translateGeminiToOpenAIResponseJSON(body []byte) ([]byte, error) {
	var in struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
				Role string `json:"role"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount      int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("gemini->openai response: parse: %w", err)
	}
	var text strings.Builder
	finishReason := "stop"
	if len(in.Candidates) > 0 {
		c := in.Candidates[0]
		for _, p := range c.Content.Parts {
			text.WriteString(p.Text)
		}
		finishReason = geminiFinishToOpenAI(c.FinishReason)
	}
	out := map[string]any{
		"object": "chat.completion",
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text.String()},
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     in.UsageMetadata.PromptTokenCount,
			"completion_tokens": in.UsageMetadata.CandidatesTokenCount,
			"total_tokens":      in.UsageMetadata.TotalTokenCount,
		},
	}
	return json.Marshal(out)
}

func geminiStreamToOpenAI(ctx context.Context, r io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	pr, pw := io.Pipe()
	go func() {
		defer r.Close()
		err := streamGeminiToOpenAI(ctx, br, pw)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

func streamGeminiToOpenAI(ctx context.Context, br *bufio.Reader, w io.Writer) error {
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
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
					Role string `json:"role"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata *struct {
				PromptTokenCount      int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if ev.UsageMetadata != nil {
			promptTokens = ev.UsageMetadata.PromptTokenCount
			completionTokens = ev.UsageMetadata.CandidatesTokenCount
		}
		if len(ev.Candidates) > 0 {
			for _, p := range ev.Candidates[0].Content.Parts {
				if p.Text == "" {
					continue
				}
				chunk := openAIStreamChunk(id, model, p.Text, first, nil)
				first = false
				if _, err := w.Write([]byte("data: " + chunk + "\n\n")); err != nil {
					return err
				}
			}
			if ev.Candidates[0].FinishReason != "" && ev.Candidates[0].FinishReason != "FINISH_REASON_UNSPECIFIED" {
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
}

func translateGeminiToOpenAIRequest(upstreamModel string, body []byte) ([]byte, error) {
	var in struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		GenerationConfig struct {
			MaxOutputTokens *int      `json:"maxOutputTokens"`
			Temperature     *float64 `json:"temperature"`
			TopP            *float64 `json:"topP"`
			StopSequences   []string `json:"stopSequences"`
		} `json:"generationConfig"`
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("gemini->openai: parse: %w", err)
	}
	out := openaiRequest{Model: upstreamModel, Stream: in.Stream}
	if in.SystemInstruction != nil {
		var sysText strings.Builder
		for _, p := range in.SystemInstruction.Parts {
			sysText.WriteString(p.Text)
		}
		b, _ := json.Marshal(sysText.String())
		out.Messages = append(out.Messages, openaiMessage{Role: "system", Content: b})
	}
	for _, c := range in.Contents {
		role := c.Role
		if role == "model" {
			role = "assistant"
		}
		if role != "user" && role != "assistant" && role != "system" {
			role = "user"
		}
		var text strings.Builder
		for _, p := range c.Parts {
			text.WriteString(p.Text)
		}
		b, _ := json.Marshal(text.String())
		out.Messages = append(out.Messages, openaiMessage{Role: role, Content: b})
	}
	out.MaxTokens = in.GenerationConfig.MaxOutputTokens
	out.Temperature = in.GenerationConfig.Temperature
	out.TopP = in.GenerationConfig.TopP
	if len(in.GenerationConfig.StopSequences) > 0 {
		raw, _ := json.Marshal(in.GenerationConfig.StopSequences)
		out.Stop = raw
	}
	return json.Marshal(out)
}

func translateOpenAIToGeminiResponseJSON(body []byte) ([]byte, error) {
	var in struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("openai->gemini response: parse: %w", err)
	}
	text := ""
	finish := "STOP"
	if len(in.Choices) > 0 {
		text = in.Choices[0].Message.Content
		finish = openAIToGeminiFinish(in.Choices[0].FinishReason)
	}
	out := map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{"text": text}},
				"role":  "model",
			},
			"finishReason": finish,
			"index":        0,
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     in.Usage.PromptTokens,
			"candidatesTokenCount": in.Usage.CompletionTokens,
			"totalTokenCount":      in.Usage.PromptTokens + in.Usage.CompletionTokens,
		},
	}
	return json.Marshal(out)
}

func openAIStreamToGemini(ctx context.Context, r io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	pr, pw := io.Pipe()
	go func() {
		defer r.Close()
		err := streamOpenAIToGemini(ctx, br, pw)
		_ = pw.CloseWithError(err)
	}()
	return pr, nil
}

func streamOpenAIToGemini(ctx context.Context, br *bufio.Reader, w io.Writer) error {
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
			return nil
		}
		if data == "" {
			continue
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		if len(ev.Choices) > 0 {
			c := ev.Choices[0]
			if c.Delta.Content != "" {
				payload := map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": c.Delta.Content}},
							"role":  "model",
						},
					}},
				}
				b, _ := json.Marshal(payload)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			}
			if c.FinishReason != "" {
				payload := map[string]any{
					"candidates": []map[string]any{{
						"content":      map[string]any{"parts": []map[string]any{{"text": ""}}, "role": "model"},
						"finishReason": openAIToGeminiFinish(c.FinishReason),
					}},
				}
				b, _ := json.Marshal(payload)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			}
		}
	}
}

func geminiFinishToOpenAI(s string) string {
	switch s {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	default:
		return "stop"
	}
}

func openAIToGeminiFinish(s string) string {
	switch s {
	case "stop", "":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}

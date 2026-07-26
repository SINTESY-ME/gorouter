package translator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

var thoughtSignatureCache sync.Map // string -> string

func init() {
	register(domain.FormatOpenAI, domain.FormatGemini, pair{
		translateRequest:        translateOpenAIToGeminiRequest,
		translateResponseJSON:   translateOpenAIToGeminiResponseJSON,
		translateResponseStream: openAIStreamToGemini,
	})
	register(domain.FormatGemini, domain.FormatOpenAI, pair{
		translateRequest:        translateGeminiToOpenAIRequest,
		translateResponseJSON:   translateGeminiToOpenAIResponseJSON,
		translateResponseStream: geminiStreamToOpenAI,
	})
}

func translateOpenAIToGeminiRequest(upstreamModel string, body []byte) ([]byte, error) {
	var r openaiRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("openai->gemini: parse: %w", err)
	}

	// 1. Pass 1: map tool_call_id -> function_name and tool_call_id -> thoughtSignature
	toolCallIDToName := make(map[string]string)
	toolCallIDToThought := make(map[string]string)

	for _, m := range r.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				name := tc.Function.Name
				if name != "" {
					toolCallIDToName[tc.ID] = name
					if strings.HasPrefix(tc.ID, "call_") {
						toolCallIDToName[strings.TrimPrefix(tc.ID, "call_")] = name
					}
				}
				ts := tc.ThoughtSignature
				if ts == "" {
					ts = tc.ThoughtSignatureCamel
				}
				if ts == "" {
					if v, ok := thoughtSignatureCache.Load(tc.ID); ok {
						ts, _ = v.(string)
					}
				}
				if ts == "" && name != "" {
					if v, ok := thoughtSignatureCache.Load(name); ok {
						ts, _ = v.(string)
					}
				}
				if ts != "" {
					toolCallIDToThought[tc.ID] = ts
					if name != "" {
						toolCallIDToThought[name] = ts
					}
				}
			}
		}
	}

	// 2. Pass 2: build contents
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

		var parts []map[string]any
		text := asStringContent(m.Content)

		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				var args map[string]any
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				if args == nil {
					args = map[string]any{}
				}
				fc := map[string]any{
					"name": tc.Function.Name,
					"args": args,
				}
				part := map[string]any{"functionCall": fc}

				ts := tc.ThoughtSignature
				if ts == "" {
					ts = tc.ThoughtSignatureCamel
				}
				if ts == "" {
					ts = toolCallIDToThought[tc.ID]
				}
				if ts == "" {
					ts = toolCallIDToThought[tc.Function.Name]
				}
				if ts == "" {
					if v, ok := thoughtSignatureCache.Load(tc.ID); ok {
						ts, _ = v.(string)
					}
				}
				if ts == "" && tc.Function.Name != "" {
					if v, ok := thoughtSignatureCache.Load(tc.Function.Name); ok {
						ts, _ = v.(string)
					}
				}
				if ts != "" {
					part["thoughtSignature"] = ts
				}
				parts = append(parts, part)
			}
		} else if m.Role == "tool" {
			role = "user"
			funcName := toolCallIDToName[m.ToolCallID]
			if funcName == "" {
				funcName = strings.TrimPrefix(m.ToolCallID, "call_")
			}
			var contentMap map[string]any
			contentStr := text
			if err := json.Unmarshal([]byte(contentStr), &contentMap); err != nil {
				contentMap = map[string]any{"result": contentStr}
			}
			parts = append(parts, map[string]any{
				"functionResponse": map[string]any{
					"name": funcName,
					"response": map[string]any{"name": funcName, "content": contentMap},
				},
			})
		} else if text != "" {
			parts = append(parts, map[string]any{"text": text})
		}

		if len(parts) == 0 {
			continue
		}

		contents, _ := out["contents"].([]map[string]any)
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": parts,
		})
		out["contents"] = contents
	}

	if len(r.Tools) > 0 {
		var openAITools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		}
		if err := json.Unmarshal(r.Tools, &openAITools); err == nil {
			var funcs []map[string]any
			for _, t := range openAITools {
				if t.Type == "function" {
					funcs = append(funcs, map[string]any{
						"name":        t.Function.Name,
						"description": t.Function.Description,
						"parameters":  t.Function.Parameters,
					})
				}
			}
			if len(funcs) > 0 {
				out["tools"] = []map[string]any{{"functionDeclarations": funcs}}
			}
		}
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
					Text             string         `json:"text"`
					ThoughtSignature string         `json:"thoughtSignature"`
					FunctionCall     map[string]any `json:"functionCall"`
				} `json:"parts"`
				Role string `json:"role"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("gemini->openai response: parse: %w", err)
	}
	var text strings.Builder
	finishReason := "stop"
	var toolCalls []map[string]any
	if len(in.Candidates) > 0 {
		c := in.Candidates[0]
		for _, p := range c.Content.Parts {
			if p.FunctionCall != nil {
				argsStr, _ := json.Marshal(p.FunctionCall["args"])
				funcName, _ := p.FunctionCall["name"].(string)
				callID := fmt.Sprintf("call_%s", funcName)
				if rawID, ok := p.FunctionCall["id"].(string); ok && rawID != "" {
					callID = rawID
				}
				tc := map[string]any{
					"id":   callID,
					"type": "function",
					"function": map[string]any{
						"name":      funcName,
						"arguments": string(argsStr),
					},
				}
				if p.ThoughtSignature != "" {
					tc["thought_signature"] = p.ThoughtSignature
					thoughtSignatureCache.Store(callID, p.ThoughtSignature)
					if funcName != "" {
						thoughtSignatureCache.Store(funcName, p.ThoughtSignature)
					}
				}
				toolCalls = append(toolCalls, tc)
			}
			text.WriteString(p.Text)
		}
		finishReason = geminiFinishToOpenAI(c.FinishReason)
	}
	message := map[string]any{"role": "assistant", "content": text.String()}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		finishReason = "tool_calls"
	}
	out := map[string]any{
		"object": "chat.completion",
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
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
			if err == io.EOF {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				return nil
			}
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
			ResponseID   string `json:"responseId"`
			ModelVersion string `json:"modelVersion"`
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text             string         `json:"text"`
						ThoughtSignature string         `json:"thoughtSignature"`
						FunctionCall     map[string]any `json:"functionCall"`
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
		if id == "" {
			if ev.ResponseID != "" {
				id = ev.ResponseID
			} else {
				id = fmt.Sprintf("chatcmpl-gemini-%d", time.Now().UnixNano())
			}
		}
		if model == "" && ev.ModelVersion != "" {
			model = ev.ModelVersion
		}
		if ev.UsageMetadata != nil {
			promptTokens = ev.UsageMetadata.PromptTokenCount
			completionTokens = ev.UsageMetadata.CandidatesTokenCount
		}
		if len(ev.Candidates) > 0 {
			var text string
			var toolCalls []map[string]any
			for _, p := range ev.Candidates[0].Content.Parts {
				text += p.Text
				if p.FunctionCall != nil {
					argsStr, _ := json.Marshal(p.FunctionCall["args"])
					funcName, _ := p.FunctionCall["name"].(string)
					callID := fmt.Sprintf("call_%s", funcName)
					if rawID, ok := p.FunctionCall["id"].(string); ok && rawID != "" {
						callID = rawID
					}
					tc := map[string]any{
						"id":   callID,
						"type": "function",
						"function": map[string]any{
							"name":      funcName,
							"arguments": string(argsStr),
						},
					}
					if p.ThoughtSignature != "" {
						tc["thought_signature"] = p.ThoughtSignature
						thoughtSignatureCache.Store(callID, p.ThoughtSignature)
						if funcName != "" {
							thoughtSignatureCache.Store(funcName, p.ThoughtSignature)
						}
					}
					toolCalls = append(toolCalls, tc)
				}
			}
			
			if text != "" || len(toolCalls) > 0 {
				// Convert to array of tool_calls with index
				var sseToolCalls []map[string]any
				for i, tc := range toolCalls {
					tc["index"] = i
					sseToolCalls = append(sseToolCalls, tc)
				}
				
				// Need a custom chunk generator for tools
				delta := map[string]any{}
				if first {
					delta["role"] = "assistant"
					first = false
				}
				if text != "" {
					delta["content"] = text
				}
				if len(sseToolCalls) > 0 {
					delta["tool_calls"] = sseToolCalls
				}
				
				chunkMap := map[string]any{
					"object": "chat.completion.chunk",
					"id":     id,
					"model":  model,
					"choices": []map[string]any{{
						"index": 0,
						"delta": delta,
					}},
				}
				b, _ := json.Marshal(chunkMap)
				if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
					return err
				}
			}
			finish := ev.Candidates[0].FinishReason
			if finish != "" && finish != "FINISH_REASON_UNSPECIFIED" {
				finishStr := geminiFinishToOpenAI(finish)
				if len(toolCalls) > 0 {
					finishStr = "tool_calls"
				}
				
				chunkMap := map[string]any{
					"object": "chat.completion.chunk",
					"id":     id,
					"model":  model,
					"choices": []map[string]any{{
						"index":         0,
						"delta":         map[string]any{},
						"finish_reason": finishStr,
					}},
				}
				
				if ev.UsageMetadata != nil {
					chunkMap["usage"] = map[string]any{
						"prompt_tokens":     promptTokens,
						"completion_tokens": completionTokens,
						"total_tokens":      promptTokens + completionTokens,
					}
				}
				
				b, _ := json.Marshal(chunkMap)
				if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
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

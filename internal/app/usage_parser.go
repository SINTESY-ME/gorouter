package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

// maxUsageBuf caps how much of the response body we buffer for usage parsing.
// Most chat completions fit well under this; if exceeded we record 0 tokens.
const maxUsageBuf = 256 * 1024

// maxCacheBuf is the maximum body size we buffer when the response cache is
// enabled. Larger than maxUsageBuf because we need the full body to store
// for future cache hits. 4MB covers most non-streaming responses.
const maxCacheBuf = 4 * 1024 * 1024

// parseUsageFromJSON extracts prompt/completion token counts from an OpenAI
// chat.completion JSON response. Also extracts cache read/creation tokens
// when present (Anthropic and OpenAI prompt caching).
func parseUsageFromJSON(buf []byte) (prompt, completion int) {
	p, c, _, _ := parseUsageFromJSONFull(buf)
	return p, c
}

// parseUsageFromJSONFull extracts all token counts including cache tokens.
// Supports OpenAI (prompt_tokens_details.cached_tokens) and Anthropic
// (cache_read_input_tokens, cache_creation_input_tokens) formats.
func parseUsageFromJSONFull(buf []byte) (prompt, completion, cacheRead, cacheCreation int) {
	var resp struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			// Anthropic format
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			// OpenAI format
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(buf, &resp); err != nil || resp.Usage == nil {
		return 0, 0, 0, 0
	}
	prompt = resp.Usage.PromptTokens
	completion = resp.Usage.CompletionTokens
	if resp.Usage.CacheReadInputTokens > 0 {
		cacheRead = resp.Usage.CacheReadInputTokens
	}
	if resp.Usage.CacheCreationInputTokens > 0 {
		cacheCreation = resp.Usage.CacheCreationInputTokens
	}
	if resp.Usage.PromptTokensDetails != nil && resp.Usage.PromptTokensDetails.CachedTokens > 0 {
		cacheRead = resp.Usage.PromptTokensDetails.CachedTokens
	}
	return prompt, completion, cacheRead, cacheCreation
}

// parseUsageFromSSE scans buffered SSE events (OpenAI chat.completion.chunk
// format) and returns the token counts from the last chunk that carries a
// usage object. OpenAI emits usage in the final chunk when
// stream_options.include_usage is true. Also extracts cache tokens.
func parseUsageFromSSE(buf []byte) (prompt, completion int) {
	p, c, _, _ := parseUsageFromSSEFull(buf)
	return p, c
}

// parseUsageFromSSEFull extracts all token counts including cache tokens from
// buffered SSE events.
func parseUsageFromSSEFull(buf []byte) (prompt, completion, cacheRead, cacheCreation int) {
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var ev struct {
			Usage *struct {
				PromptTokens             int `json:"prompt_tokens"`
				CompletionTokens         int `json:"completion_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				PromptTokensDetails      *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				// Responses API format (input_tokens / output_tokens)
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			// Responses API response.completed nests usage under response.
			Response *struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		// OpenAI chat format (top-level usage)
		if ev.Usage != nil {
			if ev.Usage.PromptTokens > 0 || ev.Usage.CompletionTokens > 0 {
				prompt = ev.Usage.PromptTokens
				completion = ev.Usage.CompletionTokens
			}
			if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
				prompt = ev.Usage.InputTokens
				completion = ev.Usage.OutputTokens
			}
			if ev.Usage.CacheReadInputTokens > 0 {
				cacheRead = ev.Usage.CacheReadInputTokens
			}
			if ev.Usage.CacheCreationInputTokens > 0 {
				cacheCreation = ev.Usage.CacheCreationInputTokens
			}
			if ev.Usage.PromptTokensDetails != nil && ev.Usage.PromptTokensDetails.CachedTokens > 0 {
				cacheRead = ev.Usage.PromptTokensDetails.CachedTokens
			}
		}
		// Responses API response.completed (nested under response)
		if ev.Response != nil && ev.Response.Usage != nil {
			if ev.Response.Usage.InputTokens > 0 || ev.Response.Usage.OutputTokens > 0 {
				prompt = ev.Response.Usage.InputTokens
				completion = ev.Response.Usage.OutputTokens
			}
		}
	}
	return prompt, completion, cacheRead, cacheCreation
}

// injectStreamUsage adds stream_options.include_usage = true to an OpenAI
// chat/completions request body so the upstream includes token counts in the
// final SSE chunk. If the field already exists it is merged. Returns the
// original body if the input is not valid JSON.
// prepareOpenAIBody applies the OpenAI→OpenAI chat request rewrites in a
// single JSON parse + single marshal, replacing the previous sequence of
// independent parse/encode passes (rewriteModel, injectStreamUsage,
// sanitizeEmptyToolCalls). It:
//   - substitutes the upstream model id
//   - adds stream_options.include_usage for streaming requests
//   - strips empty tool_calls arrays (rejected by some providers, e.g. Qwen)
//
// Fail-open: if the body isn't JSON it is returned unchanged. When nothing
// actually changes (model already set, non-stream, no empty tool_calls) the
// original bytes are returned to avoid a needless re-encode.
func prepareOpenAIBody(body []byte, upstreamModel string, stream bool) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	changed := false
	if upstreamModel != "" {
		if cur, _ := m["model"].(string); cur != upstreamModel {
			m["model"] = upstreamModel
			changed = true
		}
	}
	if stream {
		so, _ := m["stream_options"].(map[string]any)
		if so == nil {
			so = map[string]any{}
			m["stream_options"] = so
			changed = true
		}
		if include, _ := so["include_usage"].(bool); !include {
			so["include_usage"] = true
			changed = true
		}
	}
	// ai-providers-internal (Codex/Responses) requires store=false explicitly.
	// LangChain sends it, but ensure it survives OpenAI->OpenAI re-encoding
	// even if the client omitted it or sent true.
	if cur, exists := m["store"]; !exists || cur != false {
		m["store"] = false
		changed = true
	}
	if msgs, ok := m["messages"].([]any); ok {
		for _, raw := range msgs {
			msg, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if arr, exists := msg["tool_calls"]; exists {
				if empty, ok := arr.([]any); ok && len(empty) == 0 {
					delete(msg, "tool_calls")
					changed = true
				}
			}
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// injectStreamUsage adds stream_options.include_usage to a streaming OpenAI
// request body so the upstream includes token usage in the final SSE chunk.
func injectStreamUsage(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	so, _ := m["stream_options"].(map[string]any)
	if so == nil {
		so = map[string]any{}
	}
	so["include_usage"] = true
	m["stream_options"] = so
	b, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

// sanitizeEmptyToolCalls removes "tool_calls": [] from messages in the request
// body. Some providers (e.g. Qwen/DashScope) reject messages with an empty
// tool_calls array, while OpenAI-compatible clients routinely send them. This
// strips the field when the array is empty so the request is accepted
// everywhere. Returns the original body if parsing fails.
func sanitizeEmptyToolCalls(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	msgs, ok := m["messages"].([]any)
	if !ok {
		return body
	}
	changed := false
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tc, exists := msg["tool_calls"]
		if !exists {
			continue
		}
		arr, ok := tc.([]any)
		if ok && len(arr) == 0 {
			delete(msg, "tool_calls")
			changed = true
		}
	}
	if !changed {
		return body
	}
	b, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

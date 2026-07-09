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

// parseUsageFromJSON extracts prompt/completion token counts from an OpenAI
// chat.completion JSON response.
func parseUsageFromJSON(buf []byte) (prompt, completion int) {
	var resp struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(buf, &resp); err != nil || resp.Usage == nil {
		return 0, 0
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens
}

// parseUsageFromSSE scans buffered SSE events (OpenAI chat.completion.chunk
// format) and returns the token counts from the last chunk that carries a
// usage object. OpenAI emits usage in the final chunk when
// stream_options.include_usage is true.
func parseUsageFromSSE(buf []byte) (prompt, completion int) {
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
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil || ev.Usage == nil {
			continue
		}
		if ev.Usage.PromptTokens > 0 || ev.Usage.CompletionTokens > 0 {
			prompt = ev.Usage.PromptTokens
			completion = ev.Usage.CompletionTokens
		}
	}
	return prompt, completion
}

// injectStreamUsage adds stream_options.include_usage = true to an OpenAI
// chat/completions request body so the upstream includes token counts in the
// final SSE chunk. If the field already exists it is merged. Returns the
// original body if the input is not valid JSON.
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

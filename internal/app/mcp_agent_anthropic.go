package app

import "encoding/json"

// anthropicAgent drives the agent loop for /v1/messages clients (Claude Code):
// tool calls are tool_use content blocks, and results come back as a user
// message carrying one tool_result block per call, keyed by tool_use id.
type anthropicAgent struct{}

func (anthropicAgent) ToolCalls(respBody []byte) ([]agentToolCall, error) {
	var resp struct {
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	var out []agentToolCall
	for _, block := range resp.Content {
		if block.Type != "tool_use" || block.Name == "" {
			continue
		}
		args := "{}"
		if len(block.Input) > 0 && string(block.Input) != "null" {
			args = string(block.Input)
		}
		out = append(out, agentToolCall{ID: block.ID, Name: block.Name, Args: args})
	}
	return out, nil
}

func (anthropicAgent) AppendTurn(prevBody, respBody []byte, results []agentToolResult) ([]byte, error) {
	var prev struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(prevBody, &prev); err != nil {
		return nil, err
	}
	var resp struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}

	msgs := make([]json.RawMessage, 0, len(prev.Messages)+2)
	msgs = append(msgs, prev.Messages...)

	// The assistant turn is replayed verbatim (text and tool_use blocks).
	assistant, err := json.Marshal(map[string]any{
		"role":    "assistant",
		"content": resp.Content,
	})
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, assistant)

	blocks := make([]map[string]any, 0, len(results))
	for _, r := range results {
		blocks = append(blocks, map[string]any{
			"type":        "tool_result",
			"tool_use_id": r.CallID,
			"content":     r.Text,
		})
	}
	user, err := json.Marshal(map[string]any{
		"role":    "user",
		"content": blocks,
	})
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, user)

	merged, err := json.Marshal(msgs)
	if err != nil {
		return nil, err
	}
	return setField(prevBody, "messages", merged)
}

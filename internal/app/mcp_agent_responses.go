package app

import "encoding/json"

// responsesAgent drives the agent loop for /v1/responses clients (Codex CLI):
// tool calls are output items of type function_call, and results come back as
// function_call_output items keyed by the original call_id.
//
// The assistant turn is replayed from the response's own output items, so the
// upstream sees exactly the items it produced (function_call, message and
// reasoning items are all valid input items).
type responsesAgent struct{}

func (responsesAgent) ToolCalls(respBody []byte) ([]agentToolCall, error) {
	var resp struct {
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	var out []agentToolCall
	for _, item := range resp.Output {
		if item.Type != "function_call" || item.Name == "" {
			continue
		}
		out = append(out, agentToolCall{ID: item.CallID, Name: item.Name, Args: item.Arguments})
	}
	return out, nil
}

func (responsesAgent) AppendTurn(prevBody, respBody []byte, results []agentToolResult) ([]byte, error) {
	var prev struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(prevBody, &prev); err != nil {
		return nil, err
	}
	items, err := responsesInputItems(prev.Input)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	for _, raw := range resp.Output {
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &head) != nil {
			continue
		}
		switch head.Type {
		case "function_call", "message", "reasoning":
			items = append(items, raw)
		}
	}
	for _, r := range results {
		item, err := json.Marshal(map[string]any{
			"type":    "function_call_output",
			"call_id": r.CallID,
			"output":  r.Text,
		})
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	merged, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return setField(prevBody, "input", merged)
}

// responsesInputItems normalizes the request's input into an item array. The
// Responses API accepts either a plain string or an array of items.
func responsesInputItems(input json.RawMessage) ([]json.RawMessage, error) {
	if len(input) == 0 {
		return nil, nil
	}
	var text string
	if json.Unmarshal(input, &text) == nil {
		item, err := json.Marshal(map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": text}},
		})
		if err != nil {
			return nil, err
		}
		return []json.RawMessage{item}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, err
	}
	return items, nil
}

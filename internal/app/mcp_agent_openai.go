package app

import "encoding/json"

// openAIChatAgent drives the agent loop for /v1/chat/completions clients:
// tool calls in choices[].message.tool_calls, results as role:"tool" messages.
type openAIChatAgent struct{}

// openaiToolCall is a tool_calls entry in a chat completion response.
type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (openAIChatAgent) ToolCalls(respBody []byte) ([]agentToolCall, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []openaiToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, nil
	}
	calls := resp.Choices[0].Message.ToolCalls
	out := make([]agentToolCall, 0, len(calls))
	for _, c := range calls {
		if c.Function.Name == "" {
			continue
		}
		out = append(out, agentToolCall{ID: c.ID, Name: c.Function.Name, Args: c.Function.Arguments})
	}
	return out, nil
}

func (openAIChatAgent) AppendTurn(prevBody, respBody []byte, results []agentToolResult) ([]byte, error) {
	var prev struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(prevBody, &prev); err != nil {
		return nil, err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content   json.RawMessage  `json:"content"`
				ToolCalls []openaiToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	var content json.RawMessage
	var calls []openaiToolCall
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
		calls = resp.Choices[0].Message.ToolCalls
	}

	msgs := make([]json.RawMessage, 0, len(prev.Messages)+1+len(results))
	msgs = append(msgs, prev.Messages...)

	assistant, err := json.Marshal(map[string]any{
		"role":       "assistant",
		"content":    content,
		"tool_calls": calls,
	})
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, assistant)

	for _, r := range results {
		toolMsg, err := json.Marshal(map[string]any{
			"role":         "tool",
			"tool_call_id": r.CallID,
			"content":      r.Text,
		})
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, toolMsg)
	}
	merged, err := json.Marshal(msgs)
	if err != nil {
		return nil, err
	}
	return setField(prevBody, "messages", merged)
}

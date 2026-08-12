package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// maxAgentDepth caps how many model round-trips a single request may perform
// in the MCP agent loop before the last response is returned as-is.
const maxAgentDepth = 5

// routeWithAgentLoop runs the server-side agent loop for a non-stream OpenAI
// chat request: dispatch → if the response carries tool_calls, execute each
// tool via the MCP gateway, append the assistant + tool messages to the
// conversation, and re-dispatch. Stops when the model stops calling tools or
// maxAgentDepth is reached.
func (s *RouterService) routeWithAgentLoop(ctx context.Context, modelStr string, body []byte, apiKey string, opts RouteOptions, requestID string) (*RouterResponse, error) {
	current := body
	for depth := 0; depth < maxAgentDepth; depth++ {
		res, err := s.routeChatDispatch(ctx, modelStr, current, false, apiKey, opts, requestID)
		if err != nil {
			return nil, err
		}
		// Only fully-buffered JSON responses participate in the loop.
		if res.Stream || res.StatusCode >= 400 || res.Body == nil {
			return res, nil
		}
		buf, rerr := io.ReadAll(res.Body)
		res.Body.Close()
		if rerr != nil {
			return res, rerr
		}
		calls, perr := extractOpenAIToolCalls(buf)
		if perr != nil || len(calls) == 0 {
			res.Body = io.NopCloser(bytes.NewReader(buf))
			return res, nil
		}
		next, berr := s.buildAgentTurn(ctx, current, buf, calls)
		if berr != nil {
			// If we cannot append tool results, return the tool-call
			// response so the client sees what the model asked for.
			res.Body = io.NopCloser(bytes.NewReader(buf))
			return res, nil
		}
		current = next
	}
	// Depth exhausted: return the last response as-is.
	res, err := s.routeChatDispatch(ctx, modelStr, current, false, apiKey, opts, requestID)
	return res, err
}

// openaiToolCall is a tool_calls entry in a chat completion response.
type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// extractOpenAIToolCalls returns the assistant tool_calls from a buffered
// OpenAI chat completion response.
func extractOpenAIToolCalls(body []byte) ([]openaiToolCall, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []openaiToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, nil
	}
	return resp.Choices[0].Message.ToolCalls, nil
}

// buildAgentTurn executes the given tool calls, then builds the next request
// body: previous conversation + assistant message (with tool_calls) + one
// role:"tool" message per executed tool.
func (s *RouterService) buildAgentTurn(ctx context.Context, prevBody []byte, respBody []byte, calls []openaiToolCall) ([]byte, error) {
	if s.MCP == nil {
		return nil, fmt.Errorf("mcp disabled")
	}
	var prev struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(prevBody, &prev); err != nil {
		return nil, err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	content := json.RawMessage(nil)
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
	}

	msgs := make([]json.RawMessage, 0, len(prev.Messages)+1+len(calls))
	msgs = append(msgs, prev.Messages...)

	// Assistant message replaying the tool_calls the model made.
	assistant, err := json.Marshal(map[string]any{
		"role":       "assistant",
		"content":    content,
		"tool_calls": calls,
	})
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, assistant)

	// Execute each tool and append its result as a role:"tool" message.
	for _, call := range calls {
		if call.Function.Name == "" {
			continue
		}
		result, err := s.MCP.Manager.ExecuteTool(ctx, call.Function.Name, call.Function.Arguments)
		if err != nil {
			result = fmt.Sprintf("tool %q execution failed: %v", call.Function.Name, err)
		}
		toolMsg, err := json.Marshal(map[string]any{
			"role":         "tool",
			"tool_call_id": call.ID,
			"content":      result,
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

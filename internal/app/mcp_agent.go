package app

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/jhon/gorouter/internal/domain"
)

// maxAgentDepth caps how many model round-trips a single request may perform
// in the MCP agent loop before the last turn is returned as-is.
const maxAgentDepth = 5

// agentToolCall is one tool call a model asked for, in wire-independent form.
type agentToolCall struct {
	// ID is what the client must echo back with the result: an OpenAI
	// tool_call id, a Responses call_id, or an Anthropic tool_use id.
	ID   string
	Name string
	// Args is the raw JSON arguments object as the model produced it.
	Args string
}

// agentToolResult is the outcome of executing one agentToolCall.
type agentToolResult struct {
	CallID string
	Text   string
}

// agentProtocol adapts the server-side tool loop to one client wire format: it
// reads the tool calls out of a completed turn and builds the next request
// body with the executed results appended. Keeping this per format is what
// lets the loop itself stay format-agnostic and shared by every endpoint.
type agentProtocol interface {
	// ToolCalls returns the tool calls a completed buffered turn asked for,
	// or nil when it asked for none.
	ToolCalls(respBody []byte) ([]agentToolCall, error)
	// AppendTurn returns the next request body: the previous conversation,
	// the assistant turn, and one result message per executed call.
	AppendTurn(prevBody, respBody []byte, results []agentToolResult) ([]byte, error)
}

// agentProtocolFor returns the loop adapter for a client format, or nil when
// that format has no adapter and therefore no server-side tool execution.
func agentProtocolFor(f domain.Format) agentProtocol {
	switch f {
	case domain.FormatOpenAI:
		return openAIChatAgent{}
	case domain.FormatResponses:
		return responsesAgent{}
	case domain.FormatAnthropic:
		return anthropicAgent{}
	}
	return nil
}

// routeWithAgentLoop runs the server-side agent loop for a buffered request:
// dispatch → if the turn asks for tools the gateway owns, execute them over
// MCP, append the assistant turn plus one result per call, and re-dispatch.
//
// It stops when the model stops calling tools, when a turn asks for a tool
// gorouter does not own (that tool belongs to the client, which must receive
// the turn untouched), at maxAgentDepth, or on any response the loop cannot
// work with (stream, error status, unparsable body).
//
// owned is the set of tool names the gateway may execute: nothing outside it
// is ever executed here, so a client's own tools are never hijacked.
func (s *RouterService) routeWithAgentLoop(ctx context.Context, modelStr string, body []byte, apiKey string, opts RouteOptions, requestID string, owned map[string]bool) (*RouterResponse, error) {
	proto := agentProtocolFor(opts.InputFormat)
	if proto == nil {
		return s.routeChatDispatch(ctx, modelStr, body, false, apiKey, opts, requestID)
	}
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
		// Every early exit hands the same bytes to the caller.
		res.Body = io.NopCloser(bytes.NewReader(buf))

		calls, perr := proto.ToolCalls(buf)
		if perr != nil || len(calls) == 0 || !allOwned(calls, owned) {
			return res, nil
		}
		next, berr := proto.AppendTurn(current, buf, s.executeAgentTools(ctx, calls))
		if berr != nil {
			// Fail open: the client still receives the turn and its calls.
			return res, nil
		}
		current = next
	}
	// Depth exhausted: return the last turn as-is.
	return s.routeChatDispatch(ctx, modelStr, current, false, apiKey, opts, requestID)
}

// allOwned reports whether every call belongs to the MCP gateway. A single
// foreign call means the turn is the client's to complete.
func allOwned(calls []agentToolCall, owned map[string]bool) bool {
	for _, c := range calls {
		if !owned[c.Name] {
			return false
		}
	}
	return true
}

// executeAgentTools runs each call against the owning MCP client. A failing
// tool still yields a result: the model has to learn what went wrong.
func (s *RouterService) executeAgentTools(ctx context.Context, calls []agentToolCall) []agentToolResult {
	results := make([]agentToolResult, 0, len(calls))
	for _, call := range calls {
		text, err := s.MCP.Manager.ExecuteTool(ctx, call.Name, call.Args)
		if err != nil {
			text = fmt.Sprintf("tool %q execution failed: %v", call.Name, err)
		}
		results = append(results, agentToolResult{CallID: call.ID, Text: text})
	}
	return results
}

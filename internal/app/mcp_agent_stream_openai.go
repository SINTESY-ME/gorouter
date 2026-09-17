package app

import (
	"encoding/json"
	"strings"

	"github.com/jhon/gorouter/internal/infra/sse"
)

// openAIStreamToolCall is a delta.tool_calls entry: the name and id arrive in
// the first entry of an index, the arguments in the ones that follow.
type openAIStreamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolCallAcc accumulates one tool call across the chunks that carry it.
type toolCallAcc struct {
	id   string
	name string
	args strings.Builder
}

// openAIChatStream is the streaming adapter for /v1/chat/completions clients.
type openAIChatStream struct {
	streamTurnState

	proto agentProtocol
	owned map[string]bool

	kinds map[int]string       // tool_call index → "mcp" | "client"
	acc   map[int]*toolCallAcc // gateway-owned calls being assembled
	order []int                // call order, as the model emitted it

	turn int
}

func (a *openAIChatStream) StartTurn() {
	a.streamTurnState.reset()
	a.kinds = map[int]string{}
	a.acc = map[int]*toolCallAcc{}
	a.order = nil
}

func (a *openAIChatStream) NextTurn() { a.turn++ }

func (a *openAIChatStream) Handle(ev sse.Event) streamStep {
	data := strings.TrimSpace(ev.Data)
	if data == "" {
		return streamStep{Forward: ev.Raw}
	}
	if data == "[DONE]" {
		if a.sawMCP && !a.client {
			return streamStep{Hold: true, Ended: true}
		}
		return streamStep{Forward: ev.Raw, Ended: true}
	}
	// Once the turn belongs to the client nothing else may be withheld.
	if a.client {
		return streamStep{Forward: ev.Raw}
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Role      string                 `json:"role"`
				ToolCalls []openAIStreamToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return streamStep{Forward: ev.Raw}
	}
	if len(chunk.Choices) == 0 {
		// Usage-only chunk (stream_options.include_usage): it belongs to the
		// turn, so it shares the turn's fate.
		if a.sawMCP {
			return a.hold(ev)
		}
		return streamStep{Forward: ev.Raw}
	}
	choice := chunk.Choices[0]

	if len(choice.Delta.ToolCalls) > 0 {
		mcp, client := a.observeToolCalls(choice.Delta.ToolCalls)
		if client {
			// The client owns this turn: release everything withheld so the
			// client sees a complete, correctly ordered turn.
			a.client = true
			return a.flush(ev)
		}
		if mcp {
			a.sawMCP = true
		}
		return a.hold(ev)
	}
	// The turn ends at [DONE]: everything before it is just traffic that
	// belongs to the turn, and the client must not see a half-open turn.
	if choice.FinishReason != nil && a.sawMCP {
		return a.hold(ev)
	}
	if choice.Delta.Role != "" && a.turn > 0 {
		// The assistant message is already open for the client.
		return streamStep{}
	}
	return streamStep{Forward: ev.Raw}
}

// observeToolCalls records every entry, reporting whether the chunk carries a
// gateway-owned call and/or one the client owns.
func (a *openAIChatStream) observeToolCalls(entries []openAIStreamToolCall) (mcp, client bool) {
	for _, tc := range entries {
		kind, known := a.kinds[tc.Index]
		if !known {
			switch {
			case tc.Function.Name == "":
				// Arguments-only delta for an index whose name we never saw:
				// withhold it rather than leak a partial call to the client.
				kind = "mcp"
			case a.owned[tc.Function.Name]:
				kind = "mcp"
			default:
				kind = "client"
			}
			a.kinds[tc.Index] = kind
			if kind == "mcp" {
				a.acc[tc.Index] = &toolCallAcc{id: tc.ID, name: tc.Function.Name}
				a.order = append(a.order, tc.Index)
			}
		}
		if kind == "mcp" {
			if acc := a.acc[tc.Index]; acc != nil {
				if acc.name == "" {
					acc.name = tc.Function.Name
				}
				if acc.id == "" {
					acc.id = tc.ID
				}
				acc.args.WriteString(tc.Function.Arguments)
			}
			mcp = true
			continue
		}
		client = true
	}
	return mcp, client
}

func (a *openAIChatStream) Calls() []agentToolCall {
	out := make([]agentToolCall, 0, len(a.order))
	for _, idx := range a.order {
		acc := a.acc[idx]
		if acc == nil || acc.name == "" {
			continue
		}
		out = append(out, agentToolCall{ID: acc.id, Name: acc.name, Args: acc.args.String()})
	}
	return out
}

func (a *openAIChatStream) AppendTurn(prevBody []byte, results []agentToolResult) ([]byte, error) {
	calls := a.Calls()
	toolCalls := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		toolCalls = append(toolCalls, map[string]any{
			"id":   c.ID,
			"type": "function",
			"function": map[string]any{
				"name":      c.Name,
				"arguments": c.Args,
			},
		})
	}
	// Reuse the buffered adapter: the assistant turn it rebuilds from a
	// response body is exactly the turn the stream just produced.
	synth, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role":       "assistant",
				"content":    nil,
				"tool_calls": toolCalls,
			},
		}},
	})
	if err != nil {
		return nil, err
	}
	return a.proto.AppendTurn(prevBody, synth, results)
}

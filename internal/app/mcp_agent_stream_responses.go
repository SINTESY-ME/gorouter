package app

import (
	"encoding/json"
	"strings"

	"github.com/jhon/gorouter/internal/infra/sse"
)

// responsesStream is the streaming adapter for /v1/responses clients (Codex).
// A continuation turn is folded into the response the client already sees:
// its opening events are dropped, item indices and sequence numbers are shifted
// so they keep growing, and the response id stays the one from response.created.
type responsesStream struct {
	streamTurnState

	proto agentProtocol
	owned map[string]bool

	responseID string
	mcpItems   map[string]bool // item id → gateway-owned function_call

	outputs []json.RawMessage // this turn's items, replayed to the upstream
	visible []json.RawMessage // every item the client has seen

	turn     int
	seqShift int
	outShift int
	events   int // events seen in this turn
}

func newResponsesStream(owned map[string]bool) *responsesStream {
	return &responsesStream{
		proto:    responsesAgent{},
		owned:    owned,
		mcpItems: map[string]bool{},
	}
}

func (a *responsesStream) StartTurn() {
	a.streamTurnState.reset()
	a.mcpItems = map[string]bool{}
	a.outputs = nil
	a.events = 0
}

func (a *responsesStream) NextTurn() {
	a.turn++
	// Keep the client's numbering moving forward. The base is what the client
	// has actually seen, not what the upstream produced: a withheld tool call
	// must not consume an index, or the client's output items end up with a
	// hole where the hidden call used to be.
	a.outShift = len(a.visible)
	a.seqShift += a.events
}

func (a *responsesStream) Handle(ev sse.Event) streamStep {
	a.events++
	switch ev.Name {
	case "response.created":
		if a.turn == 0 {
			a.responseID = responseIDOf(ev.Data)
			return streamStep{Forward: ev.Raw}
		}
		// The client already has a response open.
		return streamStep{}
	case "response.in_progress":
		if a.turn == 0 {
			return streamStep{Forward: ev.Raw}
		}
		return streamStep{}
	case "response.output_item.added":
		item := itemOf(ev.Data)
		if item.Type == "function_call" {
			if item.Name == "" || a.owned[item.Name] {
				a.mcpItems[item.ID] = true
				a.sawMCP = true
				return a.hold(ev)
			}
			if !a.client {
				// The client owns this call: release the turn and forward
				// everything from here on untouched.
				a.client = true
				return a.flush(ev)
			}
		}
		return streamStep{Forward: a.rewrite(ev, nil)}
	case "response.output_item.done":
		item := itemOf(ev.Data)
		if len(item.Raw) > 0 {
			a.outputs = append(a.outputs, item.Raw)
		}
		if item.Type == "function_call" && !a.client && (a.mcpItems[item.ID] || item.Name == "" || a.owned[item.Name]) {
			a.sawMCP = true
			if item.Name != "" {
				a.calls = append(a.calls, agentToolCall{ID: item.CallID, Name: item.Name, Args: item.Arguments})
			}
			return a.hold(ev)
		}
		if len(item.Raw) > 0 {
			a.visible = append(a.visible, item.Raw)
		}
		return streamStep{Forward: a.rewrite(ev, nil)}
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		if a.mcpItems[eventItemID(ev.Data)] && !a.client {
			return a.hold(ev)
		}
		return streamStep{Forward: a.rewrite(ev, nil)}
	case "response.completed", "response.incomplete", "response.failed":
		if a.sawMCP && !a.client {
			// The loop may continue: hold the terminator so the client sees a
			// single, still-open response.
			return streamStep{Hold: true, Ended: true}
		}
		return streamStep{Forward: a.rewrite(ev, a.completedOutput()), Ended: true}
	default:
		return streamStep{Forward: a.rewrite(ev, nil)}
	}
}

// completedOutput rewrites response.completed so its output lists every item
// the client saw, not just the items of the last turn.
func (a *responsesStream) completedOutput() func(map[string]any) {
	return func(obj map[string]any) {
		resp, ok := obj["response"].(map[string]any)
		if !ok || len(a.visible) == 0 {
			return
		}
		items := make([]any, 0, len(a.visible))
		for _, raw := range a.visible {
			var item any
			if json.Unmarshal(raw, &item) == nil {
				items = append(items, item)
			}
		}
		resp["output"] = items
	}
}

// rewrite shifts the numbering of a continuation turn's events onto the
// response the client already sees. The first turn passes through verbatim.
func (a *responsesStream) rewrite(ev sse.Event, extra func(map[string]any)) []byte {
	if a.turn == 0 {
		return ev.Raw
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &obj); err != nil {
		return ev.Raw
	}
	if a.responseID != "" {
		if resp, ok := obj["response"].(map[string]any); ok {
			resp["id"] = a.responseID
		}
	}
	if seq, ok := obj["sequence_number"].(float64); ok {
		obj["sequence_number"] = float64(a.seqShift) + seq
	}
	if idx, ok := obj["output_index"].(float64); ok {
		obj["output_index"] = float64(a.outShift) + idx
	}
	if extra != nil {
		extra(obj)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return ev.Raw
	}
	return sse.BuildEvent(ev.Name, out)
}

func (a *responsesStream) AppendTurn(prevBody []byte, results []agentToolResult) ([]byte, error) {
	// Reuse the buffered adapter: the turn is exactly what those items would
	// have been in a non-streaming response.
	synth := map[string]any{"output": a.outputs}
	body, err := json.Marshal(synth)
	if err != nil {
		return nil, err
	}
	return a.proto.AppendTurn(prevBody, body, results)
}

// responsesItem is the subset of an output item the loop needs.
type responsesItem struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Arguments string          `json:"arguments"`
	Raw       json.RawMessage `json:"-"`
}

func itemOf(data string) responsesItem {
	var ev struct {
		Item json.RawMessage `json:"item"`
	}
	var item responsesItem
	if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) != nil || len(ev.Item) == 0 {
		return item
	}
	if json.Unmarshal(ev.Item, &item) != nil {
		return responsesItem{}
	}
	item.Raw = ev.Item
	return item
}

func eventItemID(data string) string {
	var ev struct {
		ItemID string `json:"item_id"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) != nil {
		return ""
	}
	return ev.ItemID
}

func responseIDOf(data string) string {
	var ev struct {
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) != nil {
		return ""
	}
	return ev.Response.ID
}

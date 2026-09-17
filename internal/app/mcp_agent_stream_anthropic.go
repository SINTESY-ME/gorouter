package app

import (
	"encoding/json"
	"strings"

	"github.com/jhon/gorouter/internal/infra/sse"
)

// anthropicStreamBlock accumulates one content block of the assistant turn.
type anthropicStreamBlock struct {
	kind string // "tool" (gateway-owned), "text", "other"
	id   string
	name string
	text strings.Builder
	args strings.Builder
}

// anthropicStream is the streaming adapter for /v1/messages clients. Blocks are
// addressed by an index that restarts every turn, so a continuation turn is
// renumbered onto the client's already-open message.
type anthropicStream struct {
	streamTurnState

	proto agentProtocol
	owned map[string]bool

	blocks map[int]*anthropicStreamBlock
	order  []int

	turn    int
	base    int // first block index the client has not seen yet
	maxSeen int
}

func newAnthropicStream(owned map[string]bool) *anthropicStream {
	return &anthropicStream{
		proto:  anthropicAgent{},
		owned:  owned,
		blocks: map[int]*anthropicStreamBlock{},
	}
}

func (a *anthropicStream) StartTurn() {
	a.streamTurnState.reset()
	a.blocks = map[int]*anthropicStreamBlock{}
	a.order = nil
}

func (a *anthropicStream) NextTurn() {
	a.turn++
	// Continue the client's block numbering past every block it has seen,
	// including the ones the loop withheld.
	a.base = a.maxSeen + 1
}

func (a *anthropicStream) Handle(ev sse.Event) streamStep {
	// Once the turn belongs to the client nothing else may be withheld.
	if a.client {
		return a.passthrough(ev, ev.Name == "message_delta" || ev.Name == "message_stop")
	}
	switch ev.Name {
	case "message_start":
		if a.turn > 0 {
			// The client's message is already open; a second message_start
			// would restart it.
			return streamStep{}
		}
		return streamStep{Forward: ev.Raw}
	case "ping", "":
		return streamStep{Forward: ev.Raw}
	case "content_block_start":
		var p struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
			return streamStep{Forward: ev.Raw}
		}
		if p.ContentBlock.Type == "tool_use" {
			if !a.owned[p.ContentBlock.Name] {
				// The client owns this call: release the turn so far, then
				// forward everything from here on untouched.
				a.client = true
				return a.flush(ev)
			}
			block := &anthropicStreamBlock{kind: "tool", id: p.ContentBlock.ID, name: p.ContentBlock.Name}
			a.blocks[p.Index] = block
			a.order = append(a.order, p.Index)
			a.sawMCP = true
			return a.hold(ev)
		}
		block := &anthropicStreamBlock{kind: "text"}
		if p.ContentBlock.Type != "text" {
			block.kind = "other"
		}
		block.text.WriteString(p.ContentBlock.Text)
		a.blocks[p.Index] = block
		a.order = append(a.order, p.Index)
		return streamStep{Forward: a.reindex(ev, p.Index)}
	case "content_block_delta":
		var p struct {
			Index int `json:"index"`
			Delta struct {
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
			return streamStep{Forward: ev.Raw}
		}
		block := a.blocks[p.Index]
		if block == nil {
			return streamStep{Forward: a.reindex(ev, p.Index)}
		}
		if block.kind == "tool" {
			block.args.WriteString(p.Delta.PartialJSON)
			return a.hold(ev)
		}
		block.text.WriteString(p.Delta.Text)
		return streamStep{Forward: a.reindex(ev, p.Index)}
	case "content_block_stop":
		var p struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &p); err != nil {
			return streamStep{Forward: ev.Raw}
		}
		if block := a.blocks[p.Index]; block != nil && block.kind == "tool" {
			return a.hold(ev)
		}
		return streamStep{Forward: a.reindex(ev, p.Index)}
	case "message_delta":
		// Carries stop_reason and usage, but the turn ends at message_stop:
		// stopping here would leave the client's message unterminated.
		if a.sawMCP {
			return a.hold(ev)
		}
		return streamStep{Forward: ev.Raw}
	case "message_stop":
		if a.sawMCP {
			// The turn continues: the client must not see the message close.
			return streamStep{Hold: true, Ended: true}
		}
		return streamStep{Forward: ev.Raw, Ended: true}
	default:
		return streamStep{Forward: ev.Raw}
	}
}

// passthrough forwards an event of a client-owned turn, still renumbering the
// block index on continuation turns.
func (a *anthropicStream) passthrough(ev sse.Event, ended bool) streamStep {
	var p struct {
		Index *int `json:"index"`
	}
	if json.Unmarshal([]byte(ev.Data), &p) == nil && p.Index != nil {
		return streamStep{Forward: a.reindex(ev, *p.Index), Ended: ended}
	}
	return streamStep{Forward: ev.Raw, Ended: ended}
}

// reindex rewrites the block index of an event when the loop had to renumber
// the client's blocks (continuation turns). The first turn passes through
// byte-for-byte.
func (a *anthropicStream) reindex(ev sse.Event, index int) []byte {
	if a.base == 0 {
		a.remember(index)
		return ev.Raw
	}
	newIndex := a.base + index
	var obj map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &obj); err != nil {
		return ev.Raw
	}
	obj["index"] = newIndex
	out, err := json.Marshal(obj)
	if err != nil {
		return ev.Raw
	}
	a.remember(newIndex)
	return sse.BuildEvent(ev.Name, out)
}

func (a *anthropicStream) remember(index int) {
	if index > a.maxSeen {
		a.maxSeen = index
	}
}

func (a *anthropicStream) Calls() []agentToolCall {
	out := make([]agentToolCall, 0, len(a.order))
	for _, idx := range a.order {
		block := a.blocks[idx]
		if block == nil || block.kind != "tool" || block.name == "" {
			continue
		}
		args := block.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out = append(out, agentToolCall{ID: block.id, Name: block.name, Args: args})
	}
	return out
}

func (a *anthropicStream) AppendTurn(prevBody []byte, results []agentToolResult) ([]byte, error) {
	content := make([]map[string]any, 0, len(a.order))
	for _, idx := range a.order {
		block := a.blocks[idx]
		if block == nil {
			continue
		}
		switch block.kind {
		case "tool":
			var input any = map[string]any{}
			if raw := block.args.String(); strings.TrimSpace(raw) != "" {
				if err := json.Unmarshal([]byte(raw), &input); err != nil {
					input = map[string]any{}
				}
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    block.id,
				"name":  block.name,
				"input": input,
			})
		case "text":
			if block.text.Len() > 0 {
				content = append(content, map[string]any{"type": "text", "text": block.text.String()})
			}
		}
	}
	synth, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return nil, err
	}
	return a.proto.AppendTurn(prevBody, synth, results)
}

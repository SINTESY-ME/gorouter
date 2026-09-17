package app

import (
	"bufio"
	"bytes"
	"context"
	"io"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/sse"
)

// streamStep is how the loop treats one upstream event.
type streamStep struct {
	// Forward is written to the client immediately (nil = nothing to write).
	Forward []byte
	// Hold means the event was withheld: either it belongs to a tool call the
	// gateway owns, or it closes a turn the loop may still continue.
	Hold bool
	// Ended marks the end of the assistant turn.
	Ended bool
}

// agentStreamAdapter classifies the events of an assistant turn for the
// streaming loop. One instance serves a whole client stream: StartTurn resets
// the per-turn state, NextTurn carries the cross-turn state (whether the
// opening events were already sent, block numbering, ids).
type agentStreamAdapter interface {
	// StartTurn begins a new assistant turn.
	StartTurn()
	// Handle classifies one event from the upstream.
	Handle(ev sse.Event) streamStep
	// Held returns the events withheld in this turn, in order, so the caller
	// can flush them when the loop stops mid-turn.
	Held() [][]byte
	// SawMCPCall reports whether the turn asked for a tool the gateway owns.
	SawMCPCall() bool
	// ClientCall reports whether the turn also asked for a tool the client
	// owns (the loop must then stop and let the client finish the turn).
	ClientCall() bool
	// Calls returns the gateway-owned tool calls of the turn.
	Calls() []agentToolCall
	// AppendTurn builds the next request body with the results appended.
	AppendTurn(prevBody []byte, results []agentToolResult) ([]byte, error)
	// NextTurn starts a continuation turn inside the same client stream.
	NextTurn()
}

// streamTurnState holds what every adapter accumulates per turn.
type streamTurnState struct {
	held   [][]byte
	calls  []agentToolCall
	sawMCP bool
	client bool
}

func (s *streamTurnState) reset() {
	s.held = nil
	s.calls = nil
	s.sawMCP = false
	s.client = false
}

func (s *streamTurnState) hold(ev sse.Event) streamStep {
	s.held = append(s.held, ev.Raw)
	return streamStep{Hold: true}
}

// flush releases every withheld event, followed by ev when forwardNow is set.
// Used the moment the turn turns out to belong to the client: from then on the
// client must see a complete, correctly ordered turn.
func (s *streamTurnState) flush(ev sse.Event) streamStep {
	out := bytes.Join(s.held, nil)
	s.held = nil
	if len(ev.Raw) > 0 {
		out = append(out, ev.Raw...)
	}
	if len(out) == 0 {
		return streamStep{}
	}
	return streamStep{Forward: out}
}

func (s *streamTurnState) ownCall(call agentToolCall) {
	s.sawMCP = true
	s.calls = append(s.calls, call)
}

// Held, SawMCPCall, ClientCall and Calls satisfy the read side of the adapter
// interface for every format.
func (s *streamTurnState) Held() [][]byte         { return s.held }
func (s *streamTurnState) SawMCPCall() bool       { return s.sawMCP }
func (s *streamTurnState) ClientCall() bool       { return s.client }
func (s *streamTurnState) Calls() []agentToolCall { return s.calls }

// newAgentStreamAdapter builds the streaming adapter for a client format.
func newAgentStreamAdapter(f domain.Format, owned map[string]bool) agentStreamAdapter {
	switch f {
	case domain.FormatOpenAI:
		return &openAIChatStream{proto: openAIChatAgent{}, owned: owned, kinds: map[int]string{}, acc: map[int]*toolCallAcc{}}
	case domain.FormatResponses:
		return newResponsesStream(owned)
	case domain.FormatAnthropic:
		return newAnthropicStream(owned)
	}
	return nil
}

// runAgentStreamLoop wraps the already-dispatched first turn in a pipe and
// drives the tool loop behind it. The client's HTTP response is opened with
// the first turn's status (always 200 for a stream), so the loop can keep
// dispatching without the client noticing more than one call.
func (s *RouterService) runAgentStreamLoop(ctx context.Context, first *RouterResponse, modelStr string, body []byte, apiKey string, opts RouteOptions, requestID string, owned map[string]bool) *RouterResponse {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		s.driveAgentStream(ctx, pw, first, modelStr, body, apiKey, opts, requestID, owned)
	}()
	return &RouterResponse{
		StatusCode: first.StatusCode,
		Headers:    first.Headers,
		Stream:     true,
		Body:       pr,
	}
}

// driveAgentStream consumes upstream traffic and writes what the client may
// see. A turn that asks only for gateway tools is swallowed whole (the client
// never learns the tool ran); everything else is forwarded, and a turn that
// asks for a client tool is released in full so the client can finish it.
func (s *RouterService) driveAgentStream(ctx context.Context, w io.Writer, first *RouterResponse, modelStr string, body []byte, apiKey string, opts RouteOptions, requestID string, owned map[string]bool) {
	ad := newAgentStreamAdapter(opts.InputFormat, owned)
	if ad == nil {
		forwardResponse(w, first)
		return
	}
	current := body
	res := first
	for depth := 0; depth <= maxAgentDepth; depth++ {
		if res == nil || !res.Stream || res.StatusCode >= 400 || res.Body == nil {
			forwardResponse(w, res)
			return
		}
		ad.StartTurn()
		readTurn(res.Body, ad, w)
		res.Body.Close()

		if !ad.SawMCPCall() || ad.ClientCall() {
			// Nothing withheld (the client owns the turn), or the turn mixes
			// gateway and client tools: hand back everything withheld so the
			// client receives a complete, terminated turn.
			writeHeld(w, ad.Held())
			return
		}
		next, err := ad.AppendTurn(current, s.executeAgentTools(ctx, ad.Calls()))
		if err != nil || depth == maxAgentDepth {
			// Cannot continue: the withheld events are the only thing that
			// terminates the turn for the client.
			writeHeld(w, ad.Held())
			return
		}
		current = next
		ad.NextTurn()
		if ctx.Err() != nil {
			return
		}
		nres, nerr := s.routeChatDispatch(ctx, modelStr, current, true, apiKey, opts, requestID)
		if nerr != nil {
			return
		}
		res = nres
	}
}

// readTurn forwards (or withholds) events until the upstream turn ends.
func readTurn(body io.Reader, ad agentStreamAdapter, w io.Writer) {
	br := bufio.NewReader(body)
	for {
		ev, err := sse.ReadEvent(br)
		if len(ev.Raw) > 0 {
			step := ad.Handle(ev)
			if len(step.Forward) > 0 {
				if _, werr := w.Write(step.Forward); werr != nil {
					return
				}
			}
			if step.Ended {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func forwardResponse(w io.Writer, res *RouterResponse) {
	if res == nil {
		return
	}
	if res.Body == nil {
		return
	}
	defer res.Body.Close()
	_, _ = io.Copy(w, res.Body)
}

func writeHeld(w io.Writer, held [][]byte) {
	for _, ev := range held {
		if _, err := w.Write(ev); err != nil {
			return
		}
	}
}

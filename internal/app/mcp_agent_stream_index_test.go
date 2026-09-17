package app

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jhon/gorouter/internal/domain"
)

// A withheld tool call must not consume an output index: the client's items
// have to come out contiguous, matching the output array of the completed
// event (built from exactly what the client saw).
func TestResponsesStreamContiguousOutputIndex(t *testing.T) {
	ad := newAgentStreamAdapter(domain.FormatResponses, map[string]bool{"lojateste__consultar_preco": true})

	first := runTurn(t, ad, strings.Join([]string{
		`event: response.created` + "\n" + `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1"}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_1","type":"message"}}` + "\n\n",
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"item_id":"msg_1","delta":"deixa eu ver"}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"deixa eu ver"}]}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":4,"output_index":1,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1"}}` + "\n\n",
		`event: response.function_call_arguments.delta` + "\n" + `data: {"type":"response.function_call_arguments.delta","sequence_number":5,"output_index":1,"item_id":"fc_1","delta":"{\"sku\":\"ABC-123\"}"}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":6,"output_index":1,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1","arguments":"{\"sku\":\"ABC-123\"}"}}` + "\n\n",
		`event: response.completed` + "\n" + `data: {"type":"response.completed","sequence_number":7,"response":{"id":"resp_1","output":[]}}` + "\n\n",
	}, ""))

	// The client sees the pre-tool message, never the gateway-owned call.
	assertContains(t, first, "deixa eu ver")
	assertNotContains(t, first, "function_call")
	assertNotContains(t, first, "response.completed")
	if !ad.SawMCPCall() || ad.ClientCall() {
		t.Fatalf("turn must be gateway-owned: mcp=%v client=%v", ad.SawMCPCall(), ad.ClientCall())
	}
	calls := ad.Calls()
	if len(calls) != 1 || calls[0].Name != "lojateste__consultar_preco" || calls[0].ID != "call_1" {
		t.Fatalf("tool call not captured: %+v", calls)
	}

	// Continuation turn: the upstream starts at index 0 again, and the client
	// must see the next index after the single item it already has.
	ad.NextTurn()
	second := runTurn(t, ad, strings.Join([]string{
		`event: response.created` + "\n" + `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_2"}}` + "\n\n",
		// The upstream numbers its own continuation items however it likes:
		// here the answer sits at its index 1. The client must still see it
		// right after the message it already has, at index 1.
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":1,"output_index":1,"item":{"id":"msg_2","type":"message"}}` + "\n\n",
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","sequence_number":2,"output_index":1,"item_id":"msg_2","delta":"4321"}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":3,"output_index":1,"item":{"id":"msg_2","type":"message","content":[{"type":"output_text","text":"4321"}]}}` + "\n\n",
		`event: response.completed` + "\n" + `data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_2","output":[{"id":"msg_2","type":"message","content":[{"type":"output_text","text":"4321"}]}]}}` + "\n\n",
	}, ""))

	assertContains(t, second, "4321")
	// A single continuation turn must not reopen the response the client
	// already has open.
	assertNotContains(t, second, "response.created")

	indices := outputIndices(second)
	if len(indices) == 0 {
		t.Fatal("no output_index reached the client in the continuation turn")
	}
	for _, idx := range indices {
		if idx != 1 {
			t.Fatalf("continuation output_index = %v, want 1 (the hidden call must not leave a hole); stream:\n%s", idx, second)
		}
	}
}

// Upstreams recycle item ids across turns: the continuation reuses the id of
// the message the client already has. Each delivery is a distinct item, so the
// client must get a fresh index instead of a collision.
func TestResponsesStreamRecycledItemID(t *testing.T) {
	ad := newAgentStreamAdapter(domain.FormatResponses, map[string]bool{"lojateste__consultar_preco": true})

	runTurn(t, ad, strings.Join([]string{
		`event: response.created` + "\n" + `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1"}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_1","type":"message"}}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":" "}]}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":3,"output_index":1,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1"}}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":4,"output_index":1,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1","arguments":"{}"}}` + "\n\n",
		`event: response.completed` + "\n" + `data: {"type":"response.completed","sequence_number":5,"response":{"id":"resp_1","output":[]}}` + "\n\n",
	}, ""))

	ad.NextTurn()
	second := runTurn(t, ad, strings.Join([]string{
		`event: response.created` + "\n" + `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_2"}}` + "\n\n",
		// Same id as the first turn's message, new content.
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":1,"output_index":1,"item":{"id":"msg_1","type":"message"}}` + "\n\n",
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","sequence_number":2,"output_index":1,"item_id":"msg_1","delta":" "}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":3,"output_index":1,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":" "}]}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":4,"output_index":2,"item":{"id":"msg_2","type":"message"}}` + "\n\n",
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","sequence_number":5,"output_index":2,"item_id":"msg_2","delta":"4321"}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":6,"output_index":2,"item":{"id":"msg_2","type":"message","content":[{"type":"output_text","text":"4321"}]}}` + "\n\n",
		`event: response.completed` + "\n" + `data: {"type":"response.completed","sequence_number":7,"response":{"id":"resp_2","output":[]}}` + "\n\n",
	}, ""))

	indices := outputIndices(second)
	if len(indices) == 0 {
		t.Fatal("no output_index reached the client")
	}
	for _, idx := range indices {
		switch idx {
		case 1, 2:
		default:
			t.Fatalf("output_index = %v, want 1 or 2 (no reuse, no hole); stream:\n%s", idx, second)
		}
	}
	if first := indices[0]; first != 1 {
		t.Fatalf("first continuation index = %v, want 1", first)
	}
}

// The real shape of a first turn: the upstream puts the message the client
// keeps, then the gateway-owned call, then another message. The hidden call
// must not push the trailing item out of place.
func TestResponsesStreamHiddenCallMidTurn(t *testing.T) {
	ad := newAgentStreamAdapter(domain.FormatResponses, map[string]bool{"lojateste__consultar_preco": true})

	first := runTurn(t, ad, strings.Join([]string{
		`event: response.created` + "\n" + `data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1"}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_1","type":"message"}}` + "\n\n",
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"item_id":"msg_1","delta":"vou ver"}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"vou ver"}]}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":4,"output_index":1,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1"}}` + "\n\n",
		`event: response.function_call_arguments.delta` + "\n" + `data: {"type":"response.function_call_arguments.delta","sequence_number":5,"output_index":1,"item_id":"fc_1","delta":"{}"}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":6,"output_index":1,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1","arguments":"{}"}}` + "\n\n",
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":7,"output_index":2,"item":{"id":"msg_1","type":"message"}}` + "\n\n",
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","sequence_number":8,"output_index":2,"item_id":"msg_1","delta":" "}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":9,"output_index":2,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":" "}]}}` + "\n\n",
		`event: response.completed` + "\n" + `data: {"type":"response.completed","sequence_number":10,"response":{"id":"resp_1","output":[]}}` + "\n\n",
	}, ""))

	assertNotContains(t, first, "function_call")
	assertNotContains(t, first, "response.completed")
	for _, idx := range outputIndices(first) {
		switch idx {
		case 0, 1:
		default:
			t.Fatalf("output_index = %v, want 0 or 1 (the hidden call must not leave a hole); stream:\n%s", idx, first)
		}
	}

	ad.NextTurn()
	second := runTurn(t, ad, strings.Join([]string{
		`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_2","type":"message"}}` + "\n\n",
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"item_id":"msg_2","delta":"4321"}` + "\n\n",
		`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"msg_2","type":"message","content":[{"type":"output_text","text":"4321"}]}}` + "\n\n",
		`event: response.completed` + "\n" + `data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_2","output":[]}}` + "\n\n",
	}, ""))
	for _, idx := range outputIndices(second) {
		if idx != 2 {
			t.Fatalf("final answer output_index = %v, want 2; stream:\n%s", idx, second)
		}
	}
}

// outputIndices collects every output_index the client received, in order.
func outputIndices(stream string) []float64 {
	var out []float64
	for _, line := range strings.Split(stream, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		body := strings.TrimPrefix(line, "data: ")
		if !utf8.ValidString(body) {
			continue
		}
		var obj struct {
			OutputIndex *float64 `json:"output_index"`
		}
		if err := json.Unmarshal([]byte(body), &obj); err != nil {
			continue
		}
		if obj.OutputIndex != nil {
			out = append(out, *obj.OutputIndex)
		}
	}
	return out
}

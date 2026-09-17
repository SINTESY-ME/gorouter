package app

import (
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/sse"
)

// At the depth cap the loop can no longer run the call it withheld: the client
// must still get a terminated turn, but never a tool it does not know.
func TestStreamCapClosesTurnWithoutReleasingGatewayCall(t *testing.T) {
	for name, format := range map[string]domain.Format{
		"openai":    domain.FormatOpenAI,
		"anthropic": domain.FormatAnthropic,
		"responses": domain.FormatResponses,
	} {
		t.Run(name, func(t *testing.T) {
			ad := newAgentStreamAdapter(format, map[string]bool{"lojateste__consultar_preco": true})
			for _, raw := range callAndClose(name) {
				ad.Handle(sse.Event{Name: eventName(raw), Data: eventData(raw), Raw: []byte(raw)})
			}

			held := string(joinHeld(ad.Held()))
			if !strings.Contains(held, "lojateste__consultar_preco") {
				t.Fatalf("%s: the call must stay withheld in this turn; held=\n%s", name, held)
			}
			out := string(joinHeld(ad.Terminate()))
			if strings.Contains(out, "lojateste__consultar_preco") {
				t.Fatalf("%s: the cap released a tool the client does not have:\n%s", name, out)
			}
			if !strings.Contains(out, terminatorOf(name)) {
				t.Fatalf("%s: the turn must still be closed for the client; out=\n%s", name, out)
			}
		})
	}
}

// callAndClose is one gateway-owned call followed by the event that closes the
// turn, in the wire format of each client.
func callAndClose(format string) []string {
	switch format {
	case "openai":
		return []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lojateste__consultar_preco","arguments":"{}"}}]}}]}` + "\n\n",
			`data: [DONE]` + "\n\n",
		}
	case "anthropic":
		return []string{
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"lojateste__consultar_preco"}}` + "\n\n",
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}` + "\n\n",
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n",
		}
	default:
		return []string{
			`event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1"}}` + "\n\n",
			`event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","name":"lojateste__consultar_preco","call_id":"call_1","arguments":"{}"}}` + "\n\n",
			`event: response.completed` + "\n" + `data: {"type":"response.completed","response":{"id":"resp_1","output":[]}}` + "\n\n",
		}
	}
}

func terminatorOf(format string) string {
	switch format {
	case "openai":
		return "[DONE]"
	case "anthropic":
		return "message_stop"
	default:
		return "response.completed"
	}
}

func joinHeld(chunks [][]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func eventName(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			return after
		}
	}
	return ""
}

func eventData(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			return after
		}
	}
	return ""
}

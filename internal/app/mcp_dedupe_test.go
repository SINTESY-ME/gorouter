package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// The two sides name the same upstream tool differently: a harness that speaks
// MCP itself (Claude Code, Hermes) declares "mcp__<server>__<tool>", while the
// gateway exposes "<client>__<tool>". A caller tool that already covers an
// upstream tool must suppress the gateway's copy — the caller's definition
// stays, and the gateway's duplicate schema does not travel with every request.
const (
	harnessDesc = "preco direto no harness"
	gatewayDesc = "preco via gorouter"
)

func dedupeFormats() []domain.Format {
	return []domain.Format{domain.FormatOpenAI, domain.FormatResponses, domain.FormatAnthropic}
}

func formatName(f domain.Format) string {
	switch f {
	case domain.FormatOpenAI:
		return "chat"
	case domain.FormatResponses:
		return "responses"
	default:
		return "anthropic"
	}
}

// callerTools builds the caller's own tool list in the shape of one wire format.
func callerTools(format domain.Format, name, desc string) string {
	switch format {
	case domain.FormatOpenAI:
		return `[{"type":"function","function":{"name":"` + name + `","description":"` + desc + `","parameters":{"type":"object"}}}]`
	case domain.FormatResponses:
		return `[{"type":"function","name":"` + name + `","description":"` + desc + `","parameters":{"type":"object"}}]`
	default: // anthropic
		return `[{"name":"` + name + `","description":"` + desc + `","input_schema":{"type":"object"}}]`
	}
}

func callerBody(format domain.Format, tools string) []byte {
	switch format {
	case domain.FormatResponses:
		return []byte(`{"model":"m","input":[],"tools":` + tools + `}`)
	default:
		return []byte(`{"model":"m","messages":[],"tools":` + tools + `}`)
	}
}

// toolsByName reads the tools back out of a request regardless of the nesting
// the format uses.
func toolsByName(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var req struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	out := map[string]string{}
	for _, tool := range req.Tools {
		inner, ok := tool["function"].(map[string]any)
		if !ok {
			inner = tool
		}
		name, _ := inner["name"].(string)
		desc, _ := inner["description"].(string)
		out[name] = desc
	}
	return out
}

func dedupeService(tools ...domain.MCPTool) *MCPService {
	return &MCPService{Manager: &mockMCPManager{tools: tools}}
}

// The harness's tool name differs from the gateway's by the harness prefix;
// the gateway must not add its own copy of the same upstream tool.
func TestDedupeKeepsTheCallersTool(t *testing.T) {
	for _, format := range dedupeFormats() {
		t.Run(formatName(format), func(t *testing.T) {
			svc := dedupeService(testMCPTool("lojateste__consultar_preco", gatewayDesc))
			body := callerBody(format, callerTools(format, "mcp__lojateste__consultar_preco", harnessDesc))

			out, err := svc.InjectTools(context.Background(), format, body)
			if err != nil {
				t.Fatalf("InjectTools: %v", err)
			}

			got := toolsByName(t, out)
			if len(got) != 1 {
				t.Fatalf("expected only the caller's tool, got %v", got)
			}
			if got["mcp__lojateste__consultar_preco"] != harnessDesc {
				t.Fatalf("the caller's definition did not survive: %v", got)
			}
			// Nothing was injected, so the request must come back untouched.
			if string(out) != string(body) {
				t.Fatalf("a request with nothing to inject was rewritten:\n got %s\nwant %s", out, body)
			}
		})
	}
}

// A caller that happens to use the gateway's own naming is deduped too.
func TestDedupeMatchesTheGatewaysOwnName(t *testing.T) {
	for _, format := range dedupeFormats() {
		t.Run(formatName(format), func(t *testing.T) {
			svc := dedupeService(testMCPTool("lojateste__consultar_preco", gatewayDesc))
			body := callerBody(format, callerTools(format, "lojateste__consultar_preco", harnessDesc))

			out, err := svc.InjectTools(context.Background(), format, body)
			if err != nil {
				t.Fatalf("InjectTools: %v", err)
			}
			got := toolsByName(t, out)
			if len(got) != 1 || got["lojateste__consultar_preco"] != harnessDesc {
				t.Fatalf("expected only the caller's tool, got %v", got)
			}
		})
	}
}

// The server half of the name is spelled by whoever configured it, so the two
// sides routinely disagree on case.
func TestDedupeIgnoresCaseInTheServerName(t *testing.T) {
	svc := dedupeService(testMCPTool("lojateste__consultar_preco", gatewayDesc))
	body := callerBody(domain.FormatOpenAI, callerTools(domain.FormatOpenAI, "mcp__LojaTeste__consultar_preco", harnessDesc))

	out, err := svc.InjectTools(context.Background(), domain.FormatOpenAI, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	got := toolsByName(t, out)
	if len(got) != 1 {
		t.Fatalf("expected the differently cased name to be recognized, got %v", got)
	}
}

// Only the tool the caller already has is suppressed: the rest of the gateway's
// catalogue still reaches the model.
func TestDedupeStillInjectsTheOtherTools(t *testing.T) {
	svc := dedupeService(
		testMCPTool("lojateste__consultar_preco", gatewayDesc),
		testMCPTool("lojateste__listar_pedidos", "pedidos via gorouter"),
	)
	body := callerBody(domain.FormatOpenAI, callerTools(domain.FormatOpenAI, "mcp__lojateste__consultar_preco", harnessDesc))

	out, err := svc.InjectTools(context.Background(), domain.FormatOpenAI, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	got := toolsByName(t, out)
	if len(got) != 2 {
		t.Fatalf("expected the caller's tool plus the unrelated one, got %v", got)
	}
	if got["mcp__lojateste__consultar_preco"] != harnessDesc {
		t.Fatalf("the caller's definition did not survive: %v", got)
	}
	if _, ok := got["lojateste__listar_pedidos"]; !ok {
		t.Fatalf("the gateway's own tool was dropped: %v", got)
	}
}

// An unrelated tool from the caller must not suppress anything.
func TestDedupeKeepsUnrelatedCallerTools(t *testing.T) {
	svc := dedupeService(testMCPTool("lojateste__consultar_preco", gatewayDesc))
	body := callerBody(domain.FormatOpenAI, callerTools(domain.FormatOpenAI, "mcp__outro__coisa", "nada a ver"))

	out, err := svc.InjectTools(context.Background(), domain.FormatOpenAI, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	got := toolsByName(t, out)
	if len(got) != 2 {
		t.Fatalf("expected the caller's tool plus the injected one, got %v", got)
	}
	if got["lojateste__consultar_preco"] != gatewayDesc {
		t.Fatalf("the gateway's tool was not injected: %v", got)
	}
}

// Keeping the caller's declaration also means keeping its execution path: a
// call to the caller's own tool name is not the gateway's to run, so the turn
// goes back to the caller untouched.
func TestDedupeLeavesExecutionWithTheCaller(t *testing.T) {
	svc := dedupeService(testMCPTool("lojateste__consultar_preco", gatewayDesc))
	owned := svc.OwnedTools(context.Background(), nil)
	if !owned["lojateste__consultar_preco"] {
		t.Fatal("the gateway does not own the tool it exposes")
	}
	if owned["mcp__lojateste__consultar_preco"] {
		t.Fatal("the caller's own tool name was claimed by the gateway")
	}
}

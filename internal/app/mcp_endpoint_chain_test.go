package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/translator"
)

// Injecting a tool is only half the path: the injected body still has to cross
// the request translator before it reaches the upstream. A tool that the
// injector writes but the translator drops leaves the upstream model with no
// tool at all — exactly the failure mode reported by Anthropic clients. These
// tests pin the whole chain per client format.
func TestMCPToolSurvivesTranslationForEveryFormat(t *testing.T) {
	tool := domain.MCPTool{
		Name:        "gh__create_issue",
		Description: "creates a GitHub issue",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"title": map[string]any{"type": "string"}},
			"required":   []any{"title"},
		},
	}
	svc := &MCPService{Manager: &mockMCPManager{tools: []domain.MCPTool{tool}}}
	tr := translator.New()
	ctx := context.Background()

	cases := []struct {
		name        string
		format      domain.Format
		body        string
		upstreamIn  domain.Format
		expectTools bool
	}{
		{
			name:        "openai chat",
			format:      domain.FormatOpenAI,
			body:        `{"model":"coding","messages":[{"role":"user","content":"hi"}]}`,
			upstreamIn:  domain.FormatOpenAI,
			expectTools: true,
		},
		{
			name:        "anthropic messages",
			format:      domain.FormatAnthropic,
			body:        `{"model":"coding","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`,
			upstreamIn:  domain.FormatAnthropic,
			expectTools: true,
		},
		{
			name:        "openai responses",
			format:      domain.FormatResponses,
			body:        `{"model":"coding","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			upstreamIn:  domain.FormatResponses,
			expectTools: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			injected, err := svc.InjectTools(ctx, tc.format, []byte(tc.body))
			if err != nil {
				t.Fatalf("InjectTools: %v", err)
			}
			if !containsName(injected, tool.Name) {
				t.Fatalf("injection did not add the tool to the %s body: %s", tc.name, injected)
			}

			// Cross the same translator the router uses (pivot into OpenAI).
			out := injected
			if tc.upstreamIn != domain.FormatOpenAI {
				out, err = tr.TranslateRequest(tc.upstreamIn, domain.FormatOpenAI, "upstream-model", injected)
				if err != nil {
					t.Fatalf("TranslateRequest(%s->openai): %v", tc.upstreamIn, err)
				}
			}
			var upstream struct {
				Tools []struct {
					Type     string `json:"type"`
					Function struct {
						Name        string         `json:"name"`
						Description string         `json:"description"`
						Parameters  map[string]any `json:"parameters"`
					} `json:"function"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(out, &upstream); err != nil {
				t.Fatalf("upstream body is not valid JSON: %v (%s)", err, out)
			}
			if len(upstream.Tools) != 1 {
				t.Fatalf("upstream received %d tools, want 1 — the injected tool did not survive the %s->openai translation: %s",
					len(upstream.Tools), tc.upstreamIn, out)
			}
			got := upstream.Tools[0]
			if got.Type != "function" || got.Function.Name != tool.Name {
				t.Errorf("upstream tool = %+v, want function %q", got, tool.Name)
			}
			if got.Function.Parameters["type"] != "object" {
				t.Errorf("upstream tool parameters = %#v, want the input schema as parameters", got.Function.Parameters)
			}
			if _, ok := got.Function.Parameters["properties"]; !ok {
				t.Errorf("upstream tool parameters lost properties: %#v", got.Function.Parameters)
			}
			if got.Function.Description != tool.Description {
				t.Errorf("upstream tool description = %q, want %q (the model picks tools by description)",
					got.Function.Description, tool.Description)
			}
		})
	}
}

func containsName(body []byte, name string) bool {
	var s string
	_ = json.Unmarshal(body, &s)
	return len(body) > 0 && (json.Valid(body) && indexOf(body, name) >= 0)
}

func indexOf(body []byte, needle string) int {
	for i := 0; i+len(needle) <= len(body); i++ {
		if string(body[i:i+len(needle)]) == needle {
			return i
		}
	}
	return -1
}

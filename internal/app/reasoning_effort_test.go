package app

import (
	"encoding/json"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestAdaptReasoningEffortForComboCandidate(t *testing.T) {
	tests := []struct {
		name string
		caps domain.ReasoningCapabilities
		want string
	}{
		{
			name: "max is still reduced when model advertises native max",
			caps: domain.ReasoningCapabilities{SupportsReasoning: true, SupportsXHighReasoningEffort: true, SupportsMaxReasoningEffort: true},
			want: "xhigh",
		},
		{
			name: "max degrades to xhigh",
			caps: domain.ReasoningCapabilities{SupportsReasoning: true, SupportsXHighReasoningEffort: true},
			want: "xhigh",
		},
		{
			name: "max degrades to high",
			caps: domain.ReasoningCapabilities{SupportsReasoning: true},
			want: "high",
		},
		{
			name: "unsupported removes effort",
			caps: domain.ReasoningCapabilities{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptReasoningEffort("max", tt.caps); got != tt.want {
				t.Fatalf("adaptReasoningEffort(max) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeReasoningBodyForCandidate(t *testing.T) {
	body := []byte(`{"model":"combo","messages":[],"reasoning_effort":"max"}`)
	got, err := normalizeReasoningBodyForModel(body, domain.ReasoningCapabilities{SupportsReasoning: true, SupportsXHighReasoningEffort: true})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(got, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["reasoning_effort"] != "xhigh" {
		t.Fatalf("reasoning_effort = %v, want xhigh", wire["reasoning_effort"])
	}

	got, err = normalizeReasoningBodyForModel(body, domain.ReasoningCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(body) {
		t.Fatal("unsupported candidate kept reasoning_effort")
	}
	wire = nil
	if json.Unmarshal(got, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["reasoning_effort"]; ok {
		t.Fatalf("unsupported candidate sent reasoning_effort: %s", got)
	}
}

func TestNormalizeReasoningObjectForCandidate(t *testing.T) {
	body := []byte(`{"model":"combo","reasoning":{"effort":"max","summary":"auto"}}`)
	got, err := normalizeReasoningBodyForModel(body, domain.ReasoningCapabilities{SupportsReasoning: true})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(got, &wire); err != nil {
		t.Fatal(err)
	}
	reasoning, ok := wire["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning object missing: %s", got)
	}
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning object = %#v, want effort=high and summary=auto", reasoning)
	}
}

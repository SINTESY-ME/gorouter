package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// The metadata chain: the provider's own /models answer is the first link and
// wins for every field it states; only what it leaves out is looked up in the
// external registries, in order (LiteLLM, models.dev, OpenRouter).

// staticTransport answers the three chain URLs from canned bodies, so the real
// loaders and the real merge run without the network.
type staticTransport map[string]string

func (t staticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := ""
	for needle, b := range t {
		if strings.Contains(req.URL.Host+req.URL.Path, needle) {
			body = b
			break
		}
	}
	status := http.StatusOK
	if body == "" {
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func fakeRegistry(t *testing.T, litellm, modelsDev, openRouter string) *ModelRegistry {
	t.Helper()
	r := NewModelRegistry()
	r.client = &http.Client{
		Timeout: 5 * time.Second,
		Transport: staticTransport{
			"/model_prices_and_context_window.json": litellm,
			"/api.json":                             modelsDev,
			"/api/v1/models":                        openRouter,
		},
	}
	return r
}

// TestProviderMetadataReadsTheShapesProvidersActuallySend covers the four
// response shapes found on the live providers, plus a provider that sends no
// metadata at all (which must stay unknown, not become a zero-level fact).
func TestProviderMetadataReadsTheShapesProvidersActuallySend(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want domain.ModelMetadata
	}{
		{
			// e.g. command: the window is a top-level number.
			name: "flat context_length",
			raw:  map[string]any{"id": "command-r", "context_length": float64(128000)},
			want: domain.ModelMetadata{Context: 128000},
		},
		{
			// e.g. byte: limits nested under token_limits, vision under modalities.
			name: "token_limits plus modalities",
			raw: map[string]any{
				"id": "ep-20250101",
				"token_limits": map[string]any{
					"context_window":             float64(98304),
					"max_input_token_length":     float64(65536),
					"max_output_token_length":    float64(16384),
					"max_reasoning_token_length": float64(32768),
				},
				"modalities": map[string]any{"input_modalities": []any{"text", "image"}},
			},
			want: domain.ModelMetadata{
				Context:           98304,
				MaxOutputTokens:   16384,
				SupportsVision:    true,
				SupportsReasoning: true, // only reachable via max_reasoning_token_length
			},
		},
		{
			// e.g. deepinfra: limits inside a metadata object.
			name: "nested metadata",
			raw: map[string]any{
				"id": "meta-llama/Meta-Llama-3.1-8B-Instruct",
				"metadata": map[string]any{
					"context_length": float64(131072),
					"max_tokens":     float64(4096),
				},
			},
			want: domain.ModelMetadata{Context: 131072, MaxOutputTokens: 4096},
		},
		{
			// e.g. openrouter: ceiling under top_provider, capabilities as flags.
			name: "top_provider and supported_parameters",
			raw: map[string]any{
				"id":                   "openai/gpt-4o",
				"context_length":       float64(128000),
				"top_provider":         map[string]any{"max_completion_tokens": float64(16384), "context_length": float64(128000)},
				"supported_parameters": []any{"tools", "tool_choice", "reasoning"},
				"architecture":         map[string]any{"modality": "text+image->text"},
			},
			want: domain.ModelMetadata{
				Context:           128000,
				MaxOutputTokens:   16384,
				SupportsVision:    true,
				SupportsToolCall:  true,
				SupportsReasoning: true,
			},
		},
		{
			// e.g. ollama: only identity fields — nothing is known, and the
			// chain must be the one to answer.
			name: "no metadata at all",
			raw:  map[string]any{"id": "llama3.2", "object": "model", "owned_by": "library"},
			want: domain.ModelMetadata{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := providerModelMetadata(tc.raw)
			if got != tc.want {
				t.Fatalf("providerModelMetadata() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestChainFillsOnlyWhatTheProviderLeftOut is the chain rule itself: a field the
// provider stated is kept, a field it did not state comes from the chain.
func TestChainFillsOnlyWhatTheProviderLeftOut(t *testing.T) {
	provider := domain.ModelMetadata{Context: 128000} // provider knows the window only
	chain := domain.ModelMetadata{
		Context:           8192, // must NOT win: the provider is authoritative
		MaxOutputTokens:   4096,
		SupportsVision:    true,
		SupportsToolCall:  true,
		SupportsReasoning: true,
	}

	got := fillMissingMetadata(provider, chain)

	if got.Context != 128000 {
		t.Errorf("provider's context was overwritten: got %d, want 128000", got.Context)
	}
	if got.MaxOutputTokens != 4096 {
		t.Errorf("missing ceiling was not filled from the chain: got %d, want 4096", got.MaxOutputTokens)
	}
	if !got.SupportsVision || !got.SupportsToolCall || !got.SupportsReasoning {
		t.Errorf("missing capabilities were not filled from the chain: %+v", got)
	}
}

// TestModelMetadataWithNoRegistryKeepsProviderFacts guards the degraded path: no
// registry must not blank out what the provider said.
func TestModelMetadataWithNoRegistryKeepsProviderFacts(t *testing.T) {
	s := &ModelSyncService{}
	m := domain.ModelInfo{ID: "x", Metadata: domain.ModelMetadata{Context: 200000, MaxOutputTokens: 8000}}

	if got := s.modelMetadata("command", m); got != m.Metadata {
		t.Fatalf("modelMetadata() = %+v, want the provider facts %+v", got, m.Metadata)
	}
}

// TestChainLoadKeepsEverySourceLimits is the regression test for the merge
// defect: OpenRouter loads last and used to replace the whole entry, so the
// window LiteLLM and models.dev had already provided was lost for hundreds of
// models (the live DB showed 678 models priced by OpenRouter with only 4
// carrying a context).
func TestChainLoadKeepsEverySourceLimits(t *testing.T) {
	litellm := `{
		"gpt-4o": {
			"mode": "chat",
			"max_input_tokens": 128000,
			"max_output_tokens": 16384,
			"supports_vision": true,
			"supports_function_calling": true,
			"litellm_provider": "openai",
			"input_cost_per_token": 0.0000025
		},
		"litellm-only-model": {
			"mode": "chat",
			"max_input_tokens": 32000,
			"max_output_tokens": 4096
		}
	}`
	modelsDev := `{
		"openai": {"models": {"gpt-4o": {"tool_call": true, "limit": {"context": 128000, "output": 16384}}}}
	}`
	// OpenRouter knows gpt-4o with pricing only (no limits of its own) and a
	// second model whose limits only it knows.
	openRouter := `{
		"data": [
			{"id": "openai/gpt-4o", "pricing": {"prompt": "0.0000025", "completion": "0.00001"}},
			{"id": "or/only-here", "context_length": 64000,
			 "top_provider": {"max_completion_tokens": 8192},
			 "pricing": {"prompt": "0.000001", "completion": "0.000002"}}
		]
	}`

	r := fakeRegistry(t, litellm, modelsDev, openRouter)
	if !r.ensureLoaded() {
		t.Fatal("chain did not load from the canned bodies")
	}

	// The window and the capabilities survived the later source, and the later
	// source still won the pricing (its own preference rule).
	got := r.ResolveMetadata("gpt-4o")
	if got.Context != 128000 {
		t.Errorf("context erased by a later source: got %d, want 128000", got.Context)
	}
	if got.MaxOutputTokens != 16384 {
		t.Errorf("output ceiling erased by a later source: got %d, want 16384", got.MaxOutputTokens)
	}
	if !got.SupportsVision || !got.SupportsToolCall {
		t.Errorf("capabilities erased by a later source: %+v", got)
	}
	if p, ok := r.ResolvePricing("openai", "gpt-4o"); !ok || p.Source != "openrouter" {
		t.Errorf("pricing preference changed: %+v ok=%v", p, ok)
	}

	// A source that is the only one to know a model still contributes it.
	if got := r.ResolveMetadata("or/only-here"); got.Context != 64000 || got.MaxOutputTokens != 8192 {
		t.Errorf("openrouter-only model lost its limits: %+v", got)
	}
	if got := r.ResolveMetadata("litellm-only-model"); got.Context != 32000 || got.MaxOutputTokens != 4096 {
		t.Errorf("litellm-only model lost its limits: %+v", got)
	}
}

// TestSourceBuildersReportLimits pins each link's own parsing, so a source that
// silently stops reading its limits is caught here instead of in production.
func TestSourceBuildersReportLimits(t *testing.T) {
	ll := liteLLMEntry(map[string]any{
		"max_input_tokens":  float64(200000),
		"max_output_tokens": float64(64000),
	}, domain.KindLLM)
	if ll.Context != 200000 || ll.MaxOutputTokens != 64000 {
		t.Errorf("liteLLMEntry limits = (%d, %d), want (200000, 64000)", ll.Context, ll.MaxOutputTokens)
	}

	md := modelsDevEntry(map[string]any{"limit": map[string]any{"context": float64(1000000), "output": float64(32000)}})
	if md.Context != 1000000 || md.MaxOutputTokens != 32000 {
		t.Errorf("modelsDevEntry limits = (%d, %d), want (1000000, 32000)", md.Context, md.MaxOutputTokens)
	}

	or := openRouterEntry(map[string]any{
		"context_length": float64(128000),
		"top_provider":   map[string]any{"max_completion_tokens": float64(16384)},
	}, domain.KindLLM, false)
	if or.Context != 128000 || or.MaxOutputTokens != 16384 {
		t.Errorf("openRouterEntry limits = (%d, %d), want (128000, 16384)", or.Context, or.MaxOutputTokens)
	}
}

// TestModelInfoWireFormatKeepsThePublicContract pins the /v1/models shape. The
// metadata a provider reports is internal bookkeeping: if the field ever loses
// its `json:"-"` tag, every harness reading the model list starts seeing keys
// it was not promised, so the leak has to fail here rather than in the wild.
func TestModelInfoWireFormatKeepsThePublicContract(t *testing.T) {
	info := domain.ModelInfo{
		ID:      "command/command-r",
		Object:  "model",
		OwnedBy: "command",
		Kind:    domain.KindLLM,
		Metadata: domain.ModelMetadata{
			Context:           128000,
			MaxOutputTokens:   4096,
			SupportsVision:    true,
			SupportsToolCall:  true,
			SupportsReasoning: true,
		},
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]bool{"id": true, "object": true, "owned_by": true, "kind": true}
	for k := range got {
		if !want[k] {
			t.Errorf("model list leaks an extra key %q: %s", k, raw)
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("model list lost the promised key %q: %s", k, raw)
		}
	}
}

// TestResolveKind prefers the provider's endpoint knowledge, then the chain, and
// names as a last resort.
func TestResolveKindPrefersProviderEndpoint(t *testing.T) {
	s := &ModelSyncService{}

	// A provider that reports a non-LLM endpoint is endpoint-specific knowledge.
	if got := s.resolveKind(domain.ModelInfo{ID: "whisper-1", Kind: domain.KindSTT}); got != domain.KindSTT {
		t.Errorf("provider kind ignored: got %q, want %q", got, domain.KindSTT)
	}
	// No registry, no provider kind: the name heuristic answers.
	if got := s.resolveKind(domain.ModelInfo{ID: "text-embedding-3-small"}); got != domain.KindEmbedding {
		t.Errorf("heuristic fallback = %q, want %q", got, domain.KindEmbedding)
	}
	// An explicit LLM kind from the provider is kept when the chain is absent.
	if got := s.resolveKind(domain.ModelInfo{ID: "gpt-4o", Kind: domain.KindLLM}); got != domain.KindLLM {
		t.Errorf("provider LLM kind = %q, want %q", got, domain.KindLLM)
	}
}

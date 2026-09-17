package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// The effort ladder is the reasoning half of the metadata chain. These tests
// pin where a ladder comes from, which link wins, and what routing does with
// it — including the upgrade that used to happen silently.

// TestLadderParsersPerSource covers the shape each link actually publishes.
func TestLadderParsersPerSource(t *testing.T) {
	t.Run("litellm booleans and levels", func(t *testing.T) {
		// A list plus the explicit bool extremes, all from this source.
		caps := reasoningCapabilitiesFromMap(map[string]any{
			"supports_reasoning":                true,
			"supports_minimal_reasoning_effort": true,
			"reasoning_effort_levels":           []any{"low", "high", "max"},
		})
		if want := []string{"minimal", "low", "high", "max"}; !reflect.DeepEqual(caps.Efforts, want) {
			t.Errorf("Efforts = %v, want %v", caps.Efforts, want)
		}
		if !caps.EffortsStated {
			t.Error("a listed ladder must count as stated")
		}
	})

	t.Run("litellm states reasoning cannot be disabled", func(t *testing.T) {
		// supports_none_reasoning_effort: false on a reasoning model is a
		// mandatory claim, not silence.
		caps := reasoningCapabilitiesFromMap(map[string]any{
			"supports_reasoning":             true,
			"supports_none_reasoning_effort": false,
		})
		if !caps.Mandatory {
			t.Error("Mandatory = false, want true")
		}
	})

	t.Run("litellm without a list infers from flags", func(t *testing.T) {
		caps := reasoningCapabilitiesFromMap(map[string]any{"supports_reasoning": true})
		if want := []string{"none", "medium", "high"}; !reflect.DeepEqual(caps.Efforts, want) {
			t.Errorf("Efforts = %v, want %v", caps.Efforts, want)
		}
		if caps.EffortsStated {
			t.Error("inferred levels must not claim to be stated")
		}
	})

	t.Run("models.dev reasoning_options", func(t *testing.T) {
		e := modelsDevEntry(map[string]any{
			"id":        "google/gemini-3.5-flash-thinking",
			"reasoning": true,
			"reasoning_options": []any{
				map[string]any{"type": "toggle"},
				map[string]any{"type": "budget_tokens"},
				map[string]any{"type": "effort", "values": []any{"minimal", "low", "medium", "high"}},
			},
		})
		if want := []string{"minimal", "low", "medium", "high"}; !reflect.DeepEqual(e.Reasoning.Efforts, want) {
			t.Errorf("Efforts = %v, want %v (only the effort option carries levels)", e.Reasoning.Efforts, want)
		}
		if !e.SupportsReasoning {
			t.Error("SupportsReasoning = false, want true")
		}
	})

	t.Run("openrouter ladder with default and mandatory", func(t *testing.T) {
		e := openRouterEntry(map[string]any{
			"id":                   "~deepseek/deepseek-pro-latest",
			"supported_parameters": []any{"include_reasoning", "reasoning", "reasoning_effort"},
			"reasoning": map[string]any{
				"mandatory":         false,
				"supported_efforts": []any{"max", "high", "low"},
				"default_effort":    "high",
			},
		}, domain.KindLLM, false)
		if want := []string{"low", "high", "max"}; !reflect.DeepEqual(e.Reasoning.Efforts, want) {
			t.Errorf("Efforts = %v, want %v in canonical order", e.Reasoning.Efforts, want)
		}
		if e.Reasoning.DefaultEffort != "high" {
			t.Errorf("DefaultEffort = %q, want high", e.Reasoning.DefaultEffort)
		}
		if e.Reasoning.Mandatory {
			t.Error("Mandatory = true, want false")
		}
		if !e.Reasoning.EffortsStated {
			t.Error("EffortsStated = false, want true for a published ladder")
		}
	})

	t.Run("provider may state the ladder itself", func(t *testing.T) {
		meta := providerModelMetadata(map[string]any{
			"id": "anthropic/claude-fable-5",
			"reasoning": map[string]any{
				"mandatory":         true,
				"supported_efforts": []any{"low", "medium", "high", "xhigh", "max"},
				"default_effort":    "medium",
			},
		})
		if want := []string{"low", "medium", "high", "xhigh", "max"}; !reflect.DeepEqual(meta.Reasoning.Efforts, want) {
			t.Errorf("Efforts = %v, want %v", meta.Reasoning.Efforts, want)
		}
		if !meta.Reasoning.Mandatory || meta.Reasoning.DefaultEffort != "medium" {
			t.Errorf("mandatory/default = %v/%q, want true/medium", meta.Reasoning.Mandatory, meta.Reasoning.DefaultEffort)
		}
	})
}

// TestStatedLadderOutranksInferred is the gap-filling rule applied to efforts:
// a link that lists levels beats a link that only published flags, whichever
// comes first, and two listed ladders keep the earlier link's.
func TestStatedLadderOutranksInferred(t *testing.T) {
	inferred := domain.ReasoningCapabilities{SupportsReasoning: true, Efforts: []string{"none", "medium", "high"}}
	stated := domain.ReasoningCapabilities{EffortsStated: true, Efforts: []string{"low", "medium", "high"}}

	got := mergeReasoningCapabilities(inferred, stated)
	if want := []string{"low", "medium", "high"}; !reflect.DeepEqual(got.Efforts, want) {
		t.Errorf("inferred then stated: Efforts = %v, want the stated %v", got.Efforts, want)
	}
	got = mergeReasoningCapabilities(stated, inferred)
	if want := []string{"low", "medium", "high"}; !reflect.DeepEqual(got.Efforts, want) {
		t.Errorf("stated then inferred: Efforts = %v, want the stated %v", got.Efforts, want)
	}

	earlier := domain.ReasoningCapabilities{
		EffortsStated:              true,
		Efforts:                    []string{"low", "medium", "high"},
		SupportsLowReasoningEffort: true,
		SupportsReasoning:          true,
	}
	later := domain.ReasoningCapabilities{
		EffortsStated:              true,
		Efforts:                    []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
		SupportsMaxReasoningEffort: true,
		Mandatory:                  true,
	}
	got = mergeReasoningCapabilities(earlier, later)
	if want := []string{"low", "medium", "high"}; !reflect.DeepEqual(got.Efforts, want) {
		t.Errorf("two stated ladders: Efforts = %v, want the earlier link's %v (no union)", got.Efforts, want)
	}
	if !got.SupportsMaxReasoningEffort || !got.SupportsLowReasoningEffort {
		t.Errorf("flags must union even when ladders differ: %+v", got)
	}
}

// TestAdaptReasoningEffortNeverUpgrades is the fix for the request that came
// back as four levels stronger than asked. low and minimal used to fall
// through to "high" whenever a model merely declared reasoning support.
func TestAdaptReasoningEffortNeverUpgrades(t *testing.T) {
	flagsOnly := domain.ReasoningCapabilities{SupportsReasoning: true}
	explicit := domain.ReasoningCapabilities{EffortsStated: true, Efforts: []string{"minimal", "low", "medium", "high"}}
	stopsAtLow := domain.ReasoningCapabilities{EffortsStated: true, Efforts: []string{"minimal", "low"}}

	cases := []struct {
		name      string
		requested string
		caps      domain.ReasoningCapabilities
		want      string
	}{
		{"low on a model that only claims reasoning is omitted, not upgraded", "low", flagsOnly, ""},
		{"minimal on a model that only claims reasoning is omitted", "minimal", flagsOnly, ""},
		{"low on a ladder that has low is honoured", "low", explicit, "low"},
		{"minimal on a ladder that has minimal is honoured", "minimal", explicit, "minimal"},
		{"medium on a ladder that stops at low descends to low", "medium", stopsAtLow, "low"},
		{"high on a ladder that stops at low descends to low", "high", stopsAtLow, "low"},
		{"max stays reduced to xhigh when the model advertises it", "max", domain.ReasoningCapabilities{SupportsReasoning: true, SupportsXHighReasoningEffort: true, SupportsMaxReasoningEffort: true}, "xhigh"},
		{"max on a stated ladder descends below max", "max", domain.ReasoningCapabilities{EffortsStated: true, Efforts: []string{"none", "low", "high", "max"}}, "high"},
		{"none is honoured when the ladder can be disabled", "none", domain.ReasoningCapabilities{EffortsStated: true, Efforts: []string{"none", "low", "high"}}, "none"},
		{"none is omitted when the ladder cannot be disabled", "none", explicit, ""},
		{"unknown level is omitted", "turbo", flagsOnly, ""},
		{"unknown model gets nothing", "high", domain.ReasoningCapabilities{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := adaptReasoningEffort(tc.requested, tc.caps); got != tc.want {
				t.Errorf("adaptReasoningEffort(%q) = %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}

// TestSyncKeepsReasoningWhenNoLinkStatesIt: a provider whose /models says
// nothing about reasoning must not erase what the catalog already knew.
func TestSyncKeepsReasoningWhenNoLinkStatesIt(t *testing.T) {
	s := &ModelSyncService{}
	prev := &domain.ModelEntry{
		SupportsReasoning:          true,
		SupportsLowReasoningEffort: true,
		SupportedReasoningEfforts:  []string{"low", "medium", "high"},
		DefaultReasoningEffort:     "medium",
	}
	caps := s.reasoningCapabilities(domain.ModelMetadata{}, "mystery-model-7b", prev)
	if want := []string{"low", "medium", "high"}; !reflect.DeepEqual(caps.Efforts, want) {
		t.Errorf("Efforts = %v, want the catalog's %v", caps.Efforts, want)
	}
	if !caps.SupportsLowReasoningEffort || caps.DefaultEffort != "medium" {
		t.Errorf("preserved caps = %+v, want the previous low/default", caps)
	}

	// And when a link does state something, it is the statement that counts.
	stated := domain.ModelMetadata{Reasoning: domain.ReasoningCapabilities{
		EffortsStated:     true,
		Efforts:           []string{"low", "high"},
		SupportsReasoning: true,
	}}
	caps = s.reasoningCapabilities(stated, "mystery-model-7b", prev)
	if want := []string{"low", "high"}; !reflect.DeepEqual(caps.Efforts, want) {
		t.Errorf("Efforts = %v, want the stated %v", caps.Efforts, want)
	}
}

func comboEntry(id string, e domain.ModelEntry) domain.ModelEntry {
	e.ID = id
	parts := 0
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			parts = i
			break
		}
	}
	e.ProviderID, e.ModelID, e.IsActive = id[:parts], id[parts+1:], true
	return e
}

// TestAggregateComboCapabilities pins what a combo claims: the union of what
// its members can do, with coverage counts so a partial aggregate is not read
// as a complete one.
func TestAggregateComboCapabilities(t *testing.T) {
	entries := []domain.ModelEntry{
		comboEntry("p/fast", domain.ModelEntry{
			Context: 128000, MaxOutputTokens: 16384, SupportsVision: true, SupportsToolCall: true,
			SupportsReasoning: true, SupportedReasoningEfforts: []string{"none", "medium", "high"},
		}),
		comboEntry("p/deep", domain.ModelEntry{
			Context: 1000000, MaxOutputTokens: 65536,
			SupportsReasoning: true, SupportedReasoningEfforts: []string{"low", "high", "max"},
			DefaultReasoningEffort: "high", ReasoningMandatory: true,
		}),
	}
	got := AggregateComboCapabilities(entries, 3)

	if got.Context != 1000000 || got.MaxOutputTokens != 65536 {
		t.Errorf("limits = %d/%d, want the largest member values 1000000/65536", got.Context, got.MaxOutputTokens)
	}
	if !got.SupportsVision || !got.SupportsToolCall {
		t.Errorf("flags = vision:%v tool_call:%v, want both true (one member each)", got.SupportsVision, got.SupportsToolCall)
	}
	if want := []string{"none", "low", "medium", "high", "max"}; !reflect.DeepEqual(got.Reasoning.Efforts, want) {
		t.Errorf("Efforts = %v, want the union %v", got.Reasoning.Efforts, want)
	}
	if got.Reasoning.DefaultEffort != "high" {
		t.Errorf("DefaultEffort = %q, want high (the only member that declared one)", got.Reasoning.DefaultEffort)
	}
	if got.Reasoning.Mandatory {
		t.Error("Mandatory = true, want false: only one member states it")
	}
	if got.Members != 3 || got.MembersKnown != 2 {
		t.Errorf("coverage = %d/%d, want 3 members with 2 known", got.Members, got.MembersKnown)
	}
}

// TestAggregateComboCapabilitiesMandatoryNeedsEveryMember: one member that can
// disable reasoning makes disabling false for the combo.
func TestAggregateComboCapabilitiesMandatoryNeedsEveryMember(t *testing.T) {
	both := []domain.ModelEntry{
		comboEntry("p/a", domain.ModelEntry{SupportsReasoning: true, ReasoningMandatory: true}),
		comboEntry("p/b", domain.ModelEntry{SupportsReasoning: true, ReasoningMandatory: true}),
	}
	if got := AggregateComboCapabilities(both, 2); !got.Reasoning.Mandatory {
		t.Error("Mandatory = false, want true when every member states it")
	}
	both[1].ReasoningMandatory = false
	if got := AggregateComboCapabilities(both, 2); got.Reasoning.Mandatory {
		t.Error("Mandatory = true, want false when a member can disable reasoning")
	}
	if got := AggregateComboCapabilities(nil, 0); got.Reasoning.Mandatory {
		t.Error("Mandatory = true for an empty combo")
	}
}

// TestAggregateComboCapabilitiesDefaultNeedsAgreement: members defaulting to
// different levels is not a fact about the combo, so none is reported.
func TestAggregateComboCapabilitiesDefaultNeedsAgreement(t *testing.T) {
	entries := []domain.ModelEntry{
		comboEntry("p/a", domain.ModelEntry{DefaultReasoningEffort: "high"}),
		comboEntry("p/b", domain.ModelEntry{DefaultReasoningEffort: "low"}),
	}
	if got := AggregateComboCapabilities(entries, 2); got.Reasoning.DefaultEffort != "" {
		t.Errorf("DefaultEffort = %q, want empty when members disagree", got.Reasoning.DefaultEffort)
	}
}

// TestComboCapabilitiesFollowsNestedCombos: a member that is another combo
// contributes its own members, exactly as the router recurses into it.
func TestComboCapabilitiesFollowsNestedCombos(t *testing.T) {
	models := &fakeModelRepo{entries: []domain.ModelEntry{
		comboEntry("p/fast", domain.ModelEntry{Context: 128000, SupportsReasoning: true, SupportedReasoningEfforts: []string{"none", "high"}}),
		comboEntry("p/deep", domain.ModelEntry{Context: 400000, SupportsReasoning: true, SupportedReasoningEfforts: []string{"low", "high", "max"}}),
	}}
	combos := &fakeComboRepo{combos: []domain.Combo{
		{ID: "1", Name: "inner", Models: []string{"p/deep"}},
		{ID: "2", Name: "outer", Models: []string{"p/fast", "inner", "p/ghost"}},
	}}
	svc := &ComboService{Repo: combos, Models: models}

	got := svc.Capabilities(context.Background(), combos.combos[1])
	if got.Members != 3 || got.MembersKnown != 2 {
		t.Errorf("coverage = %d/%d, want 3 members with 2 known (ghost is neither a model nor a combo)", got.Members, got.MembersKnown)
	}
	if got.Context != 400000 {
		t.Errorf("Context = %d, want 400000 from the nested combo's member", got.Context)
	}
	if want := []string{"none", "low", "high", "max"}; !reflect.DeepEqual(got.Reasoning.Efforts, want) {
		t.Errorf("Efforts = %v, want the union %v", got.Reasoning.Efforts, want)
	}
}

// TestSingleRungIsNotTheLadder pins the defect behind the `article` combo
// exposing ["minimal","xhigh"]: LiteLLM's supports_*_reasoning_effort booleans
// are statements about single rungs, not about the whole ladder. Treating a
// boolean as the ladder let the first link that published any flag freeze it
// and keep a later link's complete ladder out of the chain.
func TestSingleRungIsNotTheLadder(t *testing.T) {
	caps := reasoningCapabilitiesFromMap(map[string]any{
		"supports_reasoning":                true,
		"supports_minimal_reasoning_effort": true,
		"supports_xhigh_reasoning_effort":   true,
	})
	if caps.EffortsStated {
		t.Error("a boolean is one rung, not a stated ladder — it must not claim the ladder")
	}
	if want := []string{"none", "minimal", "medium", "high", "xhigh"}; !reflect.DeepEqual(caps.Efforts, want) {
		t.Errorf("Efforts = %v, want the contiguous ladder %v", caps.Efforts, want)
	}
}

// TestDeclaredLadderBeatsAnEarlierLinksFlags is the regression for the live
// `article` combo: the chain merges LiteLLM first and OpenRouter last, and the
// richer ladder must win on evidence rather than on arrival order.
func TestDeclaredLadderBeatsAnEarlierLinksFlags(t *testing.T) {
	litellm := reasoningCapabilitiesFromMap(map[string]any{
		"supports_reasoning":                true,
		"supports_minimal_reasoning_effort": true,
		"supports_xhigh_reasoning_effort":   true,
	})
	openrouter := openRouterEntry(map[string]any{
		"id": "meta/muse-spark-1.2",
		"reasoning": map[string]any{
			"mandatory":         true,
			"supported_efforts": []any{"xhigh", "high", "medium", "low", "minimal"},
			"default_effort":    "medium",
		},
	}, domain.KindLLM, false).Reasoning

	got := mergeReasoningCapabilities(litellm, openrouter)
	if want := []string{"minimal", "low", "medium", "high", "xhigh"}; !reflect.DeepEqual(got.Efforts, want) {
		t.Errorf("Efforts = %v, want the declared ladder %v", got.Efforts, want)
	}
	if !got.EffortsStated {
		t.Error("the surviving ladder came from a list and must count as stated")
	}
	if got.DefaultEffort != "medium" || !got.Mandatory {
		t.Errorf("DefaultEffort/Mandatory = %q/%v, want medium/true from the same source", got.DefaultEffort, got.Mandatory)
	}
	// A request for high is now served at high instead of dropping to minimal.
	if adapted := adaptReasoningEffort("high", got); adapted != "high" {
		t.Errorf("adaptReasoningEffort(high) = %q, want high", adapted)
	}
}

// TestMandatoryRemovesTheDisableSwitch guards the other half: a source that
// says reasoning cannot be turned off must not have "none" invented for it by
// the flag-derived ladder, and a request to disable it is omitted, not sent.
func TestMandatoryRemovesTheDisableSwitch(t *testing.T) {
	caps := reasoningCapabilitiesFromMap(map[string]any{
		"supports_reasoning":             true,
		"supports_none_reasoning_effort": false,
	})
	if !caps.Mandatory {
		t.Fatal("Mandatory = false, want true")
	}
	for _, l := range caps.Efforts {
		if l == "none" {
			t.Fatalf("Efforts = %v, want no disable switch on a mandatory model", caps.Efforts)
		}
	}
	if got := adaptReasoningEffort("none", caps); got != "" {
		t.Errorf("adaptReasoningEffort(none) = %q, want \"\" (omit, do not disable)", got)
	}
	// The ladder of a flags-only mandatory model is medium..: a request below it
	// is omitted rather than upgraded, and one inside it is honoured.
	if got := adaptReasoningEffort("low", caps); got != "" {
		t.Errorf("adaptReasoningEffort(low) = %q, want \"\" (nothing at or below low)", got)
	}
	if got := adaptReasoningEffort("high", caps); got != "high" {
		t.Errorf("adaptReasoningEffort(high) = %q, want high", got)
	}
}

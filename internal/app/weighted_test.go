package app

import (
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestOrderWeighted_ReordersChosenFirst(t *testing.T) {
	combo := &domain.Combo{
		Models:    []string{"a/model-a", "b/model-b", "c/model-c"},
		ModelMeta: map[string]domain.ComboModelMeta{"a/model-a": {Weight: 1}},
	}
	// Every draw must be a permutation with the chosen model first and the
	// others in configured order.
	for i := 0; i < 200; i++ {
		got := orderWeighted(combo)
		seen := map[string]int{}
		for _, m := range got {
			seen[m]++
		}
		if len(seen) != 3 {
			t.Fatalf("orderWeighted returned a non-permutation: %v", got)
		}
		// The chosen (first) model must be one of the combo models.
		if got[0] != "a/model-a" && got[0] != "b/model-b" && got[0] != "c/model-c" {
			t.Fatalf("unexpected first model %q", got[0])
		}
		// The remaining models keep their relative configured order.
		var rest []string
		for _, m := range got[1:] {
			rest = append(rest, m)
		}
		if got[0] == "a/model-a" {
			if !equalSeq(t, rest, []string{"b/model-b", "c/model-c"}) {
				t.Fatalf("expected [b, c] fallback, got %v", rest)
			}
		}
	}
}

func TestOrderWeighted_HeavyWeightDominates(t *testing.T) {
	combo := &domain.Combo{
		Models: []string{"a/model-a", "b/model-b", "c/model-c"},
		ModelMeta: map[string]domain.ComboModelMeta{
			"a/model-a": {Weight: 90},
			"b/model-b": {Weight: 5},
			"c/model-c": {Weight: 5},
		},
	}
	// Model A should be chosen first in the vast majority of draws.
	first := map[string]int{}
	const n = 3000
	for i := 0; i < n; i++ {
		first[orderWeighted(combo)[0]]++
	}
	if first["a/model-a"] < n*85/100 {
		t.Fatalf("expected model A first >= 85%%, got %d/%d", first["a/model-a"], n)
	}
}

func TestOrderWeighted_MissingWeights_Equal(t *testing.T) {
	// No model_meta at all: every model defaults to weight 1 -> roughly
	// uniform. With a huge N the first-model distribution should stay within
	// reasonable bounds for an equal draw.
	combo := &domain.Combo{Models: []string{"a/model-a", "b/model-b", "c/model-c"}}
	first := map[string]int{}
	const n = 6000
	for i := 0; i < n; i++ {
		first[orderWeighted(combo)[0]]++
	}
	for _, m := range combo.Models {
		if first[m] < n/3-n/10 || first[m] > n/3+n/10 {
			t.Fatalf("expected ~1/3 for %s, got %d/%d", m, first[m], n)
		}
	}
}

func TestOrderWeighted_SingleModel(t *testing.T) {
	combo := &domain.Combo{Models: []string{"a/model-a"}}
	if got := orderWeighted(combo); len(got) != 1 || got[0] != "a/model-a" {
		t.Fatalf("single model should be returned unchanged, got %v", got)
	}
}

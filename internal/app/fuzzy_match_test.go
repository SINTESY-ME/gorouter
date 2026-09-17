package app

import (
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestTryStripSafeSuffixes(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"deepseek-v4-flash-free", "deepseek-v4-flash", true},
		{"gpt-4o-latest", "gpt-4o", true},
		{"claude-3-preview", "claude-3", true},
		{"model-alpha", "model", true},
		{"model-beta", "model", true},
		{"gpt-4o-turbo", "", false},
		{"gpt-4o-mini", "", false},
		{"gpt-4o", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, ok := tryStripSafeSuffixes(tt.input)
			if ok != tt.ok {
				t.Errorf("tryStripSafeSuffixes(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if ok && result != tt.expected {
				t.Errorf("tryStripSafeSuffixes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFindLongestContainment(t *testing.T) {
	pricing := domain.ModelPricing{InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6}
	entries := map[string]registryEntry{
		"glm-5.2":           {Pricing: pricing},
		"deepseek-v4-flash": {Pricing: pricing},
		"minimax-m3":        {Pricing: pricing},
		"no-pricing":        {Pricing: domain.ModelPricing{}},
		"ab":                {},
	}

	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"0g-glm-5.2", "glm-5.2", true},
		{"deepseek-v4-flash-free", "deepseek-v4-flash", true},
		// Self-match must be skipped; minimax-m3 is in entries but we
		// should NOT return it as a containment of itself.
		{"minimax-m3", "", false},
		// Entries without pricing must be skipped.
		{"no-pricing-extended", "", false},
		{"unknown-model", "", false},
		{"glm", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, ok := findLongestContainment(tt.input, entries)
			if ok != tt.ok {
				t.Errorf("findLongestContainment(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if result != tt.expected {
				t.Errorf("findLongestContainment(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"glm-5.2", "0g-glm-5.2", 3},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			result := levenshteinDistance(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestFindBestFuzzyMatch(t *testing.T) {
	pricing := domain.ModelPricing{
		InputCostPerToken:  1e-6,
		OutputCostPerToken: 2e-6,
	}
	entries := map[string]registryEntry{
		"glm-5.2":           {Pricing: pricing},
		"deepseek-v4-flash": {Pricing: pricing},
		"minimax-m3":        {Pricing: pricing},
		// Entry without pricing — must never be returned.
		"deepseek-v4-flash-free": {Pricing: domain.ModelPricing{}},
	}

	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"0g-glm-5.2", "glm-5.2", true},
		{"deepseek-v4-flash-free", "deepseek-v4-flash", true},
		{"minimax-m3", "", false}, // self-match, no fuzzy needed
		{"unknown-model", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, ok := findBestFuzzyMatch(tt.input, entries)
			if ok != tt.ok {
				t.Errorf("findBestFuzzyMatch(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if ok && result.Pricing.InputCostPerToken != pricing.InputCostPerToken {
				t.Errorf("findBestFuzzyMatch(%q) returned wrong pricing", tt.input)
			}
			if !ok && tt.expected != "" {
				t.Errorf("findBestFuzzyMatch(%q) unexpectedly failed", tt.input)
			}
		})
	}
}

// TestFindBestFuzzyMatchNoPricingEntry verifies that an entry without pricing
// is never returned, even when it's the only fuzzy match available.
func TestFindBestFuzzyMatchNoPricingEntry(t *testing.T) {
	entries := map[string]registryEntry{
		"model-free": {Pricing: domain.ModelPricing{}},
	}

	result, ok := findBestFuzzyMatch("model-free", entries)
	if ok {
		t.Errorf("findBestFuzzyMatch should not return entry without pricing, got ok=true pricing=%+v", result.Pricing)
	}
}

func TestLevenshteinThreshold(t *testing.T) {
	tests := []struct {
		modelLen int
		expected int
	}{
		{0, minLevenLen},
		{6, minLevenLen},
		{18, 3},
		{24, 4},
		{30, 5},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := levenshteinThreshold(tt.modelLen)
			if result != tt.expected {
				t.Errorf("levenshteinThreshold(%d) = %d, want %d", tt.modelLen, result, tt.expected)
			}
		})
	}
}
package app

import (
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestCalculateCostChat(t *testing.T) {
	p := domain.ModelPricing{
		InputCostPerToken:  2.5e-6,  // $2.50/1M
		OutputCostPerToken: 1e-5,     // $10.00/1M
	}
	cost := CalculateCost(p, "chat/completions", 1000, 500, 0, 0)
	// 1000 * 2.5e-6 + 500 * 1e-5 = 0.0025 + 0.005 = 0.0075
	if !approxEqual(cost, 0.0075) {
		t.Fatalf("expected 0.0075, got %f", cost)
	}
}

func TestCalculateCostEmbeddings(t *testing.T) {
	p := domain.ModelPricing{
		InputCostPerToken: 1.3e-7, // $0.13/1M
	}
	cost := CalculateCost(p, "embeddings", 10000, 0, 0, 0)
	// 10000 * 1.3e-7 = 0.0013
	if !approxEqual(cost, 0.0013) {
		t.Fatalf("expected 0.0013, got %f", cost)
	}
}

func TestCalculateCostCacheTokens(t *testing.T) {
	p := domain.ModelPricing{
		InputCostPerToken:           3e-6,
		OutputCostPerToken:          1.5e-5,
		CacheReadInputTokenCost:     3e-7,
		CacheCreationInputTokenCost: 3.75e-6,
	}
	// 1000 prompt * 3e-6 + 500 completion * 1.5e-5 + 200 cacheRead * 3e-7 + 100 cacheCreation * 3.75e-6
	// = 0.003 + 0.0075 + 0.00006 + 0.000375 = 0.010935
	cost := CalculateCost(p, "chat/completions", 1000, 500, 200, 100)
	if !approxEqual(cost, 0.010935) {
		t.Fatalf("expected 0.010935, got %f", cost)
	}
}

func TestCalculateCostTiered(t *testing.T) {
	p := domain.ModelPricing{
		InputCostPerToken:          2.5e-6,
		OutputCostPerToken:         1e-5,
		InputCostPerTokenAbove200k: 5e-6,
	}
	// prompt > 200k → use above-200k price for ALL tokens (simplified)
	cost := CalculateCost(p, "chat/completions", 250000, 1000, 0, 0)
	// 250000 * 5e-6 + 1000 * 1e-5 = 1.25 + 0.01 = 1.26
	if !approxEqual(cost, 1.26) {
		t.Fatalf("expected 1.26, got %f", cost)
	}
}

func TestCalculateCostNoPricing(t *testing.T) {
	cost := CalculateCost(domain.ModelPricing{}, "chat/completions", 1000, 500, 0, 0)
	if cost != 0 {
		t.Fatalf("expected 0, got %f", cost)
	}
}

func TestHasPricing(t *testing.T) {
	if HasPricing(domain.ModelPricing{}) {
		t.Fatal("empty pricing should return false")
	}
	if !HasPricing(domain.ModelPricing{InputCostPerToken: 1e-6}) {
		t.Fatal("non-zero input cost should return true")
	}
	if !HasPricing(domain.ModelPricing{OutputCostPerImage: 0.04}) {
		t.Fatal("non-zero image cost should return true")
	}
}

func approxEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}
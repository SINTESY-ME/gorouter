package app

import "github.com/jhon/gorouter/internal/domain"

// CalculateCost computes the dollar cost of a request based on the model's
// pricing data, the endpoint, and token counts. For chat/completions it uses
// per-token input/output prices plus cache read/creation if available.
// For embeddings it uses input cost only. For other endpoints (images, audio,
// rerank) the corresponding per-unit fields are used when available, but
// v1 only captures token counts from the response body so those endpoints
// return 0 until dimension capture is added.
//
// Tiered pricing: if prompt tokens exceed a threshold and a tiered price
// exists, the tiered price is used for ALL tokens (simplified — a split-exact
// calculation can be added in a future version).
func CalculateCost(pricing domain.ModelPricing, endpoint string, prompt, completion, cacheRead, cacheCreation int) float64 {
	if endpoint == "" {
		endpoint = "chat/completions"
	}

	switch endpoint {
	case "chat/completions", "completions":
		inputPrice := pricing.InputCostPerToken
		outputPrice := pricing.OutputCostPerToken
		// Tiered: if prompt > 200k and above-200k price exists, use it.
		if prompt > 200000 && pricing.InputCostPerTokenAbove200k > 0 {
			inputPrice = pricing.InputCostPerTokenAbove200k
		} else if prompt > 128000 && pricing.InputCostPerTokenAbove128k > 0 {
			inputPrice = pricing.InputCostPerTokenAbove128k
		}
		if completion > 200000 && pricing.OutputCostPerTokenAbove200k > 0 {
			outputPrice = pricing.OutputCostPerTokenAbove200k
		} else if completion > 128000 && pricing.OutputCostPerTokenAbove128k > 0 {
			outputPrice = pricing.OutputCostPerTokenAbove128k
		}
		cost := float64(prompt)*inputPrice + float64(completion)*outputPrice
		// Cache discounts (Anthropic/OpenAI prompt caching nativo)
		if cacheRead > 0 && pricing.CacheReadInputTokenCost > 0 {
			cost += float64(cacheRead) * pricing.CacheReadInputTokenCost
		}
		if cacheCreation > 0 && pricing.CacheCreationInputTokenCost > 0 {
			cost += float64(cacheCreation) * pricing.CacheCreationInputTokenCost
		}
		return cost

	case "embeddings":
		return float64(prompt) * pricing.InputCostPerToken

	case "images/generations":
		// v1: per-image cost requires capturing `n` from the request body.
		// Using OutputCostPerImage when available; 0 if not set.
		// TODO: capture n from request body for accurate per-image cost.
		return pricing.OutputCostPerImage

	case "audio/speech":
		// v1: per-character cost requires capturing character count.
		// TODO: capture character count from request body.
		return 0

	case "audio/transcriptions":
		// v1: per-second cost requires capturing audio duration.
		// TODO: capture duration from response.
		return 0

	case "rerank":
		// v1: per-query cost requires capturing query count.
		return pricing.InputCostPerQuery

	default:
		return 0
	}
}

// HasPricing reports whether the pricing struct has any non-zero cost field.
// Used to filter fuzzy-match candidates (only paid models are valid candidates).
func HasPricing(p domain.ModelPricing) bool {
	return p.InputCostPerToken > 0 ||
		p.OutputCostPerToken > 0 ||
		p.InputCostPerTokenBatches > 0 ||
		p.OutputCostPerTokenBatches > 0 ||
		p.CacheReadInputTokenCost > 0 ||
		p.CacheCreationInputTokenCost > 0 ||
		p.OutputCostPerImage > 0 ||
		p.InputCostPerPixel > 0 ||
		p.InputCostPerSecond > 0 ||
		p.OutputCostPerSecond > 0 ||
		p.InputCostPerCharacter > 0 ||
		p.OutputCostPerCharacter > 0 ||
		p.InputCostPerQuery > 0
}

// HasPricingData reports whether the pricing struct has any data at all
// (even a free model with cost=0 has pricing data when Source is set).
// Used for exact matches and cache population — free models ($0) are
// included so the dashboard can display "$0" instead of "no price".
func HasPricingData(p domain.ModelPricing) bool {
	return p.Source != ""
}
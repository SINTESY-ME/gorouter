package translator

import (
	"encoding/json"
	"fmt"
	"strings"
)

// These budgets mirror LiteLLM's cross-provider defaults. They are deliberately
// kept in one place so a request has the same meaning when a combo falls back
// from a Responses model to Anthropic or Gemini.
const (
	reasoningMinimalBudget = 128
	reasoningLowBudget     = 1024
	reasoningMediumBudget  = 2048
	reasoningHighBudget    = 4096
	reasoningXHighBudget   = 8192
	reasoningMaxBudget     = 16384
)

var validReasoningEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true,
}

// parseReasoningEffort accepts both the Chat Completions string form and the
// Responses object form ({"effort":"high", ...}). The bool distinguishes an
// omitted value from an explicit "none".
func parseReasoningEffort(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return validateReasoningEffort(value)
	}
	var object struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.Effort == "" {
		return "", false, fmt.Errorf("reasoning_effort must be a string or an object with an effort field")
	}
	return validateReasoningEffort(object.Effort)
}

func validateReasoningEffort(value string) (string, bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !validReasoningEfforts[value] {
		return "", false, fmt.Errorf("invalid reasoning_effort %q: expected none, minimal, low, medium, high, xhigh, or max", value)
	}
	return value, true, nil
}

func reasoningBudget(effort string) int {
	switch effort {
	case "minimal":
		return reasoningMinimalBudget
	case "low":
		return reasoningLowBudget
	case "medium":
		return reasoningMediumBudget
	case "high":
		return reasoningHighBudget
	case "xhigh":
		return reasoningXHighBudget
	case "max":
		return reasoningMaxBudget
	default:
		return 0
	}
}

func reasoningForResponses(effort string) (map[string]any, error) {
	effort, _, err := validateReasoningEffort(effort)
	if err != nil {
		return nil, err
	}
	return map[string]any{"effort": effort}, nil
}

// reasoningForAnthropic maps OpenAI effort to Anthropic's budget-based thinking
// API while preserving the caller's max_tokens limit.
func reasoningForAnthropic(effort string, maxTokens int) (map[string]any, int, error) {
	effort, _, err := validateReasoningEffort(effort)
	if err != nil {
		return nil, maxTokens, err
	}
	if effort == "none" {
		return nil, maxTokens, nil
	}
	budget := reasoningBudget(effort)
	if budget < reasoningLowBudget {
		budget = reasoningLowBudget
	}
	// Anthropic rejects budgets below 1024 and requires max_tokens > budget.
	// Match LiteLLM: never inflate the caller's max_tokens; drop thinking when
	// there is not enough room, otherwise cap its budget at max_tokens-1.
	if maxTokens > 0 {
		if maxTokens <= reasoningLowBudget {
			return nil, maxTokens, nil
		}
		if budget >= maxTokens {
			budget = maxTokens - 1
		}
	}
	return map[string]any{"type": "enabled", "budget_tokens": budget}, maxTokens, nil
}

// reasoningForAnthropicModel follows LiteLLM's adaptive-thinking capability
// split for Claude 4.6+ while retaining legacy budget thinking elsewhere.
func reasoningForAnthropicModel(model, effort string, maxTokens int) (map[string]any, map[string]any, int, error) {
	effort, _, err := validateReasoningEffort(effort)
	if err != nil {
		return nil, nil, maxTokens, err
	}
	if effort == "none" {
		return nil, nil, maxTokens, nil
	}
	lower := strings.ToLower(model)
	adaptive := strings.Contains(lower, "opus-4-6") || strings.Contains(lower, "opus_4_6") ||
		strings.Contains(lower, "opus-4.6") || strings.Contains(lower, "opus-4-7") ||
		strings.Contains(lower, "opus_4_7") || strings.Contains(lower, "opus-4.7") ||
		strings.Contains(lower, "sonnet-4-6") || strings.Contains(lower, "sonnet_4_6") ||
		strings.Contains(lower, "sonnet-4.6")
	if !adaptive {
		thinking, maxTokens, err := reasoningForAnthropic(effort, maxTokens)
		return thinking, nil, maxTokens, err
	}
	outputEffort := effort
	if effort == "minimal" {
		outputEffort = "low"
	}
	return map[string]any{"type": "adaptive"}, map[string]any{"effort": outputEffort}, maxTokens, nil
}

// reasoningForGemini handles both Gemini 2.5's token budget and Gemini 3's
// qualitative thinkingLevel. Gemini 3 does not accept thinkingBudget.
func reasoningForGemini(model, effort string) map[string]any {
	effort, _, err := validateReasoningEffort(effort)
	if err != nil {
		return nil
	}
	lower := strings.ToLower(model)
	if strings.Contains(lower, "gemini-3") {
		level := "low"
		switch effort {
		case "minimal":
			if strings.Contains(lower, "flash") {
				level = "minimal"
			}
		case "medium":
			if strings.Contains(lower, "3.1-pro") || strings.Contains(lower, "flash") {
				level = "medium"
			} else {
				level = "high"
			}
		case "high", "xhigh", "max":
			level = "high"
		}
		return map[string]any{
			"thinkingLevel":   level,
			"includeThoughts": effort != "none",
		}
	}
	budget := 0
	include := effort != "none"
	if include {
		budget = reasoningBudget(effort)
		if effort == "minimal" {
			switch {
			case strings.Contains(lower, "gemini-2.5-flash-lite"):
				budget = 512
			case strings.Contains(lower, "gemini-2.5-flash"):
				budget = 1
			case strings.Contains(lower, "gemini-2.5-pro"):
				budget = 128
			}
		}
	}
	return map[string]any{"thinkingBudget": budget, "includeThoughts": include}
}

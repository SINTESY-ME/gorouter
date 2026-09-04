package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
)

// adaptReasoningEffort implements LiteLLM's capability degradation for a
// concrete combo candidate. The requested effort is never upgraded. In
// particular, max follows max -> xhigh -> high -> omitted.
func adaptReasoningEffort(requested string, caps domain.ReasoningCapabilities) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	supportsHigh := caps.SupportsReasoning
	switch requested {
	case "none":
		if supportsHigh || caps.SupportsLowReasoningEffort || caps.SupportsMinimalReasoningEffort {
			return "none"
		}
	case "max":
		if caps.SupportsXHighReasoningEffort {
			return "xhigh"
		}
		if supportsHigh {
			return "high"
		}
	case "xhigh":
		if caps.SupportsXHighReasoningEffort {
			return "xhigh"
		}
		if supportsHigh {
			return "high"
		}
	case "high":
		if supportsHigh {
			return "high"
		}
	case "medium":
		if supportsHigh {
			return "medium"
		}
		if caps.SupportsLowReasoningEffort {
			return "low"
		}
	case "low":
		if caps.SupportsLowReasoningEffort {
			return "low"
		}
		if supportsHigh {
			return "high"
		}
	case "minimal":
		if caps.SupportsMinimalReasoningEffort {
			return "minimal"
		}
		if caps.SupportsLowReasoningEffort {
			return "low"
		}
		if supportsHigh {
			return "high"
		}
	}
	return ""
}

// normalizeReasoningBodyForModel returns a copy of a request body whose
// reasoning effort is adapted for one concrete model. If that model has no
// reasoning capability, the effort is removed instead of being sent as an
// unsupported provider parameter.
func normalizeReasoningBodyForModel(body []byte, caps domain.ReasoningCapabilities) ([]byte, error) {
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("normalize reasoning: parse request: %w", err)
	}

	if raw, ok := wire["reasoning_effort"]; ok {
		effort, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("reasoning_effort must be a string")
		}
		effort = adaptReasoningEffort(effort, caps)
		if effort == "" {
			delete(wire, "reasoning_effort")
		} else {
			wire["reasoning_effort"] = effort
		}
	}

	if raw, ok := wire["reasoning"]; ok {
		if reasoning, ok := raw.(map[string]any); ok {
			if requested, ok := reasoning["effort"].(string); ok {
				effort := adaptReasoningEffort(requested, caps)
				if effort == "" {
					delete(reasoning, "effort")
				} else {
					reasoning["effort"] = effort
				}
			}
			if len(reasoning) == 0 {
				delete(wire, "reasoning")
			}
		}
	}
	return json.Marshal(wire)
}

func reasoningCapabilitiesFromModelEntry(entry domain.ModelEntry) domain.ReasoningCapabilities {
	return domain.ReasoningCapabilities{
		Known:                          true,
		SupportsReasoning:              entry.SupportsReasoning,
		SupportsMinimalReasoningEffort: entry.SupportsMinimalReasoningEffort,
		SupportsLowReasoningEffort:     entry.SupportsLowReasoningEffort,
		SupportsXHighReasoningEffort:   entry.SupportsXHighReasoningEffort,
		SupportsMaxReasoningEffort:     entry.SupportsMaxReasoningEffort,
	}
}

func mergeReasoningCapabilities(a, b domain.ReasoningCapabilities) domain.ReasoningCapabilities {
	return domain.ReasoningCapabilities{
		Known:                          a.Known || b.Known,
		SupportsReasoning:              a.SupportsReasoning || b.SupportsReasoning,
		SupportsMinimalReasoningEffort: a.SupportsMinimalReasoningEffort || b.SupportsMinimalReasoningEffort,
		SupportsLowReasoningEffort:     a.SupportsLowReasoningEffort || b.SupportsLowReasoningEffort,
		SupportsXHighReasoningEffort:   a.SupportsXHighReasoningEffort || b.SupportsXHighReasoningEffort,
		SupportsMaxReasoningEffort:     a.SupportsMaxReasoningEffort || b.SupportsMaxReasoningEffort,
	}
}
func reasoningCapabilitiesFromMap(values map[string]any) domain.ReasoningCapabilities {
	caps := domain.ReasoningCapabilities{Known: true}
	caps.SupportsReasoning, _ = values["supports_reasoning"].(bool)
	if reasoning, ok := values["reasoning"].(bool); ok {
		caps.SupportsReasoning = caps.SupportsReasoning || reasoning
	}
	caps.SupportsMinimalReasoningEffort, _ = values["supports_minimal_reasoning_effort"].(bool)
	caps.SupportsLowReasoningEffort, _ = values["supports_low_reasoning_effort"].(bool)
	caps.SupportsXHighReasoningEffort, _ = values["supports_xhigh_reasoning_effort"].(bool)
	caps.SupportsMaxReasoningEffort, _ = values["supports_max_reasoning_effort"].(bool)
	if caps.SupportsXHighReasoningEffort || caps.SupportsMaxReasoningEffort {
		caps.SupportsReasoning = true
	}
	return caps
}

// inferReasoningCapabilities is only used before the model catalog has been
// populated. Unknown models are fail-closed: max/high is not forwarded without
// evidence that the model supports reasoning.
func inferReasoningCapabilities(model string) domain.ReasoningCapabilities {
	lower := strings.ToLower(model)
	caps := domain.ReasoningCapabilities{}
	switch {
	case strings.Contains(lower, "gpt-5"), strings.Contains(lower, "o1"), strings.Contains(lower, "o3"), strings.Contains(lower, "o4"):
		caps.SupportsReasoning = true
		caps.SupportsXHighReasoningEffort = strings.Contains(lower, "o3") || strings.Contains(lower, "o4")
	case strings.Contains(lower, "claude-3-7"), strings.Contains(lower, "claude-3.7"), strings.Contains(lower, "claude-4"), strings.Contains(lower, "claude-5"):
		caps.SupportsReasoning = true
	case strings.Contains(lower, "gemini-2.5"), strings.Contains(lower, "gemini-2-5"), strings.Contains(lower, "gemini-3"):
		caps.SupportsReasoning = true
	}
	return caps
}

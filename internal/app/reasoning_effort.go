package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
)

// Effort is adapted per candidate, never per request: a combo can reach a
// different model on every fallback attempt, and each one has its own ladder.
//
// adaptReasoningEffort picks the closest level a model can actually honour.
// The requested effort is never upgraded: a model whose ladder starts at
// "medium" gets the field omitted when the caller asked for "low", instead of
// silently getting "high" — four levels of thinking nobody asked (or paid) for.
func adaptReasoningEffort(requested string, caps domain.ReasoningCapabilities) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return ""
	}
	return domain.HighestEffortAtOrBelow(requested, adaptationLadder(caps))
}

// adaptationLadder is the ladder used to pick an upstream value. "max" is the
// gateway's own ceiling and not an upstream value: asking for it selects the
// highest level below it (xhigh when the model has it). That has always been
// the behaviour here, and it is what keeps the literal "max" off the wire.
func adaptationLadder(caps domain.ReasoningCapabilities) []string {
	ladder := reasoningLadder(caps)
	out := make([]string, 0, len(ladder))
	for _, l := range ladder {
		if l != "max" {
			out = append(out, l)
		}
	}
	return out
}

// reasoningLadder returns the levels a model can be driven at. A ladder a
// source stated verbatim wins; otherwise the levels are inferred from the
// capability flags — the most a source publishing only booleans allows.
func reasoningLadder(caps domain.ReasoningCapabilities) []string {
	if len(caps.Efforts) > 0 {
		return caps.Efforts
	}
	return ladderFromFlags(caps)
}

// ladderFromFlags infers the ladder of a source that published booleans and no
// list. Every reasoning model is read as accepting the middle levels and the
// disable switch, because that is what these flags have always meant here: a
// source saying "supports_reasoning" has never been read as refusing "high".
// Extremes are added only when a source claims them.
func ladderFromFlags(caps domain.ReasoningCapabilities) []string {
	if !caps.SupportsReasoning && !caps.SupportsMinimalReasoningEffort &&
		!caps.SupportsLowReasoningEffort && !caps.SupportsXHighReasoningEffort &&
		!caps.SupportsMaxReasoningEffort {
		return nil
	}
	ladder := []string{"none", "medium", "high"}
	if caps.SupportsMinimalReasoningEffort {
		ladder = append(ladder, "minimal")
	}
	if caps.SupportsLowReasoningEffort {
		ladder = append(ladder, "low")
	}
	if caps.SupportsXHighReasoningEffort {
		ladder = append(ladder, "xhigh")
	}
	if caps.SupportsMaxReasoningEffort {
		ladder = append(ladder, "max")
	}
	return domain.SortEfforts(ladder)
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

// reasoningCapabilitiesFromMap reads LiteLLM-shaped reasoning metadata. The
// booleans are the older half of the contract; reasoning_effort_levels and
// default_reasoning_effort are the newer, more precise half.
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
	if d, ok := values["default_reasoning_effort"].(string); ok {
		caps.DefaultEffort = strings.ToLower(strings.TrimSpace(d))
	}
	// A list and the explicit bool extremes are statements from this same
	// source, so they combine — nothing enters the ladder that this source did
	// not say.
	ladder := stringList(values["reasoning_effort_levels"])
	stated := len(ladder) > 0
	if caps.SupportsMinimalReasoningEffort {
		ladder, stated = append(ladder, "minimal"), true
	}
	if caps.SupportsLowReasoningEffort {
		ladder, stated = append(ladder, "low"), true
	}
	if caps.SupportsXHighReasoningEffort {
		ladder, stated = append(ladder, "xhigh"), true
	}
	if caps.SupportsMaxReasoningEffort {
		ladder, stated = append(ladder, "max"), true
	}
	if none, ok := values["supports_none_reasoning_effort"].(bool); ok {
		if none {
			ladder, stated = append(ladder, "none"), true
		} else if caps.SupportsReasoning {
			// Stated as not disableable: a mandatory claim.
			caps.Mandatory = true
		}
	}
	if stated {
		caps.Efforts = domain.SortEfforts(ladder)
		caps.EffortsStated = true
	} else {
		caps.Efforts = ladderFromFlags(caps)
	}
	return caps
}

// reasoningCapabilitiesFromModelEntry rebuilds the capabilities of a persisted
// entry. A stored ladder is authoritative — it is what the sync concluded.
func reasoningCapabilitiesFromModelEntry(entry domain.ModelEntry) domain.ReasoningCapabilities {
	caps := domain.ReasoningCapabilities{
		Known:                          true,
		SupportsReasoning:              entry.SupportsReasoning,
		SupportsMinimalReasoningEffort: entry.SupportsMinimalReasoningEffort,
		SupportsLowReasoningEffort:     entry.SupportsLowReasoningEffort,
		SupportsXHighReasoningEffort:   entry.SupportsXHighReasoningEffort,
		SupportsMaxReasoningEffort:     entry.SupportsMaxReasoningEffort,
		Efforts:                        entry.SupportedReasoningEfforts,
		DefaultEffort:                  entry.DefaultReasoningEffort,
		Mandatory:                      entry.ReasoningMandatory,
	}
	caps.EffortsStated = len(caps.Efforts) > 0
	return caps
}

// reasoningCapabilitiesForEntry flattens merged capabilities back into the
// entry columns, deriving the four flags from the ladder so the row cannot
// contradict itself.
func reasoningCapabilitiesForEntry(caps domain.ReasoningCapabilities) (ladder []string, defaultEffort string, mandatory bool, minimal, low, xhigh, max bool) {
	caps = applyReasoningLadder(caps)
	return caps.Efforts, caps.DefaultEffort, caps.Mandatory,
		caps.SupportsMinimalReasoningEffort, caps.SupportsLowReasoningEffort,
		caps.SupportsXHighReasoningEffort, caps.SupportsMaxReasoningEffort
}

// applyReasoningLadder derives the four extreme flags from a stated ladder, so
// the columns and the ladder the catalog stores cannot contradict each other.
func applyReasoningLadder(caps domain.ReasoningCapabilities) domain.ReasoningCapabilities {
	if len(caps.Efforts) == 0 {
		return caps
	}
	has := func(level string) bool {
		for _, e := range caps.Efforts {
			if e == level {
				return true
			}
		}
		return false
	}
	caps.SupportsMinimalReasoningEffort = has("minimal")
	caps.SupportsLowReasoningEffort = has("low")
	caps.SupportsXHighReasoningEffort = has("xhigh")
	caps.SupportsMaxReasoningEffort = has("max")
	caps.SupportsReasoning = true
	return caps
}

// mergeReasoningCapabilities merges a later source into an earlier one. Flags
// are unioned: a capability one source claims is not erased by a source that
// says nothing about it. Ladders follow the chain instead — a stated ladder
// beats an inferred one regardless of order, two stated ladders keep the
// earlier link's, and when nobody stated a list the ladder is recomputed from
// the unioned flags. Unioning the ladders themselves would invent levels: a
// level only one upstream accepts is not a level the model accepts.
func mergeReasoningCapabilities(a, b domain.ReasoningCapabilities) domain.ReasoningCapabilities {
	out := domain.ReasoningCapabilities{
		Known:                          a.Known || b.Known,
		SupportsReasoning:              a.SupportsReasoning || b.SupportsReasoning,
		SupportsMinimalReasoningEffort: a.SupportsMinimalReasoningEffort || b.SupportsMinimalReasoningEffort,
		SupportsLowReasoningEffort:     a.SupportsLowReasoningEffort || b.SupportsLowReasoningEffort,
		SupportsXHighReasoningEffort:   a.SupportsXHighReasoningEffort || b.SupportsXHighReasoningEffort,
		SupportsMaxReasoningEffort:     a.SupportsMaxReasoningEffort || b.SupportsMaxReasoningEffort,
		// Reasoning that cannot be disabled on one candidate cannot be
		// disabled on the combo either.
		Mandatory:     a.Mandatory || b.Mandatory,
		DefaultEffort: firstNonEmpty(a.DefaultEffort, b.DefaultEffort),
	}
	switch {
	case a.EffortsStated:
		out.Efforts, out.EffortsStated = a.Efforts, true
	case b.EffortsStated:
		out.Efforts, out.EffortsStated = b.Efforts, true
	default:
		out.Efforts = ladderFromFlags(out)
	}
	return out
}

// reasoningCapsStated reports whether a source said anything at all about
// reasoning. "Known" is not the test: a registry knows every model it lists,
// including the ones it lists with no reasoning fields at all.
func reasoningCapsStated(caps domain.ReasoningCapabilities) bool {
	return len(caps.Efforts) > 0 || caps.SupportsReasoning || caps.Mandatory ||
		caps.SupportsMinimalReasoningEffort || caps.SupportsLowReasoningEffort ||
		caps.SupportsXHighReasoningEffort || caps.SupportsMaxReasoningEffort
}

// inferReasoningCapabilities is the last resort, used only when no source in
// the chain knows the model. Unknown models are fail-closed: an effort is not
// forwarded without evidence that the model reasons at all.
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
	if reasoningCapsStated(caps) {
		caps.Efforts = ladderFromFlags(caps)
	}
	return caps
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// stringList reads a JSON array of strings, dropping anything that is not one.
func stringList(v any) []string {
	if strs, ok := v.([]string); ok {
		return strs
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

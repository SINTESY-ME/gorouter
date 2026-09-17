package domain

import "strings"

// ReasoningEffortLevels is the canonical ladder, lowest to highest. "none" is
// not a level of thinking: it is the disable switch, and it sits at the floor
// so that ranking a request against a model's ladder is a single comparison.
var ReasoningEffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// EffortRank returns the level's position in the ladder, or -1 when the string
// is not a known level.
func EffortRank(level string) int {
	level = strings.ToLower(strings.TrimSpace(level))
	for i, l := range ReasoningEffortLevels {
		if l == level {
			return i
		}
	}
	return -1
}

// SortEfforts returns the levels in canonical order, deduplicated, with
// unknown strings dropped. Sources publish their ladder in their own order
// (OpenRouter sends max before high, before low), so anything compared or
// displayed goes through here first.
func SortEfforts(levels []string) []string {
	seen := make(map[string]bool, len(levels))
	for _, l := range levels {
		if r := EffortRank(l); r >= 0 {
			seen[ReasoningEffortLevels[r]] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for _, l := range ReasoningEffortLevels {
		if seen[l] {
			out = append(out, l)
		}
	}
	return out
}

// HighestEffortAtOrBelow returns the highest level in ladder that does not
// exceed requested, or "" when the ladder has nothing that low. "none" is only
// ever returned when it was asked for: disabling reasoning is not a valid way
// to satisfy a request for less thinking, and omitting the field is.
func HighestEffortAtOrBelow(requested string, ladder []string) string {
	want := EffortRank(requested)
	if want < 0 {
		return ""
	}
	best := ""
	for _, l := range ladder {
		r := EffortRank(l)
		if r < 0 || r > want {
			continue
		}
		if r == 0 && !strings.EqualFold(strings.TrimSpace(requested), "none") {
			continue
		}
		if best == "" || r > EffortRank(best) {
			best = ReasoningEffortLevels[r]
		}
	}
	return best
}

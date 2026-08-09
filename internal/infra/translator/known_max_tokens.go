package translator

import "strings"

// knownMaxOutputTokens mirrors the per-model max output limits that upstream
// providers advertise (same values used by bifrost's model-parameters cache).
// The Anthropic Messages API requires max_tokens, so when the client omits it
// we default to the model's known ceiling instead of a blind low value. Unknown
// models use the documented flagship ceiling (128000).
var knownMaxOutputTokens = map[string]int{
	"claude-opus-5":      128000,
	"claude-opus-4-8":    128000,
	"claude-opus-4-7":    128000,
	"claude-opus-4-6":    128000,
	"claude-sonnet-5":    128000,
	"claude-fable-5":     128000,
	"claude-sonnet-4-6":  64000,
	"claude-sonnet-4-5":  64000,
	"claude-haiku-4-5":   64000,
	"claude-opus-4-5":    64000,
	"claude-opus-4-1":    32000,
	"claude-sonnet-4":    64000,
	"claude-opus-4":      32000,
	"claude-sonnet-4-0":  64000,
	"claude-opus-4-0":    32000,
	"claude-3-5-sonnet":  8192,
	"claude-3-5-haiku":   8192,
	"claude-3-7-sonnet":  8192,
	"claude-3-opus":      4096,
	"claude-3-sonnet":    4096,
	"claude-3-haiku":     4096,
}

const fallbackMaxOutputTokens = 128000

// defaultMaxOutputTokens returns the max_output_tokens default to use when the
// client omits max_tokens. Known models use their documented maximum; unknown
// models (including provider-prefixed ids like "openrouter/anthropic/...")
// fall back to 128000, the ceiling shared by Anthropic's flagship models
// (opus-5/sonnet-5/fable-5) and modern providers that advertise a large
// output window.
func defaultMaxOutputTokens(model string) int {
	if v, ok := knownMaxOutputTokens[model]; ok {
		return v
	}
	base := normalizeMaxOutputModel(model)
	if v, ok := knownMaxOutputTokens[base]; ok {
		return v
	}
	return fallbackMaxOutputTokens
}

// normalizeMaxOutputModel strips provider prefixes, catalog suffixes and
// pricing tiers so that catalog ids like "anthropic/claude-opus-4.8-cheap:
// metered:full-context" or "openrouter/anthropic/claude-sonnet-5:batch"
// resolve to the base model name used in knownMaxOutputTokens.
func normalizeMaxOutputModel(model string) string {
	m := model
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	m = strings.TrimPrefix(m, "~")
	if i := strings.Index(m, ":"); i >= 0 {
		m = m[:i]
	}
	for {
		trimmed := m
		for _, suf := range []string{"-thinking", "-flatcost", "-metered", "-cheap", "-code", "-fast", "-batch", "-full-context"} {
			trimmed = strings.TrimSuffix(trimmed, suf)
		}
		trimmed = strings.ReplaceAll(trimmed, ".", "-")
		if trimmed == m {
			break
		}
		m = trimmed
	}
	return m
}
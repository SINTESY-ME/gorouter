package app

import "bytes"

// codexDefaultReasoningEffort is the level the Codex route runs at when the
// caller names none.
//
// The reference client pins this per connection instead of letting the
// upstream pick (the ChatGPT Codex backend applies its own default when the
// request carries no reasoning object), so the gateway does the same: the
// behaviour is then a decision recorded here rather than an upstream default
// that can move without notice. The value is still adapted per candidate model
// below, so a model that cannot honour "medium" gets the closest level it can.
//
// Measured 2026-09-23 on the codex route: naming the level changes the
// completion-token count by ~5% (2785 vs 2921 tokens for the same large tool
// call), so this is a determinism pin, NOT a fix for tool-call truncation.
const codexDefaultReasoningEffort = "medium"

// bodyHasReasoningEffort reports whether a request body already names a
// reasoning level, in either wire shape (top-level reasoning_effort, or the
// nested reasoning.effort that OpenRouter-style providers use).
func bodyHasReasoningEffort(body []byte) bool {
	return bytes.Contains(body, []byte(`"reasoning_effort"`)) ||
		bytes.Contains(body, []byte(`"reasoning"`))
}

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
)

// Combo strategy names. Stored on domain.Combo.Strategy and resolved by the
// StrategyRegistry. Add a constant here and Register it in
// NewStrategyRegistry to introduce a new strategy.
const (
	StrategyOrderedFallback = "ordered_fallback"
	StrategyRoundRobin      = "round-robin"
	StrategyVelocity        = "velocity"     // route to the fastest model (highest observed TPS)
	StrategyIntelligence    = "intelligence" // classifier picks the best model for the prompt
)

// StrategyRequest bundles the per-request data a strategy may need to decide
// the model iteration order. The router constructs it once and passes it to
// the resolved strategy; strategies ignore fields they don't use.
type StrategyRequest struct {
	Combo  *domain.Combo
	Body   []byte
	APIKey string
}

// ComboStrategy decides the order in which a combo's models are tried for a
// given request. The router iterates the returned list with its standard
// health/fallback logic, so a strategy only chooses ORDER — it never executes
// upstream calls itself, except the intelligence strategy which consults a
// classifier model to inform the ordering. Implementations must be safe for
// concurrent use.
type ComboStrategy interface {
	Order(ctx context.Context, req StrategyRequest) ([]string, error)
}

// StrategyRegistry maps strategy names to implementations. The default
// (ordered_fallback) is always present. Unknown strategies fall back to
// ordered_fallback so a misconfigured combo still serves requests.
type StrategyRegistry struct {
	strategies map[string]ComboStrategy
}

// NewStrategyRegistry builds a registry wired to the given router and
// registers every built-in strategy. The router pointer is captured by the
// strategies that need router-owned state (rotation counter, TPS cache, the
// classifier execution path).
func NewStrategyRegistry(r *RouterService) *StrategyRegistry {
	reg := &StrategyRegistry{strategies: map[string]ComboStrategy{}}
	reg.Register(StrategyOrderedFallback, orderedFallbackStrategy{})
	reg.Register(StrategyRoundRobin, roundRobinStrategy{r: r})
	reg.Register(StrategyVelocity, velocityStrategy{r: r})
	reg.Register(StrategyIntelligence, intelligenceStrategy{r: r})
	return reg
}

// Register adds (or replaces) a strategy under the given name.
func (r *StrategyRegistry) Register(name string, s ComboStrategy) {
	r.strategies[name] = s
}

// For returns the strategy registered under name, or the ordered-fallback
// strategy when name is empty or unknown.
func (r *StrategyRegistry) For(name string) ComboStrategy {
	if s, ok := r.strategies[name]; ok {
		return s
	}
	return r.strategies[StrategyOrderedFallback]
}

// --- ordered_fallback ---

// orderedFallbackStrategy returns the configured model order unchanged.
type orderedFallbackStrategy struct{}

func (orderedFallbackStrategy) Order(_ context.Context, req StrategyRequest) ([]string, error) {
	return req.Combo.Models, nil
}

// --- round-robin ---

// roundRobinStrategy rotates the starting index each request so load is
// distributed evenly across members. It borrows the router's in-memory
// rotation counter (which is process-local and resets on restart).
type roundRobinStrategy struct{ r *RouterService }

func (s roundRobinStrategy) Order(_ context.Context, req StrategyRequest) ([]string, error) {
	return s.r.rotatedModels(req.Combo.Name, req.Combo.Models), nil
}

// --- velocity (tps) ---

// velocityStrategy sorts members by observed tokens-per-second, fastest
// first. Models with no observed TPS sort last (stable), preserving the
// configured order until enough usage data accumulates. Before ordering,
// it triggers background TPS probes for models that have no data at all or
// whose probe data is stale — so future requests benefit from the measured
// TPS. The fallback chain stays intact: if the fastest model fails, the
// next-fastest is tried.
type velocityStrategy struct{ r *RouterService }

func (s velocityStrategy) Order(_ context.Context, req StrategyRequest) ([]string, error) {
	for _, m := range req.Combo.Models {
		if s.r.TPSProber != nil {
			s.r.TPSProber.MaybeProbe(m)
		}
	}
	return orderVelocity(req.Combo.Models, s.r.TPS), nil
}

// orderVelocity returns a copy of models sorted by TPS descending. A nil
// cache (strategy not wired) yields the original order.
func orderVelocity(models []string, tps *TPSCache) []string {
	out := make([]string, len(models))
	copy(out, models)
	if tps == nil {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		return tps.Get(out[i]) > tps.Get(out[j])
	})
	return out
}

// --- intelligence ---

// intelligenceStrategy asks a classifier model to pick the best model for the
// user's prompt directly. The classifier receives the list of combo members
// with their descriptions and returns the model id it recommends. The chosen
// model leads the iteration order; the remaining models follow in their
// configured order as fallback. On any failure it degrades to the original
// configured order (play safe, try the first model).
type intelligenceStrategy struct{ r *RouterService }

func (s intelligenceStrategy) Order(ctx context.Context, req StrategyRequest) ([]string, error) {
	combo := req.Combo
	if combo.ClassifierModel == "" {
		return combo.Models, nil
	}
	prompt := extractPromptText(req.Body)
	chosen, err := s.classify(ctx, combo, prompt, req.APIKey)
	if err != nil || chosen == "" {
		slog.Warn("intelligence classification failed; using original order", "combo", combo.Name, "err", err, "chosen", chosen)
		return combo.Models, nil
	}
	return reorderChosenFirst(combo.Models, chosen), nil
}

// classify asks the classifier model to pick the best model for the prompt.
// It returns the model id string (must match one of the combo's members).
func (s intelligenceStrategy) classify(ctx context.Context, combo *domain.Combo, prompt string, apiKey string) (string, error) {
	if prompt == "" {
		return "", nil
	}
	var modelLines []string
	for _, mID := range combo.Models {
		desc := ""
		if meta, ok := combo.ModelMeta[mID]; ok {
			desc = meta.Description
		}
		line := fmt.Sprintf("- %s", mID)
		if desc != "" {
			line += fmt.Sprintf(": %s", desc)
		}
		modelLines = append(modelLines, line)
	}

	system := fmt.Sprintf(
		"You are an intelligent LLM routing classifier. You analyze the full conversation flow below (including system instructions, user messages, assistant responses, tool calls, and tool results) and choose the single model that best fits the task.\n\nConsider:\n- The type of task (coding, reasoning, writing, math, simple chat, etc.)\n- The complexity indicated by the conversation context (not just the last message)\n- Whether tool calls or code execution are involved\n- The length and depth of the context\n\nAvailable models:\n%s\n\nRespond with ONLY the model id (exactly as written above), no extra words, no punctuation, no explanation.",
		strings.Join(modelLines, "\n"))
	messages := []map[string]any{
		{"role": "system", "content": system},
		{"role": "user", "content": prompt},
	}
	text, err := s.r.singleCompletion(ctx, combo.ClassifierModel, messages, apiKey)
	if err != nil {
		slog.Warn("intelligence classifier model call failed", "combo", combo.Name, "classifier", combo.ClassifierModel, "err", err)
		return "", err
	}
	chosen := strings.TrimSpace(text)
	slog.Info("intelligence classifier result", "combo", combo.Name, "classifier", combo.ClassifierModel, "prompt_sample", truncate(prompt, 100), "raw_output", chosen)
	// Validate that the classifier returned one of the combo members.
	for _, mID := range combo.Models {
		if mID == chosen {
			return chosen, nil
		}
	}
	// Fuzzy match: the classifier may have returned the model id with
	// extra whitespace, quotes, or a partial match. Try a contains check.
	lower := strings.ToLower(chosen)
	for _, mID := range combo.Models {
		if strings.ToLower(mID) == lower || strings.Contains(lower, strings.ToLower(mID)) {
			return mID, nil
		}
	}
	slog.Warn("intelligence classifier returned unknown model; ignoring", "combo", combo.Name, "chosen", chosen)
	return "", nil
}

// reorderChosenFirst moves the chosen model to the front of the list,
// preserving the relative order of the remaining models as fallback.
func reorderChosenFirst(models []string, chosen string) []string {
	out := make([]string, 0, len(models))
	out = append(out, chosen)
	for _, m := range models {
		if m != chosen {
			out = append(out, m)
		}
	}
	return out
}

// extractPromptText builds a text representation of the conversation for the
// classifier. It includes the system prompt (for task context) and the last
// few messages (to understand the current task), including tool calls and
// results from the current turn. This gives the classifier enough context to
// distinguish between coding, reasoning, writing, and simple chat tasks
// without sending the entire conversation history.
//
// The text is capped at maxClassifierPromptChars. When the selected messages
// exceed the cap, trimming is applied from the oldest included message so
// the most recent context (the current task) is always preserved.
func extractPromptText(body []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return truncate(string(body), maxClassifierPromptChars)
	}
	msgs, _ := raw["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}

	// Collect system messages (always included) + last N messages.
	const recentCount = 6
	var selected []map[string]any
	for _, mi := range msgs {
		m, _ := mi.(map[string]any)
		if m == nil {
			continue
		}
		if role, _ := m["role"].(string); role == "system" {
			selected = append(selected, m)
		}
	}
	// Add the last recentCount messages (deduplicated against system messages
	// already selected).
	start := len(msgs) - recentCount
	if start < 0 {
		start = 0
	}
	for i := start; i < len(msgs); i++ {
		m, _ := msgs[i].(map[string]any)
		if m == nil {
			continue
		}
		// Skip system messages already collected above.
		if role, _ := m["role"].(string); role == "system" {
			continue
		}
		selected = append(selected, m)
	}

	if len(selected) == 0 {
		return ""
	}

	// Build text representation.
	var lines []string
	for _, m := range selected {
		role, _ := m["role"].(string)
		if role == "" {
			role = "unknown"
		}
		content := extractMessageContent(m)

		// Include tool_calls if present (assistant messages with function calls).
		if tc, ok := m["tool_calls"].([]any); ok && len(tc) > 0 {
			for _, call := range tc {
				c, _ := call.(map[string]any)
				if c == nil {
					continue
				}
				fn, _ := c["function"].(map[string]any)
				if fn != nil {
					name, _ := fn["name"].(string)
					content += fmt.Sprintf("\n[tool_call: %s]", name)
				}
			}
		}

		// Include tool name for tool messages (tool results).
		if role == "tool" {
			if name, ok := m["name"].(string); ok && name != "" {
				role = "tool:" + name
			}
		}

		if content != "" {
			lines = append(lines, fmt.Sprintf("[%s] %s", role, content))
		}
	}

	if len(lines) == 0 {
		return ""
	}

	full := strings.Join(lines, "\n")
	if len(full) > maxClassifierPromptChars {
		cut := len(full) - maxClassifierPromptChars
		if idx := strings.Index(full[cut:], "\n"); idx != -1 {
			cut += idx + 1
		}
		full = full[cut:]
	}
	return full
}

func extractMessageContent(m map[string]any) string {
	switch v := m["content"].(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, block := range v {
			b, _ := block.(map[string]any)
			if b != nil {
				if t, ok := b["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

const maxClassifierPromptChars = 8000

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
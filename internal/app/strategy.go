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
		"You are an intelligent LLM routing classifier. Given the user's prompt and the list of available models below, choose the single model that best fits the task.\n\nAvailable models:\n%s\n\nRespond with ONLY the model id (exactly as written above), no extra words, no punctuation, no explanation.",
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

// extractPromptText pulls the user's prompt out of a chat request body. It
// prioritizes the last user message in the conversation so that system prompts
// or previous conversation turns do not obscure the current user task.
func extractPromptText(body []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return truncate(string(body), maxClassifierPromptChars)
	}
	msgs, _ := raw["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}

	// 1. Prioritize the last user message in the conversation
	for i := len(msgs) - 1; i >= 0; i-- {
		m, _ := msgs[i].(map[string]any)
		if m == nil {
			continue
		}
		if role, ok := m["role"].(string); ok && role == "user" {
			content := extractMessageContent(m)
			if content != "" {
				return truncate(content, maxClassifierPromptChars)
			}
		}
	}

	// 2. Fallback: join all message contents if no user message found
	parts := make([]string, 0, len(msgs))
	for _, mi := range msgs {
		m, _ := mi.(map[string]any)
		if m != nil {
			if c := extractMessageContent(m); c != "" {
				parts = append(parts, c)
			}
		}
	}
	return truncate(strings.Join(parts, "\n"), maxClassifierPromptChars)
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

const maxClassifierPromptChars = 2000

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
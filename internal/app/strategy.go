package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
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
	StrategyIntelligence    = "intelligence" // classify prompt complexity, route by model weight
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
	// Trigger background probes for models without TPS data or with stale
	// probes. Non-blocking; results populate the cache for future requests.
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

// intelligenceStrategy classifies the prompt's complexity via a dedicated
// classifier model, then returns members ordered by best weight fit: the
// least-capable model that meets the complexity leads (no overkill), with
// the remaining models trailing as fallback. On any failure it degrades to
// the most-capable model first (assume complex, play safe).
type intelligenceStrategy struct{ r *RouterService }

func (s intelligenceStrategy) Order(ctx context.Context, req StrategyRequest) ([]string, error) {
	combo := req.Combo
	if combo.ClassifierModel == "" {
		// Not configured: assume complex, prefer the most capable member.
		return orderIntelligence(combo.Models, combo.ModelMeta, maxComplexity), nil
	}
	prompt := extractPromptText(req.Body)
	complexity, err := s.classify(ctx, combo, prompt, req.APIKey)
	if err != nil || complexity == 0 {
		slog.Warn("intelligence classification failed or unparseable; defaulting to most capable", "combo", combo.Name, "err", err, "complexity", complexity)
		return orderIntelligence(combo.Models, combo.ModelMeta, maxComplexity), nil
	}
	return orderIntelligence(combo.Models, combo.ModelMeta, complexity), nil
}

const maxComplexity = 10

// classify asks the classifier model to rate the prompt complexity on the
// same 1..10 scale the weights use. The available weight range is included
// so the model calibrates to the combo's capability tiers.
func (s intelligenceStrategy) classify(ctx context.Context, combo *domain.Combo, prompt string, apiKey string) (int, error) {
	if prompt == "" {
		return 1, nil // nothing to classify; treat as trivial
	}
	var modelLines []string
	for _, mID := range combo.Models {
		w := 5
		desc := ""
		if meta, ok := combo.ModelMeta[mID]; ok {
			if meta.Weight > 0 {
				w = meta.Weight
			}
			desc = meta.Description
		}
		line := fmt.Sprintf("- Model: %s (Weight: %d)", mID, w)
		if desc != "" {
			line += fmt.Sprintf(" - Description: %s", desc)
		}
		modelLines = append(modelLines, line)
	}

	system := fmt.Sprintf(
		"You are an intelligent LLM routing classifier. Analyze the user's prompt and rate its complexity on a scale of 1 (trivial/greetings/formatting) to 10 (highly complex/advanced coding/deep reasoning/architecture).\n\nAvailable models, capability weights, and usage guidelines:\n%s\n\nCalibrate your score to match the appropriate model weight. Respond with ONLY a single integer between 1 and 10, no words, no punctuation.",
		strings.Join(modelLines, "\n"))
	messages := []map[string]any{
		{"role": "system", "content": system},
		{"role": "user", "content": prompt},
	}
	text, err := s.r.singleCompletion(ctx, combo.ClassifierModel, messages, apiKey)
	if err != nil {
		slog.Warn("intelligence classifier model call failed", "combo", combo.Name, "classifier", combo.ClassifierModel, "err", err)
		return 0, err
	}
	c := parseComplexity(text)
	slog.Info("intelligence classifier result", "combo", combo.Name, "classifier", combo.ClassifierModel, "prompt_sample", truncate(prompt, 100), "raw_output", strings.TrimSpace(text), "parsed_complexity", c)
	if c < 1 {
		c = 1
	}
	if c > maxComplexity {
		c = maxComplexity
	}
	return c, nil
}

// orderIntelligence orders models by best weight fit for the complexity.
// Capable models (weight >= complexity) lead, least overkill first; the
// remainder follow, most capable first (best effort). Members without a
// weight keep their configured relative order at the tail.
func orderIntelligence(models []string, meta map[string]domain.ComboModelMeta, complexity int) []string {
	type entry struct {
		id     string
		weight int
	}
	capable := make([]entry, 0, len(models))
	rest := make([]entry, 0, len(models))
	for _, id := range models {
		w := 0
		if m, ok := meta[id]; ok {
			w = m.Weight
		}
		if w > 0 && w >= complexity {
			capable = append(capable, entry{id, w})
		} else {
			rest = append(rest, entry{id, w})
		}
	}
	// Least overkill first: ascending weight, stable on original order.
	sort.SliceStable(capable, func(i, j int) bool { return capable[i].weight < capable[j].weight })
	// Best effort: most capable first, stable on original order.
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].weight > rest[j].weight })
	out := make([]string, 0, len(models))
	for _, e := range capable {
		out = append(out, e.id)
	}
	for _, e := range rest {
		out = append(out, e.id)
	}
	return out
}

// weightRange returns the min/max capability weight declared on the combo's
// members. Defaults to 1..10 when no weights are set so the classifier still
// has a meaningful scale to calibrate against.
func weightRange(meta map[string]domain.ComboModelMeta) (int, int) {
	lo, hi := 0, 0
	for _, m := range meta {
		if m.Weight > 0 {
			if lo == 0 || m.Weight < lo {
				lo = m.Weight
			}
			if m.Weight > hi {
				hi = m.Weight
			}
		}
	}
	if lo == 0 {
		return 1, maxComplexity
	}
	return lo, hi
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

var complexityRE = regexp.MustCompile(`\d+`)

// parseComplexity extracts the first integer from the classifier output and
// clamps it to 1..10. Strips <think>...</think> reasoning blocks first.
func parseComplexity(text string) int {
	cleaned := text
	if idx := strings.LastIndex(cleaned, "</think>"); idx != -1 {
		cleaned = cleaned[idx+8:]
	}
	cleaned = strings.TrimSpace(cleaned)

	if m := complexityRE.FindString(cleaned); m != "" {
		n, err := strconv.Atoi(m)
		if err == nil {
			return n
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

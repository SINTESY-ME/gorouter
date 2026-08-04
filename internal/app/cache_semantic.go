package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/jhon/gorouter/internal/domain"
)

// SemanticCacheMode controls when the semantic cache is consulted.
const (
	SemanticModeActive = "active" // search on every deterministic miss
	SemanticModeLazy   = "lazy"   // only search after warm-up threshold
	SemanticLazyWarmup = 50       // min entries before lazy mode searches
)

// SemanticCacheService wraps a SemanticCache and an EmbeddingProvider with
// the logic to look up and store responses by vector similarity. It is
// nil-safe: a nil service disables all semantic caching.
type SemanticCacheService struct {
	cache         domain.SemanticCache
	embedder      domain.EmbeddingProvider
	modelSettable interface {
		SetModel(string)
	}
	threshold float64
	mode      string
	enabled   atomic.Bool
}

// NewSemanticCacheService creates a new SemanticCacheService. The service
// starts disabled; call SetEnabled to activate it.
func NewSemanticCacheService(cache domain.SemanticCache, embedder domain.EmbeddingProvider, threshold float64, mode string) *SemanticCacheService {
	s := &SemanticCacheService{
		cache:     cache,
		embedder:  embedder,
		threshold: threshold,
		mode:      mode,
	}
	if settable, ok := embedder.(interface{ SetModel(string) }); ok {
		s.modelSettable = settable
	}
	if mode == "" {
		s.mode = SemanticModeActive
	}
	return s
}

// Enabled reports whether semantic caching is active.
func (s *SemanticCacheService) Enabled() bool {
	return s != nil && s.enabled.Load()
}

// SetEnabled toggles semantic caching live (dashboard setting).
func (s *SemanticCacheService) SetEnabled(v bool) {
	if s == nil {
		return
	}
	s.enabled.Store(v)
}

// Mode returns the current semantic cache mode ("active" or "lazy").
func (s *SemanticCacheService) Mode() string {
	if s == nil {
		return ""
	}
	return s.mode
}

// SetMode changes the semantic cache mode live.
func (s *SemanticCacheService) SetMode(mode string) {
	if s == nil {
		return
	}
	if mode == SemanticModeActive || mode == SemanticModeLazy {
		s.mode = mode
	}
}

// SetThreshold updates the similarity threshold live.
func (s *SemanticCacheService) SetThreshold(t float64) {
	if s == nil {
		return
	}
	s.threshold = t
}

// SetModel changes the embedding model used to generate vectors.
func (s *SemanticCacheService) SetModel(model string) {
	if s == nil {
		return
	}
	if s.modelSettable != nil {
		s.modelSettable.SetModel(model)
	}
}

// LookupResult holds the result of a semantic cache lookup.
type LookupResult struct {
	Response   *domain.CachedResponse
	Similarity float64
	Model      string // the model ID that had the hit
	Hit        bool
}

// Lookup searches the semantic cache for a response similar to the given
// request body, across all candidate models. Returns the first hit found
// (in model order). Fail-open: any error returns a miss.
func (s *SemanticCacheService) Lookup(ctx context.Context, body []byte, models []string, inputFormat domain.Format) LookupResult {
	if s == nil || !s.Enabled() || s.cache == nil || s.embedder == nil || len(models) == 0 {
		return LookupResult{}
	}

	// Lazy mode: only search after warm-up.
	if s.mode == SemanticModeLazy {
		stats := s.cache.Stats()
		if stats.Entries < SemanticLazyWarmup {
			return LookupResult{}
		}
	}

	promptText := normalizeSemanticText(extractSemanticPromptText(body))
	if promptText == "" {
		return LookupResult{}
	}

	embedding, err := s.embedder.Embed(ctx, promptText)
	if err != nil {
		slog.Debug("semantic cache: embedding failed", "err", err)
		return LookupResult{}
	}

	for _, modelStr := range models {
		key := modelStr + "/" + string(inputFormat)
		resp, sim, ok := s.cache.Get(ctx, key, embedding, s.threshold)
		if ok {
			slog.Info("semantic cache hit", "model", modelStr, "similarity", sim)
			return LookupResult{
				Response:   resp,
				Similarity: sim,
				Model:      modelStr,
				Hit:        true,
			}
		}
	}
	return LookupResult{}
}

// Store records a response in the semantic cache under the given model key.
// Fail-open: any error is silently dropped.
func (s *SemanticCacheService) Store(ctx context.Context, body []byte, modelStr string, inputFormat domain.Format, resp *domain.CachedResponse) {
	if s == nil || !s.Enabled() || s.cache == nil || s.embedder == nil || resp == nil {
		return
	}

	promptText := normalizeSemanticText(extractSemanticPromptText(body))
	if promptText == "" {
		return
	}

	embedding, err := s.embedder.Embed(ctx, promptText)
	if err != nil {
		slog.Debug("semantic cache: store embedding failed", "err", err)
		return
	}

	key := modelStr + "/" + string(inputFormat)
	s.cache.Put(ctx, key, embedding, resp)
}

// Stats returns current semantic cache statistics.
func (s *SemanticCacheService) Stats() domain.CacheStats {
	if s == nil || s.cache == nil {
		return domain.CacheStats{}
	}
	return s.cache.Stats()
}

// Flush removes all entries from the semantic cache.
func (s *SemanticCacheService) Flush(ctx context.Context) {
	if s == nil || s.cache == nil {
		return
	}
	s.cache.Flush(ctx)
}

// normalizeSemanticText applies consistent normalization (trim + lowercase)
// before embedding so case/whitespace variations map to the same vector —
// mirroring Bifrost's normalizeText for higher semantic hit rates.
func normalizeSemanticText(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// extractSemanticPromptText extracts a text representation of the prompt
// from a JSON request body (OpenAI format). It concatenates system + user
// messages up to a cap, providing enough text for a meaningful embedding
// without consuming excessive tokens.
func extractSemanticPromptText(body []byte) string {
	var raw struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}
	if len(raw.Messages) == 0 {
		return ""
	}

	const maxChars = 4000
	var result []byte
	for _, msg := range raw.Messages {
		if msg.Role == "assistant" || msg.Role == "tool" {
			continue
		}
		text := contentToText(msg.Content)
		if text == "" {
			continue
		}
		if len(result) > 0 {
			result = append(result, '\n')
		}
		result = append(result, text...)
		if len(result) >= maxChars {
			result = result[:maxChars]
			break
		}
	}
	return string(result)
}

// contentToText extracts text from an OpenAI message content field, which
// can be a string or an array of content blocks.
func contentToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, block := range v {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := b["text"].(string); ok && t != "" {
				parts = append(parts, t)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		// Simple join without importing strings — keep it minimal.
		out := parts[0]
		for _, p := range parts[1:] {
			out += "\n" + p
		}
		return out
	}
	return ""
}

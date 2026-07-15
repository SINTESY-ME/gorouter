package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// ModelRegistry is an in-memory cache of model metadata fetched from public
// APIs (LiteLLM, models.dev, OpenRouter). HuggingFace is queried on demand.
// The cache is loaded lazily on first use and refreshed every 24h.
//
// ResolveKind tries each source in order and returns the first match. If no
// source has the model, it falls back to a name-based heuristic.
//
// Two maps are maintained: byModel (keyed by normalized model name, first-wins
// across sources) and byProviderModel (keyed by "provider/model", allowing
// provider-specific price lookups). ResolvePricing tries provider+model
// first, then falls back to model-only.
type ModelRegistry struct {
	mu             sync.RWMutex
	entries        map[string]registryEntry // byModel (normalized name)
	byProviderModel map[string]registryEntry // "litellmProvider/normalizedModel"
	loadedAt       time.Time
	ttl            time.Duration
	client         *http.Client
}

type registryEntry struct {
	Kind              domain.ModelKind
	Context           int
	SupportsVision    bool
	SupportsToolCall  bool
	SupportsReasoning bool
	Pricing           domain.ModelPricing
}

const registryTTL = 24 * time.Hour

func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		entries: nil, // lazy
		ttl:     registryTTL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// ResolveKind returns the ModelKind for the given model ID, trying external
// registries first and falling back to a name-based heuristic. The modelID
// is the bare model name (e.g. "whisper-1", not "openai/whisper-1").
func (r *ModelRegistry) ResolveKind(modelID string) (domain.ModelKind, int, bool, bool, bool) {
	if r.ensureLoaded() {
		r.mu.RLock()
		e, ok := r.entries[normalizeModelName(modelID)]
		r.mu.RUnlock()
		if ok {
			return e.Kind, e.Context, e.SupportsVision, e.SupportsToolCall, e.SupportsReasoning
		}
	}
	k := heuristicKind(modelID)
	return k, 0, false, false, false
}

// ResolvePricing returns the ModelPricing for the given (gorouterProvider, modelID)
// pair. It first tries an exact (provider, model) match, then falls back to
// model-only, then attempts fuzzy matching (safe suffixes, containment, Levenshtein).
// Returns (zero, false) if no pricing data is found.
func (r *ModelRegistry) ResolvePricing(gorouterProvider, modelID string) (domain.ModelPricing, bool) {
	if !r.ensureLoaded() {
		return domain.ModelPricing{}, false
	}
	normModel := normalizeModelName(modelID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	// 1. Exact (provider, model) match — map gorouter provider to LiteLLM provider.
	lp := mapGorouterToLitellmProvider(gorouterProvider)
	if lp != "" {
		if e, ok := r.byProviderModel[lp+"/"+normModel]; ok && HasPricing(e.Pricing) {
			return e.Pricing, true
		}
	}
	// 2. Fallback: model-only match.
	if e, ok := r.entries[normModel]; ok && HasPricing(e.Pricing) {
		return e.Pricing, true
	}
	// 3. Fuzzy matching: safe suffixes, containment, Levenshtein distance.
	if e, ok := findBestFuzzyMatch(normModel, r.entries); ok {
		return e.Pricing, true
	}
	return domain.ModelPricing{}, false
}

// ensureLoaded loads the registry from external APIs if stale or not yet
// loaded. Returns true if the cache is available (even if partially loaded).
func (r *ModelRegistry) ensureLoaded() bool {
	r.mu.RLock()
	if r.entries != nil && time.Since(r.loadedAt) < r.ttl {
		r.mu.RUnlock()
		return true
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if r.entries != nil && time.Since(r.loadedAt) < r.ttl {
		return true
	}

	entries := make(map[string]registryEntry)
	byProvider := make(map[string]registryEntry)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Load from each source; failures are non-fatal — we use whatever we get.
	r.loadLiteLLM(ctx, entries, byProvider)
	r.loadModelsDev(ctx, entries, byProvider)
	r.loadOpenRouter(ctx, entries, byProvider)

	r.entries = entries
	r.byProviderModel = byProvider
	r.loadedAt = time.Now()
	return len(entries) > 0
}

func (r *ModelRegistry) loadLiteLLM(ctx context.Context, entries map[string]registryEntry, byProvider map[string]registryEntry) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json", nil)
	if err != nil {
		return
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return
	}
	var data map[string]map[string]any
	if json.Unmarshal(buf, &data) != nil {
		return
	}
	for k, v := range data {
		if k == "sample_spec" {
			continue
		}
		mode, _ := v["mode"].(string)
		kind := litellmModeToKind(mode)
		if kind == "" {
			continue
		}
		ctxLen := 0
		if mi, ok := v["max_input_tokens"].(float64); ok {
			ctxLen = int(mi)
		}
		e := registryEntry{Kind: kind, Context: ctxLen}
		e.SupportsVision, _ = v["supports_vision"].(bool)
		e.SupportsToolCall, _ = v["supports_function_calling"].(bool)
		e.SupportsReasoning, _ = v["supports_reasoning"].(bool)
		e.Pricing = parseLiteLLMPricing(v)
		e.Pricing.Source = "litellm"
		e.Pricing.LastSyncedAt = time.Now()
		normModel := normalizeModelName(k)
		entries[normModel] = e
		// Also key by litellm_provider/model for provider-specific lookup.
		if lp, ok := v["litellm_provider"].(string); ok && lp != "" {
			byProvider[lp+"/"+normModel] = e
		}
	}
}

func (r *ModelRegistry) loadModelsDev(ctx context.Context, entries map[string]registryEntry, byProvider map[string]registryEntry) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://models.dev/api.json", nil)
	if err != nil {
		return
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return
	}
	var data map[string]map[string]any
	if json.Unmarshal(buf, &data) != nil {
		return
	}
	for providerID, provider := range data {
		models, ok := provider["models"].(map[string]any)
		if !ok {
			continue
		}
		for modelID, mv := range models {
			m, ok := mv.(map[string]any)
			if !ok {
				continue
			}
			key := normalizeModelName(modelID)
			// models.dev is LLM-focused; kind is always llm unless output has image.
			kind := domain.KindLLM
			e := registryEntry{Kind: kind}
			e.SupportsVision, _ = m["attachment"].(bool)
			e.SupportsToolCall, _ = m["tool_call"].(bool)
			e.SupportsReasoning, _ = m["reasoning"].(bool)
			// Parse pricing (per-1M-tokens, convert to per-token)
			if cost, ok := m["cost"].(map[string]any); ok {
				e.Pricing = parseModelsDevPricing(cost)
				e.Pricing.Source = "models.dev"
				e.Pricing.LastSyncedAt = time.Now()
			}
			// byModel: first-wins (LiteLLM takes priority)
			if _, exists := entries[key]; !exists {
				entries[key] = e
			}
			// byProvider: first-wins
			pk := providerID + "/" + key
			if _, exists := byProvider[pk]; !exists {
				byProvider[pk] = e
			}
		}
	}
}

func (r *ModelRegistry) loadOpenRouter(ctx context.Context, entries map[string]registryEntry, byProvider map[string]registryEntry) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return
	}
	var data struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(buf, &data) != nil {
		return
	}
	for _, m := range data.Data {
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		key := normalizeModelName(id)
		// Extract provider from id (e.g. "openai/gpt-4o" -> "openai")
		var orProvider string
		if i := strings.Index(id, "/"); i > 0 {
			orProvider = id[:i]
		}
		kind := domain.KindLLM
		arch, _ := m["architecture"].(map[string]any)
		if modality, ok := arch["modality"].(string); ok {
			if strings.Contains(modality, "image") {
				kind = domain.KindImage
			}
		}
		e := registryEntry{Kind: kind}
		e.SupportsToolCall = hasParam(m, "tools")
		e.SupportsReasoning = hasParam(m, "reasoning")
		// Parse pricing (per-token, but values are strings)
		if pricing, ok := m["pricing"].(map[string]any); ok {
			e.Pricing = parseOpenRouterPricing(pricing)
			e.Pricing.Source = "openrouter"
			e.Pricing.LastSyncedAt = time.Now()
		}
		// byModel: first-wins
		if _, exists := entries[key]; !exists {
			entries[key] = e
		}
		// byProvider: map OpenRouter provider to gorouter provider, first-wins
		if orProvider != "" {
			gp := mapOpenRouterProvider(orProvider)
			pk := gp + "/" + key
			if _, exists := byProvider[pk]; !exists {
				byProvider[pk] = e
			}
		}
	}
}

// hasParam checks if supported_parameters contains the given value.
func hasParam(m map[string]any, target string) bool {
	sp, ok := m["supported_parameters"].([]any)
	if !ok {
		return false
	}
	for _, v := range sp {
		if s, ok := v.(string); ok && s == target {
			return true
		}
	}
	return false
}

// litellmModeToKind maps LiteLLM's mode field to ModelKind.
func litellmModeToKind(mode string) domain.ModelKind {
	switch mode {
	case "chat", "completion", "responses":
		return domain.KindLLM
	case "embedding":
		return domain.KindEmbedding
	case "image_generation", "image_edit":
		return domain.KindImage
	case "audio_speech":
		return domain.KindTTS
	case "audio_transcription":
		return domain.KindSTT
	case "rerank":
		return domain.KindRerank
	case "ocr":
		return domain.KindOCR
	case "video_generation":
		return domain.KindVideo
	default:
		return ""
	}
}

// normalizeModelName lowercases the model name and strips any provider
// prefix before the last "/". e.g. "azure/whisper-1" -> "whisper-1".
func normalizeModelName(s string) string {
	s = strings.ToLower(s)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// heuristicKind guesses the model kind from its name when external registries
// don't have it.
func heuristicKind(modelID string) domain.ModelKind {
	s := strings.ToLower(modelID)
	switch {
	case strings.Contains(s, "embed"):
		return domain.KindEmbedding
	case strings.Contains(s, "whisper") || strings.Contains(s, "transcri"):
		return domain.KindSTT
	case strings.Contains(s, "tts") || strings.Contains(s, "speech") || strings.Contains(s, "kokoro"):
		return domain.KindTTS
	case strings.Contains(s, "dall-e") || strings.Contains(s, "image") || strings.Contains(s, "diffusion") || strings.Contains(s, "stable"):
		return domain.KindImage
	case strings.Contains(s, "rerank"):
		return domain.KindRerank
	default:
		return domain.KindLLM
	}
}

// providerModelTypeToKind maps the provider's own model_type/endpoint_format
// fields (e.g. OpenAdapter's response) to ModelKind. endpoint_format takes
// priority when it indicates a non-chat type (some providers mark whisper as
// model_type="chat" but endpoint_format="audio").
func providerModelTypeToKind(modelType, endpointFormat string) domain.ModelKind {
	mt := strings.ToLower(modelType)
	ef := strings.ToLower(endpointFormat)
	// endpoint_format is more reliable: it says which API endpoint to use.
	switch {
	case ef == "embedding":
		return domain.KindEmbedding
	case ef == "speech" || strings.Contains(ef, "tts"):
		return domain.KindTTS
	case ef == "audio" || strings.Contains(ef, "transcri"):
		return domain.KindSTT
	case ef == "image":
		return domain.KindImage
	}
	// Fall back to model_type.
	switch {
	case mt == "embedding":
		return domain.KindEmbedding
	case mt == "audio" && (ef == "speech" || strings.Contains(ef, "tts")):
		return domain.KindTTS
	case mt == "audio" && (strings.Contains(ef, "audio") || strings.Contains(ef, "transcri")):
		return domain.KindSTT
	case mt == "image":
		return domain.KindImage
	case mt == "chat" || mt == "llm":
		return domain.KindLLM
	case strings.Contains(mt, "embed"):
		return domain.KindEmbedding
	case strings.Contains(mt, "speech"):
		return domain.KindTTS
	case strings.Contains(mt, "transcri"):
		return domain.KindSTT
	default:
		return ""
	}
}

// mapGorouterToLitellmProvider maps a gorouter provider ID to the
// corresponding litellm_provider value used in the LiteLLM JSON. Most map
// directly; a few differ (vertex→vertex_ai, azure→azure_ai, etc.).
func mapGorouterToLitellmProvider(gorouterID string) string {
	m := map[string]string{
		"openai":     "openai",
		"anthropic":  "anthropic",
		"vertex":     "vertex_ai",
		"bedrock":    "bedrock",
		"azure":      "azure_ai",
		"groq":       "groq",
		"mistral":    "mistral",
		"cohere":     "cohere",
		"deepseek":   "deepseek",
		"together":   "together_ai",
		"fireworks":  "fireworks_ai",
		"perplexity": "perplexity",
		"xai":        "xai",
		"ollama":     "ollama",
		"nvidia":     "nvidia_nim",
		"nebius":     "nebius",
		"ai21":       "ai21",
		"voyage":     "voyage",
		"jina_ai":    "jina_ai",
	}
	if v, ok := m[strings.ToLower(gorouterID)]; ok {
		return v
	}
	return strings.ToLower(gorouterID)
}

// mapOpenRouterProvider maps an OpenRouter model ID prefix to a gorouter
// provider ID. Most are the same; google covers both vertex and gemini.
func mapOpenRouterProvider(orProvider string) string {
	m := map[string]string{
		"google":          "vertex",
		"google-ai-studio": "gemini",
		"meta-llama":      "meta",
		"amazon":          "bedrock",
		"mistralai":       "mistral",
	}
	if v, ok := m[strings.ToLower(orProvider)]; ok {
		return v
	}
	return strings.ToLower(orProvider)
}

// parseLiteLLMPricing extracts pricing fields from a LiteLLM model entry.
// All fields are per-token (USD) as float64.
func parseLiteLLMPricing(v map[string]any) domain.ModelPricing {
	return domain.ModelPricing{
		InputCostPerToken:           floatVal(v["input_cost_per_token"]),
		OutputCostPerToken:          floatVal(v["output_cost_per_token"]),
		InputCostPerTokenBatches:    floatVal(v["input_cost_per_token_batches"]),
		OutputCostPerTokenBatches:   floatVal(v["output_cost_per_token_batches"]),
		CacheReadInputTokenCost:     floatVal(v["cache_read_input_token_cost"]),
		CacheCreationInputTokenCost: floatVal(v["cache_creation_input_token_cost"]),
		InputCostPerTokenAbove128k:  floatVal(v["input_cost_per_token_above_128k_tokens"]),
		InputCostPerTokenAbove200k:  floatVal(v["input_cost_per_token_above_200k_tokens"]),
		OutputCostPerTokenAbove128k: floatVal(v["output_cost_per_token_above_128k_tokens"]),
		OutputCostPerTokenAbove200k: floatVal(v["output_cost_per_token_above_200k_tokens"]),
		OutputCostPerImage:          floatVal(v["output_cost_per_image"]),
		InputCostPerPixel:           floatVal(v["input_cost_per_pixel"]),
		InputCostPerSecond:           floatVal(v["input_cost_per_second"]),
		OutputCostPerSecond:          floatVal(v["output_cost_per_second"]),
		InputCostPerCharacter:        floatVal(v["input_cost_per_character"]),
		OutputCostPerCharacter:       floatVal(v["output_cost_per_character"]),
		InputCostPerQuery:            floatVal(v["input_cost_per_query"]),
	}
}

// parseModelsDevPricing extracts pricing from a models.dev cost object.
// models.dev prices are per-1M-tokens, so we divide by 1e6 to get per-token.
func parseModelsDevPricing(cost map[string]any) domain.ModelPricing {
	return domain.ModelPricing{
		InputCostPerToken:           floatVal(cost["input"]) / 1e6,
		OutputCostPerToken:          floatVal(cost["output"]) / 1e6,
		CacheReadInputTokenCost:     floatVal(cost["cache_read"]) / 1e6,
		CacheCreationInputTokenCost: floatVal(cost["cache_write"]) / 1e6,
	}
}

// parseOpenRouterPricing extracts pricing from an OpenRouter pricing object.
// OpenRouter prices are per-token but stored as strings, so we parse them.
func parseOpenRouterPricing(pricing map[string]any) domain.ModelPricing {
	return domain.ModelPricing{
		InputCostPerToken:           strFloatVal(pricing["prompt"]),
		OutputCostPerToken:          strFloatVal(pricing["completion"]),
		CacheReadInputTokenCost:     strFloatVal(pricing["input_cache_read"]),
		CacheCreationInputTokenCost: strFloatVal(pricing["input_cache_write"]),
	}
}

// floatVal safely extracts a float64 from an any value (JSON numbers come as
// float64).
func floatVal(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// strFloatVal parses a string or float64 to float64 (OpenRouter prices are
// strings).
func strFloatVal(v any) float64 {
	switch n := v.(type) {
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
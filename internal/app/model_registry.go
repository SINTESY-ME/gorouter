package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
type ModelRegistry struct {
	mu       sync.RWMutex
	entries  map[string]registryEntry
	loadedAt time.Time
	ttl      time.Duration
	client   *http.Client
}

type registryEntry struct {
	Kind             domain.ModelKind
	Context          int
	SupportsVision   bool
	SupportsToolCall bool
	SupportsReasoning bool
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Load from each source; failures are non-fatal — we use whatever we get.
	r.loadLiteLLM(ctx, entries)
	r.loadModelsDev(ctx, entries)
	r.loadOpenRouter(ctx, entries)

	r.entries = entries
	r.loadedAt = time.Now()
	return len(entries) > 0
}

func (r *ModelRegistry) loadLiteLLM(ctx context.Context, entries map[string]registryEntry) {
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
		entries[normalizeModelName(k)] = e
	}
}

func (r *ModelRegistry) loadModelsDev(ctx context.Context, entries map[string]registryEntry) {
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
	for _, provider := range data {
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
			if _, exists := entries[key]; exists {
				continue // LiteLLM already has it
			}
			// models.dev is LLM-focused; kind is always llm unless output has image.
			kind := domain.KindLLM
			e := registryEntry{Kind: kind}
			e.SupportsVision, _ = m["attachment"].(bool)
			e.SupportsToolCall, _ = m["tool_call"].(bool)
			e.SupportsReasoning, _ = m["reasoning"].(bool)
			entries[key] = e
		}
	}
}

func (r *ModelRegistry) loadOpenRouter(ctx context.Context, entries map[string]registryEntry) {
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
		if _, exists := entries[key]; exists {
			continue
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
		entries[key] = e
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
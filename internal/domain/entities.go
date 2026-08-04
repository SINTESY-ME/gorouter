// Package domain holds the core entities and ports of gorouter.
//
// This package has no framework or I/O dependencies. It defines the
// vocabulary of the system (Provider, Connection, Combo, ApiKey, Model,
// Usage) and the ports (Repository, Executor, Translator, ModelFetcher)
// that the application layer depends on. Infrastructure implements them.
package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Format identifies a wire format for chat requests/responses. The router
// pivots through OpenAI as the canonical format and translates to/from
// others as needed.
type Format string

const (
	FormatOpenAI    Format = "openai"    // /v1/chat/completions
	FormatAnthropic Format = "anthropic" // /v1/messages
	FormatGemini    Format = "gemini"    // generateContent
	FormatResponses Format = "responses" // /v1/responses
	FormatAuto      Format = "auto"      // auto-detect at probe time
)

// ModelKind distinguishes families of capabilities offered by a model.
type ModelKind string

const (
	KindLLM       ModelKind = "llm"
	KindEmbedding ModelKind = "embedding"
	KindImage     ModelKind = "image"
	KindTTS       ModelKind = "tts"
	KindSTT       ModelKind = "stt"
	KindRerank    ModelKind = "rerank"
	KindOCR       ModelKind = "ocr"
	KindVideo     ModelKind = "video"
)

// Provider is the static registry entry for an upstream provider kind. It
// is configuration, not state. Matches the registry pattern from 9router
// but trimmed to what we use.
type Provider struct {
	ID      string      // stable short id, e.g. "openai", "anthropic", "groq"
	Display string      // human label
	Format  Format      // wire format the provider speaks natively
	BaseURL string      // default base url; a connection may override
	Kind    string      // "openai-compatible" | "anthropic" | "gemini" | "custom"
	Auth    AuthScheme  // how to authenticate
	Models  []ModelSpec // static model list; auto-fetched ones are merged on top
}

// AuthScheme describes how a connection authenticates against its provider.
type AuthScheme string

const (
	AuthBearer  AuthScheme = "bearer"    // Authorization: Bearer <key>
	AuthXAPIKey AuthScheme = "x-api-key" // x-api-key: <key>  (Anthropic)
	AuthNone    AuthScheme = "none"
)

// ModelSpec is a static model declaration inside a Provider registry entry.
type ModelSpec struct {
	ID      string
	Kind    ModelKind
	Context int
}

// Connection is a single credential for a provider. A provider may have
// multiple connections (multi-account / key pool). Connections have priority
// order; the router tries them in order on failure.
type Connection struct {
	ID               string    `json:"id" gorm:"primaryKey"`
	ProviderID       string    `json:"provider_id" gorm:"column:provider_id;uniqueIndex:idx_provider_name,priority:1;index:idx_conn_provider,priority:1"`
	Name             string    `json:"name" gorm:"uniqueIndex:idx_provider_name,priority:2"`
	APIKey           string    `json:"api_key" gorm:"column:api_key"` // access token for oauth
	Priority         int       `json:"priority" gorm:"index:idx_conn_provider,priority:2"`
	IsActive         bool      `json:"is_active" gorm:"column:is_active;default:true"`
	RateLimitedUntil time.Time `json:"rate_limited_until" gorm:"column:rate_limited_until"`
	// OAuth extras (empty for API-key connections).
	RefreshToken   string    `json:"-" gorm:"column:refresh_token"` // never expose in list JSON
	TokenExpiresAt time.Time `json:"token_expires_at,omitempty" gorm:"column:token_expires_at"`
	// Meta is JSON for provider-specific oauth data (project_id, account_id, email…).
	Meta string `json:"meta,omitempty" gorm:"column:meta;type:text"`
	// CreatedBy is the dashboard user ID that owns this connection. Empty
	// means admin-owned (created before multi-user, or by an admin).
	CreatedBy string    `json:"-" gorm:"column:created_by;index"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProviderConfig is a logical grouping of one or more Connections (API
// keys) under a single provider_id. It holds provider-level metadata
// and the load-balance strategy applied to all its connections. The
// ID field matches the provider_id used by Connection.ProviderID.
type ProviderConfig struct {
	ID          string `json:"id" gorm:"primaryKey"`
	Name        string `json:"name"` // human-friendly display name (optional)
	Description string `json:"description,omitempty"`
	BaseURL     string `json:"base_url" gorm:"column:base_url"`
	// ResolvedBaseURL is the base URL ready for consumption, with the
	// version prefix (e.g. /v1) included. It is resolved once by the probe
	// when the first connection is added, and re-resolved whenever BaseURL
	// changes. Consumers (executor, fetcher) use this field directly — no
	// runtime URL resolution. If empty, the provider has not been probed
	// yet; the caller should return an error to the user.
	ResolvedBaseURL string     `json:"resolved_base_url" gorm:"column:resolved_base_url"`
	Format          Format     `json:"format" gorm:"default:openai"`
	Auth            AuthScheme `json:"auth" gorm:"default:bearer"`
	// LoadBalance controls how connections are selected for this provider.
	// "failover" (default): always try the first active connection, only
	// fall through on failure. "round-robin": rotate the starting index
	// across requests to distribute load evenly.
	LoadBalance string `json:"load_balance" gorm:"column:load_balance;default:failover"`
	// CreatedBy is the dashboard user ID that owns this provider. Empty
	// means admin-owned (pre-multi-user or created by an admin).
	CreatedBy string    `json:"-" gorm:"column:created_by;index"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ModelInfo is one model surfaced through /v1/models. Combos appear as
// models with OwnedBy == "combo".
type ModelInfo struct {
	ID      string    `json:"id"`
	Object  string    `json:"object"`   // always "model"
	OwnedBy string    `json:"owned_by"` // provider id, or "combo"
	Kind    ModelKind `json:"kind,omitempty"`
}

// ModelEntry is a persisted model in the catalog. It is populated by sync
// (fetching /v1/models from the provider), by manual addition, or enriched
// with data from external model registries (LiteLLM, models.dev, OpenRouter,
// HuggingFace).
type ModelEntry struct {
	ID                string       `json:"id" gorm:"primaryKey"` // "{providerID}/{modelID}"
	ProviderID        string       `json:"provider_id" gorm:"index;column:provider_id"`
	ModelID           string       `json:"model_id" gorm:"column:model_id"` // without prefix
	Name              string       `json:"name,omitempty"`
	Kind              ModelKind    `json:"kind" gorm:"default:llm;index"`
	Source            string       `json:"source" gorm:"default:sync"` // "sync" | "manual"
	IsActive          bool         `json:"is_active" gorm:"column:is_active;default:true;index"`
	Context           int          `json:"context,omitempty"`
	SupportsVision    bool         `json:"supports_vision,omitempty"`
	SupportsToolCall  bool         `json:"supports_tool_call,omitempty"`
	SupportsReasoning bool         `json:"supports_reasoning,omitempty"`
	Pricing           ModelPricing `json:"pricing,omitempty" gorm:"serializer:json;type:text"`
	LastSyncedAt      time.Time    `json:"last_synced_at,omitempty" gorm:"index"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// ModelPricing holds per-model price data used for cost calculation. All
// per-token fields are in USD per single token (matching LiteLLM's convention).
// Per-image is USD per image, per-second is USD per second, per-character is
// USD per character, per-query is USD per query. Zero means the field is not
// applicable or unknown.
type ModelPricing struct {
	// LLM / chat / embeddings (per-token, USD)
	InputCostPerToken         float64 `json:"input_cost_per_token,omitempty"`
	OutputCostPerToken        float64 `json:"output_cost_per_token,omitempty"`
	InputCostPerTokenBatches  float64 `json:"input_cost_per_token_batches,omitempty"`
	OutputCostPerTokenBatches float64 `json:"output_cost_per_token_batches,omitempty"`

	// Cache (Anthropic/OpenAI prompt caching nativo)
	CacheReadInputTokenCost     float64 `json:"cache_read_input_token_cost,omitempty"`
	CacheCreationInputTokenCost float64 `json:"cache_creation_input_token_cost,omitempty"`

	// Tiered (context-length)
	InputCostPerTokenAbove128k  float64 `json:"input_cost_per_token_above_128k,omitempty"`
	InputCostPerTokenAbove200k  float64 `json:"input_cost_per_token_above_200k,omitempty"`
	OutputCostPerTokenAbove128k float64 `json:"output_cost_per_token_above_128k,omitempty"`
	OutputCostPerTokenAbove200k float64 `json:"output_cost_per_token_above_200k,omitempty"`

	// Image generation (per-image)
	OutputCostPerImage float64 `json:"output_cost_per_image,omitempty"`
	InputCostPerPixel  float64 `json:"input_cost_per_pixel,omitempty"`

	// Audio (per-second)
	InputCostPerSecond  float64 `json:"input_cost_per_second,omitempty"`
	OutputCostPerSecond float64 `json:"output_cost_per_second,omitempty"`

	// TTS (per-character)
	InputCostPerCharacter  float64 `json:"input_cost_per_character,omitempty"`
	OutputCostPerCharacter float64 `json:"output_cost_per_character,omitempty"`

	// Rerank / search (per-query)
	InputCostPerQuery float64 `json:"input_cost_per_query,omitempty"`

	// Metadata
	Source       string    `json:"source,omitempty"` // "litellm" | "openrouter" | "models.dev" | "manual"
	LastSyncedAt time.Time `json:"last_synced_at,omitempty"`
}

// Combo is a named virtual model backed by an ordered fallback list of
// real model ids (e.g. ["openai/gpt-4o", "anthropic/claude-3-opus"]).
type Combo struct {
	ID              string                    `json:"id" gorm:"primaryKey"`
	Name            string                    `json:"name" gorm:"uniqueIndex"`
	Models          []string                  `json:"models" gorm:"serializer:json;type:text"`
	Strategy        string                    `json:"strategy" gorm:"default:ordered_fallback"`
	ModelMeta       map[string]ComboModelMeta `json:"model_meta,omitempty" gorm:"serializer:json;type:text"`
	ClassifierModel string                    `json:"classifier_model,omitempty" gorm:"column:classifier_model"`
	Kind            ModelKind                 `json:"kind,omitempty" gorm:"default:llm"`
	// CreatedBy is the dashboard user ID that owns this combo. Empty means
	// admin-owned.
	CreatedBy string    `json:"-" gorm:"column:created_by;index"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ComboModelMeta holds per-member metadata for combo routing strategies.
type ComboModelMeta struct {
	Weight      int    `json:"weight,omitempty"`
	Description string `json:"description,omitempty"`
}

// ApiKey is a client-facing key created in the dashboard. Clients send it
// as Authorization: Bearer or x-api-key. The plaintext key is returned to
// the caller exactly once at creation time; at rest only the SHA-256 hash
// is stored (in the "key" column, via KeyHash). The Key field is populated
// only for the creation response and is empty on subsequent reads.
type ApiKey struct {
	ID       string `json:"id" gorm:"primaryKey"`
	Key      string `json:"key,omitempty" gorm:"-"`          // plaintext; never persisted (gorm:"-")
	KeyHash  string `json:"-" gorm:"uniqueIndex;column:key"` // SHA-256 hash at rest (col "key")
	Name     string `json:"name"`
	IsActive bool   `json:"is_active" gorm:"column:is_active;default:true"`
	// Limits holds every rate/spend limit applied to this key. An empty
	// (or nil) slice means the key is unlimited.
	Limits []KeyLimit `json:"limits,omitempty" gorm:"serializer:json;type:text"`
	// AllowedModels restricts which models/combos this key may use. Empty
	// (or nil) means all models are allowed. Entries match a model id
	// ("openai/gpt-4o"), a bare model name ("gpt-4o"), or a combo name.
	AllowedModels []string `json:"allowed_models,omitempty" gorm:"serializer:json;type:text"`
	// CreatedBy is the dashboard user ID that owns this key. Empty means
	// admin-owned.
	CreatedBy string    `json:"-" gorm:"column:created_by;index"`
	CreatedAt time.Time `json:"created_at"`
}

// KeyLimitKind distinguishes the two kinds of limits a key can carry.
type KeyLimitKind string

const (
	KeyLimitRate   KeyLimitKind = "rate"   // max requests per rolling window
	KeyLimitBudget KeyLimitKind = "budget" // max USD spend per rolling window
)

// KeyLimit is one limit applied to an API key: a request-rate cap or a
// spend cap over a rolling window. A key can hold any number of limits of
// either kind, all of which must be satisfied (a request is blocked if any
// limit is exceeded). Max is in requests (rate) or USD (budget). Duration
// is a window string parseable by ParseWindowDuration (e.g. "5h", "7d",
// "30d").
type KeyLimit struct {
	ID       string       `json:"id"`
	Kind     KeyLimitKind `json:"kind"`
	Max      float64      `json:"max"`
	Duration string       `json:"duration"`
}

// ParseWindowDuration parses a limit window string. It accepts Go duration
// syntax (e.g. "30m", "5h", "72h") plus day and week suffixes ("7d",
// "2w") that time.ParseDuration does not support. Non-positive and invalid
// values return an error.
func ParseWindowDuration(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	if d, err := time.ParseDuration(trimmed); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return d, nil
	}
	n := len(trimmed)
	if n < 2 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	unit := trimmed[n-1]
	num := trimmed[:n-1]
	var mult time.Duration
	switch unit {
	case 'd':
		mult = 24 * time.Hour
	case 'w':
		mult = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return time.Duration(v * float64(mult)), nil
}

// UsageEntry is a single upstream call's resource accounting. One row per
// real API call (not per combo level). Combo membership is tracked in the
// separate combo_executions table so that nested combos don't duplicate
// tokens/cost in aggregate queries.
type UsageEntry struct {
	ID                  int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	Timestamp           time.Time `json:"timestamp" gorm:"index"`
	Provider            string    `json:"provider"`
	Model               string    `json:"model"`
	ConnectionID        string    `json:"connection_id" gorm:"column:connection_id"`
	ApiKey              string    `json:"api_key,omitempty" gorm:"column:api_key"`             // legacy: plaintext (old rows); empty on new rows
	ApiKeyID            string    `json:"api_key_id,omitempty" gorm:"column:api_key_id;index"` // stable key identity (FK to api_keys.id), or "user:<id>" for dashboard/playground use
	UserID              string    `json:"user_id,omitempty" gorm:"column:user_id;index"`       // dashboard user ID when authenticated via session
	Endpoint            string    `json:"endpoint"`
	LatencyMs           int64     `json:"latency_ms,omitempty"`
	TTFTMs              int64     `json:"ttft_ms,omitempty" gorm:"column:ttft_ms;default:0"`
	PromptTokens        int       `json:"prompt_tokens"`
	CompletionTokens    int       `json:"completion_tokens"`
	CachedTokens        int       `json:"cached_tokens,omitempty"`
	Cost                float64   `json:"cost"`
	Status              int       `json:"status"`
	CacheHit            bool      `json:"cache_hit,omitempty" gorm:"column:cache_hit;default:false"`
	CacheTokensSaved    int       `json:"cache_tokens_saved,omitempty" gorm:"column:cache_tokens_saved;default:0"`
	CacheCostSaved      float64   `json:"cache_cost_saved,omitempty" gorm:"column:cache_cost_saved;default:0"`
	RTKCompressed       bool      `json:"rtk_compressed,omitempty" gorm:"column:rtk_compressed;default:false"`
	RTKBytesSaved       int       `json:"rtk_bytes_saved,omitempty" gorm:"column:rtk_bytes_saved;default:0"`
	RTKTokensSaved      int       `json:"rtk_tokens_saved,omitempty" gorm:"column:rtk_tokens_saved;default:0"`
	RTKCostSaved        float64   `json:"rtk_cost_saved,omitempty" gorm:"column:rtk_cost_saved;default:0"`
	SemanticCacheHit    bool      `json:"semantic_cache_hit,omitempty" gorm:"column:semantic_cache_hit;default:false"`
	SemanticTokensSaved int       `json:"semantic_tokens_saved,omitempty" gorm:"column:semantic_tokens_saved;default:0"`
	SemanticCostSaved   float64   `json:"semantic_cost_saved,omitempty" gorm:"column:semantic_cost_saved;default:0"`
	FallbackReason      string    `json:"fallback_reason,omitempty" gorm:"column:fallback_reason;default:''"`
	RequestID           string    `json:"request_id,omitempty" gorm:"column:request_id;index"`
	Attempt             int       `json:"attempt" gorm:"column:attempt;default:0"`
	Error               string    `json:"error,omitempty" gorm:"column:error;type:text;default:''"`
	ComboChain          []string  `json:"combo_chain,omitempty" gorm:"-"`
}

// ComboExecution records which combos were traversed for a single upstream
// call. One row per combo in the chain (root → ... → leaf). The leaf combo
// is the one that directly contains the model that was called.
type ComboExecution struct {
	ID        int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	UsageID   int64  `json:"usage_id" gorm:"column:usage_id;index"`
	ComboName string `json:"combo_name" gorm:"column:combo_name;index"`
	Depth     int    `json:"depth" gorm:"column:depth"`
	// FallbackReason is set when this combo in the chain was NOT the
	// one initially chosen (e.g. classifier picked "medium" but it
	// failed and execution fell through to this one). Empty when the
	// combo was used directly.
	FallbackReason string `json:"fallback_reason,omitempty" gorm:"column:fallback_reason;default:''"`
}

// Setting is a key-value persisted configuration entry (dashboard password
// hash, health state, etc.). Values are small strings; structured data is
// encoded by the caller.
type Setting struct {
	Key       string `gorm:"primaryKey"`
	Value     string `gorm:"type:text"`
	UpdatedAt time.Time
}

// ModelID is "<provider>/<model>" or "<combo-name>" (no slash). Alias
// resolution turns a combo name into its members.
type ModelID struct {
	Provider string
	Model    string
}

// SplitModelID splits "<provider>/<model>". Returns ok=false if there is
// no slash (likely a combo name).
func SplitModelID(s string) (ModelID, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return ModelID{Provider: s[:i], Model: s[i+1:]}, true
		}
	}
	return ModelID{}, false
}

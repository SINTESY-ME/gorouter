// Package domain holds the core entities and ports of gorouter.
//
// This package has no framework or I/O dependencies. It defines the
// vocabulary of the system (Provider, Connection, Combo, ApiKey, Model,
// Usage) and the ports (Repository, Executor, Translator, ModelFetcher)
// that the application layer depends on. Infrastructure implements them.
package domain

import (
	"time"
)

// Format identifies a wire format for chat requests/responses. The router
// pivots through OpenAI as the canonical format and translates to/from
// others as needed.
type Format string

const (
	FormatOpenAI    Format = "openai"     // /v1/chat/completions
	FormatAnthropic Format = "anthropic" // /v1/messages
	FormatGemini    Format = "gemini"     // generateContent
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
	ID      string        // stable short id, e.g. "openai", "anthropic", "groq"
	Display string        // human label
	Format  Format        // wire format the provider speaks natively
	BaseURL string        // default base url; a connection may override
	Kind    string        // "openai-compatible" | "anthropic" | "gemini" | "custom"
	Auth    AuthScheme    // how to authenticate
	Models  []ModelSpec   // static model list; auto-fetched ones are merged on top
}

// AuthScheme describes how a connection authenticates against its provider.
type AuthScheme string

const (
	AuthBearer  AuthScheme = "bearer"   // Authorization: Bearer <key>
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
	ID               string       `json:"id" gorm:"primaryKey"`
	ProviderID       string       `json:"provider_id" gorm:"column:provider_id;uniqueIndex:idx_provider_name,priority:1;index:idx_conn_provider,priority:1"`
	Name             string       `json:"name" gorm:"uniqueIndex:idx_provider_name,priority:2"`
	APIKey           string       `json:"api_key" gorm:"column:api_key"` // access token for oauth
	Priority         int          `json:"priority" gorm:"index:idx_conn_provider,priority:2"`
	IsActive         bool         `json:"is_active" gorm:"column:is_active;default:true"`
	RateLimitedUntil time.Time    `json:"rate_limited_until" gorm:"column:rate_limited_until"`
	// OAuth extras (empty for API-key connections).
	RefreshToken   string    `json:"-" gorm:"column:refresh_token"` // never expose in list JSON
	TokenExpiresAt time.Time `json:"token_expires_at,omitempty" gorm:"column:token_expires_at"`
	// Meta is JSON for provider-specific oauth data (project_id, account_id, email…).
	Meta      string    `json:"meta,omitempty" gorm:"column:meta;type:text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProviderConfig is a logical grouping of one or more Connections (API
// keys) under a single provider_id. It holds provider-level metadata
// and the load-balance strategy applied to all its connections. The
// ID field matches the provider_id used by Connection.ProviderID.
type ProviderConfig struct {
	ID          string     `json:"id" gorm:"primaryKey"`
	Name        string     `json:"name"`        // human-friendly display name (optional)
	Description string     `json:"description,omitempty"`
	BaseURL     string     `json:"base_url" gorm:"column:base_url"`
	Format      Format     `json:"format" gorm:"default:openai"`
	Auth        AuthScheme `json:"auth" gorm:"default:bearer"`
	// LoadBalance controls how connections are selected for this provider.
	// "failover" (default): always try the first active connection, only
	// fall through on failure. "round-robin": rotate the starting index
	// across requests to distribute load evenly.
	LoadBalance string    `json:"load_balance" gorm:"column:load_balance;default:failover"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ModelInfo is one model surfaced through /v1/models. Combos appear as
// models with OwnedBy == "combo".
type ModelInfo struct {
	ID      string    `json:"id"`
	Object  string    `json:"object"`  // always "model"
	OwnedBy string    `json:"owned_by"` // provider id, or "combo"
	Kind    ModelKind `json:"kind,omitempty"`
}

// ModelEntry is a persisted model in the catalog. It is populated by sync
// (fetching /v1/models from the provider), by manual addition, or enriched
// with data from external model registries (LiteLLM, models.dev, OpenRouter,
// HuggingFace).
type ModelEntry struct {
	ID                string    `json:"id" gorm:"primaryKey"` // "{providerID}/{modelID}"
	ProviderID        string    `json:"provider_id" gorm:"index;column:provider_id"`
	ModelID           string    `json:"model_id" gorm:"column:model_id"` // without prefix
	Name              string    `json:"name,omitempty"`
	Kind              ModelKind `json:"kind" gorm:"default:llm;index"`
	Source            string    `json:"source" gorm:"default:sync"` // "sync" | "manual"
	IsActive          bool      `json:"is_active" gorm:"column:is_active;default:true;index"`
	Context           int       `json:"context,omitempty"`
	SupportsVision    bool      `json:"supports_vision,omitempty"`
	SupportsToolCall  bool      `json:"supports_tool_call,omitempty"`
	SupportsReasoning bool      `json:"supports_reasoning,omitempty"`
	Pricing           ModelPricing `json:"pricing,omitempty" gorm:"serializer:json;type:text"`
	LastSyncedAt      time.Time `json:"last_synced_at,omitempty" gorm:"index"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
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
	InputCostPerSecond   float64 `json:"input_cost_per_second,omitempty"`
	OutputCostPerSecond  float64 `json:"output_cost_per_second,omitempty"`

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
	ID        string    `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"uniqueIndex"`
	Models    []string  `json:"models" gorm:"serializer:json;type:text"`
	Strategy  string    `json:"strategy" gorm:"default:ordered_fallback"`
	Kind      ModelKind `json:"kind,omitempty" gorm:"default:llm"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ApiKey is a client-facing key created in the dashboard. Clients send it
// as Authorization: Bearer or x-api-key.
type ApiKey struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	Key          string    `json:"key" gorm:"uniqueIndex"`
	Name         string    `json:"name"`
	IsActive     bool      `json:"is_active" gorm:"column:is_active;default:true"`
	RateLimitRPM int       `json:"rate_limit_rpm" gorm:"column:rate_limit_rpm;default:0"`
	CreatedAt    time.Time `json:"created_at"`
}

// UsageEntry is a single request's resource accounting.
type UsageEntry struct {
	ID                int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	Timestamp         time.Time `json:"timestamp" gorm:"index"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	ComboName         string    `json:"combo_name,omitempty" gorm:"column:combo_name;default:''"`
	ConnectionID      string    `json:"connection_id" gorm:"column:connection_id"`
	ApiKey            string    `json:"api_key,omitempty" gorm:"column:api_key"`
	Endpoint          string    `json:"endpoint"`
	LatencyMs         int64     `json:"latency_ms,omitempty"`
	PromptTokens      int       `json:"prompt_tokens"`
	CompletionTokens  int       `json:"completion_tokens"`
	CachedTokens      int       `json:"cached_tokens,omitempty"`
	Cost              float64   `json:"cost"`
	Status            int       `json:"status"` // http status
}

// Setting is a key-value persisted configuration entry (dashboard password
// hash, health state, etc.). Values are small strings; structured data is
// encoded by the caller.
type Setting struct {
	Key       string    `gorm:"primaryKey"`
	Value     string    `gorm:"type:text"`
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
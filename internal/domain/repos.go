package domain

import (
	"context"
	"time"
)

// ConnectionRepo persists provider connections (credentials).
type ConnectionRepo interface {
	List(ctx context.Context) ([]Connection, error)
	ListByProvider(ctx context.Context, providerID string) ([]Connection, error)
	Get(ctx context.Context, id string) (*Connection, error)
	Create(ctx context.Context, c *Connection) error
	Update(ctx context.Context, c *Connection) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, orderedIDs []string) error
	SetRateLimited(ctx context.Context, id string, until time.Time) error
}

// ProviderConfigRepo persists provider metadata (grouping of connections).
type ProviderConfigRepo interface {
	List(ctx context.Context) ([]ProviderConfig, error)
	Get(ctx context.Context, id string) (*ProviderConfig, error)
	GetByProviderID(ctx context.Context, providerID string) (*ProviderConfig, error)
	Create(ctx context.Context, p *ProviderConfig) error
	Update(ctx context.Context, p *ProviderConfig) error
	Delete(ctx context.Context, id string) error
}

// ComboRepo persists combos (virtual models).
type ComboRepo interface {
	List(ctx context.Context) ([]Combo, error)
	Get(ctx context.Context, id string) (*Combo, error)
	GetByName(ctx context.Context, name string) (*Combo, error)
	Create(ctx context.Context, c *Combo) error
	Update(ctx context.Context, c *Combo) error
	Delete(ctx context.Context, id string) error
}

// ApiKeyRepo persists client API keys.
type ApiKeyRepo interface {
	List(ctx context.Context) ([]ApiKey, error)
	Create(ctx context.Context, k *ApiKey) error
	Update(ctx context.Context, k *ApiKey) error
	Delete(ctx context.Context, id string) error
	Validate(ctx context.Context, key string) (bool, error)
	GetByKey(ctx context.Context, key string) (*ApiKey, error) // nil if not found
}

// UsageRepo records and aggregates request usage.
type UsageRepo interface {
	Record(ctx context.Context, e UsageEntry) error
	// Stats returns aggregated totals for a time range. Period is one of
	// "24h", "7d", "30d".
	Stats(ctx context.Context, period string) (*UsageStats, error)
	// History returns raw entries, newest first, limited.
	History(ctx context.Context, limit int) ([]UsageEntry, error)
	// ModelStats returns per-model aggregate stats (avg TPS, avg latency, requests).
	ModelStats(ctx context.Context) (map[string]*ModelStat, error)
}

// ModelRepo persists the model catalog (synced + manual entries).
type ModelRepo interface {
	List(ctx context.Context) ([]ModelEntry, error)
	ListByProvider(ctx context.Context, providerID string) ([]ModelEntry, error)
	ListActive(ctx context.Context) ([]ModelEntry, error)
	Get(ctx context.Context, id string) (*ModelEntry, error)
	Upsert(ctx context.Context, m *ModelEntry) error
	Delete(ctx context.Context, id string) error
	SetActive(ctx context.Context, id string, active bool) error
	// DeactivateStaleSync marks inactive any sync-source entries for the
	// given provider whose IDs are not in activeIDs. Manual entries are
	// always preserved.
	DeactivateStaleSync(ctx context.Context, providerID string, activeIDs []string) error
}

// SettingRepo persists key-value settings (dashboard password hash, health
// state, etc.). Get returns ("", nil) when the key does not exist.
type SettingRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	Has(ctx context.Context, key string) (bool, error)
}

// UsageStats is the aggregated dashboard summary.
type UsageStats struct {
	Requests         int                `json:"requests"`
	PromptTokens     int                `json:"prompt_tokens"`
	CompletionTokens int                `json:"completion_tokens"`
	Cost             float64            `json:"cost"`
	ByProvider       map[string]int     `json:"by_provider"`     // -> requests
	ByModel          map[string]int     `json:"by_model"`
	ByModelCost      map[string]float64 `json:"by_model_cost"`
	ByApiKey         map[string]int     `json:"by_api_key"`
	Daily            []UsageDailyPoint  `json:"daily"`
}

// UsageDailyPoint is one bucket of a time series.
type UsageDailyPoint struct {
	Date     string `json:"date"`
	Requests int    `json:"requests"`
	Tokens   int    `json:"tokens"`
	Cost     float64 `json:"cost"`
}

// ModelStat is per-model aggregate performance data.
type ModelStat struct {
	AvgTPS      float64 `json:"avg_tps"`
	AvgLatencyMs int64  `json:"avg_latency_ms"`
	Requests    int     `json:"requests"`
}
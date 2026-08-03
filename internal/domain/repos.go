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

// UsageStatsQuery specifies the time range and bucket granularity for a
// usage stats query. When From is zero, the preset period string is used
// instead. When To is zero, now (exclusive) is used. When Bucket is empty,
// the repository auto-selects based on the range width.
type UsageStatsQuery struct {
	From   time.Time // inclusive lower bound; zero = use period preset
	To     time.Time // exclusive upper bound; zero = now
	Period string    // preset: "24h", "7d", "30d", "60d", "1h" — used when From is zero
	Bucket string    // "hour", "minute", "5m", "30m", "day"; empty = auto
	ApiKey string    // filter by raw api key; empty = all keys
}

// HistoryQuery specifies filters for the logs/history endpoint. All fields
// are optional; empty/zero values mean "no filter on this field".
type HistoryQuery struct {
	From    time.Time // inclusive; zero = no lower bound
	To      time.Time // exclusive; zero = no upper bound
	Model   string    // exact model name; empty = all
	Combo   string    // combo name in the chain; empty = all
	ApiKey  string    // raw api key; empty = all
	Search  string    // substring match on model/provider/endpoint; empty = all
	Limit   int       // legacy: max groups to return; ignored when PerPage > 0
	Page    int       // 1-based page number; 0 = page 1
	PerPage int       // groups per page; 0 = use Limit or default 100
}

// HistoryResult is a paginated page of usage history groups. Each entry in
// Data is a raw UsageEntry; callers group by request_id (newest attempt is
// the primary row, older attempts are children). Total is the total number
// of request groups matching the filters (not just this page).
type HistoryResult struct {
	Data    []UsageEntry `json:"data"`
	Total   int          `json:"total"`
	Page    int          `json:"page"`
	PerPage int          `json:"per_page"`
	HasMore bool         `json:"has_more"`
}

// HistoryFilters lists the distinct models, combos, and providers seen in
// usage entries, for populating the logs filter dropdowns.
type HistoryFilters struct {
	Models    []string `json:"models"`
	Combos    []string `json:"combos"`
	Providers []string `json:"providers"`
}

// UsageRepo records and aggregates request usage.
type UsageRepo interface {
	// Record inserts a usage entry and its associated combo_executions
	// rows (derived from entry.ComboChain) in a single transaction.
	// The entry's ID is populated on success.
	Record(ctx context.Context, e *UsageEntry) error
	// Stats returns aggregated totals + a time-series for the given query.
	Stats(ctx context.Context, q UsageStatsQuery) (*UsageStats, error)
	// History returns raw entries matching the query, newest first.
	// Each entry includes the combo chain (populated from combo_executions).
	History(ctx context.Context, q HistoryQuery) (*HistoryResult, error)
	DistinctHistoryFilters(ctx context.Context, search string) (*HistoryFilters, error)
	// ModelStats returns per-model aggregate stats (avg TPS, avg latency, requests).
	ModelStats(ctx context.Context) (map[string]*ModelStat, error)
	// ModelStatsByID is like ModelStats but keyed by the full "provider/model"
	// identifier so callers can match combo members unambiguously.
	ModelStatsByID(ctx context.Context) (map[string]*ModelStat, error)
	// SavingsStats returns aggregated savings (cache + RTK) for a time range.
	SavingsStats(ctx context.Context, period string, apiKey string) (*SavingsAgg, error)
}

// ModelRepo persists the model catalog (synced + manual entries).
type ModelRepo interface {
	List(ctx context.Context) ([]ModelEntry, error)
	ListByProvider(ctx context.Context, providerID string) ([]ModelEntry, error)
	ListActive(ctx context.Context) ([]ModelEntry, error)
	Get(ctx context.Context, id string) (*ModelEntry, error)
	Upsert(ctx context.Context, m *ModelEntry) error
	// UpsertBatch upserts multiple entries in a single transaction. Used by
	// the model sync to avoid N individual queries. Entries missing
	// CreatedAt get the current time; existing rows keep their CreatedAt.
	UpsertBatch(ctx context.Context, entries []*ModelEntry) error
	Delete(ctx context.Context, id string) error
	SetActive(ctx context.Context, id string, active bool) error
	// DeactivateStaleSync marks inactive any sync-source entries for the
	// given provider whose IDs are not in activeIDs. Manual entries are
	// always preserved.
	DeactivateStaleSync(ctx context.Context, providerID string, activeIDs []string) error
	// ReactivateSync re-enables sync-source entries for the given provider
	// whose IDs are in activeIDs. Used when the API lists a model that was
	// previously deactivated because it had disappeared.
	ReactivateSync(ctx context.Context, providerID string, activeIDs []string) error
}

// SettingRepo persists key-value settings (dashboard password hash, health
// state, etc.). Get returns ("", nil) when the key does not exist.
type SettingRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	Has(ctx context.Context, key string) (bool, error)
}

// UsageStats is the aggregated dashboard summary. Bucket indicates the
// granularity of the Daily series ("hour", "minute", "5m", "30m", "day").
type UsageStats struct {
	Requests         int                `json:"requests"`
	PromptTokens     int                `json:"prompt_tokens"`
	CompletionTokens int                `json:"completion_tokens"`
	Cost             float64            `json:"cost"`
	ByProvider       map[string]int     `json:"by_provider"`
	ByModel          map[string]int     `json:"by_model"`
	ByModelCost      map[string]float64 `json:"by_model_cost"`
	ByApiKey         map[string]int     `json:"by_api_key"`
	ByCombo          map[string]int     `json:"by_combo,omitempty"`
	ByComboTokens    map[string]int     `json:"by_combo_tokens,omitempty"`
	ByComboCost      map[string]float64 `json:"by_combo_cost,omitempty"`
	ByEndpoint       map[string]int     `json:"by_endpoint,omitempty"`
	Daily            []UsageDailyPoint  `json:"daily"`
	Bucket           string             `json:"bucket"`

	// Performance — average TTFT, generation TPS, total latency across all
	// requests in the period. Computed only over successful requests
	// (status < 400) with valid timings, so the averages aren't skewed by
	// upstream failures that complete in <1ms.
	AvgTTFTMs    int64   `json:"avg_ttft_ms"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	AvgTPS       float64 `json:"avg_tps"`

	// Latency percentiles (milliseconds). Computed the same way as the
	// averages. Zero means no successful requests.
	P50LatencyMs int64 `json:"p50_latency_ms"`
	P95LatencyMs int64 `json:"p95_latency_ms"`
	P99LatencyMs int64 `json:"p99_latency_ms"`

	// Reliability — counts of all requests, successful (status < 400),
	// errored (status >= 400). ErrorRate is errors / total.
	SuccessfulRequests int     `json:"successful_requests"`
	ErrorRequests      int     `json:"error_requests"`
	ErrorRate          float64 `json:"error_rate"`

	// ComboRequests counts entries that have at least one row in
	// combo_executions (i.e. the request was routed through a combo).
	ComboRequests int     `json:"combo_requests"`
	CacheHitRate  float64 `json:"cache_hit_rate"`
	CacheHits     int64   `json:"cache_hits"`
	TokensSaved   int64   `json:"tokens_saved"`
	CostSaved     float64 `json:"cost_saved"`

	// Efficiency — tokens per dollar (1k tokens per $1). For inputs and
	// outputs separately so users can see which side is expensive.
	TokensPerDollar float64 `json:"tokens_per_dollar"`
	CostPerRequest  float64 `json:"cost_per_request"`
}

// UsageDailyPoint is one bucket of a time series.
type UsageDailyPoint struct {
	Date     string  `json:"date"`
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
	Cost     float64 `json:"cost"`
	Errors   int     `json:"errors,omitempty"`
	AvgTPS   float64 `json:"avg_tps,omitempty"`
}

// SavingsAgg is the aggregated savings summary for a time range. Each type
// (cache, RTK) has its own counters so the dashboard can show them
// separately. Future saving mechanisms can add their own fields.
type SavingsAgg struct {
	CacheHits        int64   `json:"cache_hits"`
	CacheTokensSaved int64   `json:"cache_tokens_saved"`
	CacheCostSaved   float64 `json:"cache_cost_saved"`
	RTKCompressions  int64   `json:"rtk_compressions"`
	RTKBytesSaved    int64   `json:"rtk_bytes_saved"`
	RTKTokensSaved   int64   `json:"rtk_tokens_saved"`
	RTKCostSaved     float64 `json:"rtk_cost_saved"`
}

// ModelStat is per-model aggregate performance data.
type ModelStat struct {
	AvgTPS       float64 `json:"avg_tps"`
	AvgTTFTMs    int64   `json:"avg_ttft_ms"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	Requests     int     `json:"requests"`
}

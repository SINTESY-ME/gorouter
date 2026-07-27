package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// UsageRepo implements domain.UsageRepo via GORM.
type UsageRepo struct{ db *gorm.DB }

func NewUsageRepo(db *gorm.DB) *UsageRepo { return &UsageRepo{db: db} }

func (r *UsageRepo) Record(ctx context.Context, e domain.UsageEntry) error {
	return r.db.WithContext(ctx).Create(&e).Error
}

func (r *UsageRepo) Stats(ctx context.Context, q domain.UsageStatsQuery) (*domain.UsageStats, error) {
	from, to, err := resolveRange(q)
	if err != nil {
		return nil, err
	}
	bucket := q.Bucket
	if bucket == "" {
		bucket = autoBucket(from, to)
	}
	s := &domain.UsageStats{
		ByProvider:  map[string]int{},
		ByModel:     map[string]int{},
		ByModelCost: map[string]float64{},
		ByApiKey:    map[string]int{},
		Bucket:      bucket,
	}
	tx := r.db.WithContext(ctx).Model(&domain.UsageEntry{})
	if q.ApiKey != "" {
		tx = tx.Where("api_key = ?", q.ApiKey)
	}
	// Totals
	var totals struct {
		Requests        int
		PromptTokens    int
		CompletionTokens int
		Cost            float64
	}
	if err := tx.Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("COUNT(*) as requests, COALESCE(SUM(prompt_tokens), 0) as prompt_tokens, COALESCE(SUM(completion_tokens), 0) as completion_tokens, COALESCE(SUM(cost), 0) as cost").
		Scan(&totals).Error; err != nil {
		return nil, err
	}
	s.Requests = totals.Requests
	s.PromptTokens = totals.PromptTokens
	s.CompletionTokens = totals.CompletionTokens
	s.Cost = totals.Cost

	// By provider
	type groupRow struct{ Key string; Count int }
	var provRows []groupRow
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("provider as key, COUNT(*) as count").Group("provider").Scan(&provRows).Error; err != nil {
		return nil, err
	}
	for _, row := range provRows {
		s.ByProvider[row.Key] = row.Count
	}

	// By model
	var modelRows []groupRow
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("model as key, COUNT(*) as count").Group("model").Scan(&modelRows).Error; err != nil {
		return nil, err
	}
	for _, row := range modelRows {
		s.ByModel[row.Key] = row.Count
	}

	// By model cost
	type costRow struct {
		Key   string
		Cost  float64
	}
	var costRows []costRow
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("model as key, COALESCE(SUM(cost), 0) as cost").Group("model").Scan(&costRows).Error; err != nil {
		return nil, err
	}
	for _, row := range costRows {
		s.ByModelCost[row.Key] = row.Cost
	}

	// By api_key (non-empty only)
	var keyRows []groupRow
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ? AND api_key != ''", from, to).
		Select("api_key as key, COUNT(*) as count").Group("api_key").Scan(&keyRows).Error; err != nil {
		return nil, err
	}
	for _, row := range keyRows {
		s.ByApiKey[row.Key] = row.Count
	}

	// Time series with dynamic bucket
	series, err := r.timeseries(ctx, from, to, bucket, q.ApiKey)
	if err != nil {
		return nil, err
	}
	s.Daily = series
	return s, nil
}

// resolveRange computes (from, to) from a UsageStatsQuery. When q.From is
// non-zero, the custom range is used directly. Otherwise q.Period is
// resolved via periodStart. To defaults to now when zero. Both bounds are
// computed from the same now() call so the duration is exact (not off by
// the nanoseconds between two time.Now() calls — which would push autoBucket
// into the wrong tier).
func resolveRange(q domain.UsageStatsQuery) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := q.From
	to := q.To
	if from.IsZero() {
		dur, err := periodDuration(q.Period)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = now.Add(-dur)
	}
	if to.IsZero() {
		to = now
	}
	return from, to, nil
}

// periodDuration returns the duration for a preset period string.
func periodDuration(period string) (time.Duration, error) {
	switch period {
	case "", "24h":
		return 24 * time.Hour, nil
	case "1h":
		return time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	case "60d":
		return 60 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("%w: unknown period %q", domain.ErrValidation, period)
	}
}

// autoBucket selects a reasonable bucket size based on the time range width.
// Uses a small margin (1 second) so that exact durations like 24h or 1h
// don't fall into the wrong tier due to sub-second clock jitter.
func autoBucket(from, to time.Time) string {
	dur := to.Sub(from)
	margin := time.Second
	switch {
	case dur <= time.Hour+margin:
		return "minute"
	case dur <= 24*time.Hour+margin:
		return "hour"
	default:
		return "day"
	}
}

// bucketExpr returns the SQL expression for bucketing by the given granularity.
// Supported: "minute", "5m", "30m", "hour", "day".
func bucketExpr(bucket string, isPostgres bool) string {
	if !isPostgres {
		// SQLite strftime
		switch bucket {
		case "minute":
			return "strftime('%Y-%m-%dT%H:%M', timestamp)"
		case "5m":
			return "strftime('%Y-%m-%dT%H:%M', datetime(timestamp, 'unixepoch', ('-' || (strftime('%M', timestamp) % 5) || ' minutes'), 'unixepoch'))"
		case "30m":
			return "strftime('%Y-%m-%dT%H:%M', datetime(timestamp, 'unixepoch', ('-' || (strftime('%M', timestamp) % 30) || ' minutes'), 'unixepoch'))"
		case "hour":
			return "strftime('%Y-%m-%dT%H:00', timestamp)"
		default:
			return "date(timestamp)"
		}
	}
	// Postgres date_trunc + to_char
	switch bucket {
	case "minute":
		return "to_char(date_trunc('minute', timestamp), 'YYYY-MM-DD\"T\"HH24:MI')"
	case "5m":
		return "to_char(date_trunc('hour', timestamp) + INTERVAL '5 min' * FLOOR(EXTRACT(minute FROM timestamp) / 5), 'YYYY-MM-DD\"T\"HH24:MI')"
	case "30m":
		return "to_char(date_trunc('hour', timestamp) + INTERVAL '30 min' * FLOOR(EXTRACT(minute FROM timestamp) / 30), 'YYYY-MM-DD\"T\"HH24:MI')"
	case "hour":
		return "to_char(date_trunc('hour', timestamp), 'YYYY-MM-DD\"T\"HH24:00')"
	default:
		return "to_char(date_trunc('day', timestamp), 'YYYY-MM-DD')"
	}
}

func (r *UsageRepo) timeseries(ctx context.Context, from, to time.Time, bucket string, apiKey string) ([]domain.UsageDailyPoint, error) {
	isPg := r.db.Dialector.Name() == "postgres"
	dateExpr := bucketExpr(bucket, isPg)
	var rows []struct {
		Date     string
		Requests int
		Tokens   int
		Cost     float64
	}
	tx := r.db.WithContext(ctx).Model(&domain.UsageEntry{})
	if apiKey != "" {
		tx = tx.Where("api_key = ?", apiKey)
	}
	if err := tx.Where("timestamp >= ? AND timestamp < ?", from, to).
		Select(dateExpr + " as date, COUNT(*) as requests, COALESCE(SUM(prompt_tokens + completion_tokens), 0) as tokens, COALESCE(SUM(cost), 0) as cost").
		Group(dateExpr).Order("date").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.UsageDailyPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.UsageDailyPoint{
			Date:     row.Date,
			Requests: row.Requests,
			Tokens:   row.Tokens,
			Cost:     row.Cost,
		})
	}
	return out, nil
}

func (r *UsageRepo) History(ctx context.Context, limit int) ([]domain.UsageEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var entries []domain.UsageEntry
	err := r.db.WithContext(ctx).Order("timestamp DESC").Limit(limit).Find(&entries).Error
	return entries, err
}

func (r *UsageRepo) ModelStats(ctx context.Context) (map[string]*domain.ModelStat, error) {
	var rows []struct {
		Model        string
		AvgTPS       float64
		AvgTTFTMs    int64
		AvgLatencyMs int64
		Requests     int
	}
	err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).
		Where("status < 400 AND completion_tokens > 0 AND latency_ms > 0").
		Select(`model,
			AVG(CASE WHEN ttft_ms > 0 AND latency_ms > ttft_ms
				THEN completion_tokens * 1000.0 / (latency_ms - ttft_ms)
				ELSE completion_tokens * 1000.0 / latency_ms
			END) as avg_tps,
			AVG(ttft_ms) as avg_ttft_ms,
			AVG(latency_ms) as avg_latency_ms,
			COUNT(*) as requests`).
		Group("model").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*domain.ModelStat, len(rows))
	for _, row := range rows {
		out[row.Model] = &domain.ModelStat{
			AvgTPS:       row.AvgTPS,
			AvgTTFTMs:    row.AvgTTFTMs,
			AvgLatencyMs: row.AvgLatencyMs,
			Requests:     row.Requests,
		}
	}
	return out, nil
}

// ModelStatsByID aggregates per-model performance keyed by "provider/model".
func (r *UsageRepo) ModelStatsByID(ctx context.Context) (map[string]*domain.ModelStat, error) {
	var rows []struct {
		ID           string
		AvgTPS       float64
		AvgTTFTMs    int64
		AvgLatencyMs int64
		Requests     int
	}
	err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).
		Where("status < 400 AND completion_tokens > 0 AND latency_ms > 0").
		Select(`provider || '/' || model as id,
			AVG(CASE WHEN ttft_ms > 0 AND latency_ms > ttft_ms
				THEN completion_tokens * 1000.0 / (latency_ms - ttft_ms)
				ELSE completion_tokens * 1000.0 / latency_ms
			END) as avg_tps,
			AVG(ttft_ms) as avg_ttft_ms,
			AVG(latency_ms) as avg_latency_ms,
			COUNT(*) as requests`).
		Group("provider, model").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]*domain.ModelStat, len(rows))
	for _, row := range rows {
		out[row.ID] = &domain.ModelStat{
			AvgTPS:       row.AvgTPS,
			AvgTTFTMs:    row.AvgTTFTMs,
			AvgLatencyMs: row.AvgLatencyMs,
			Requests:     row.Requests,
		}
	}
	return out, nil
}

// SavingsStats aggregates cache and RTK savings from usage_entries for a
// given time range. Each type is summed independently so the dashboard can
// show them separately.
func (r *UsageRepo) SavingsStats(ctx context.Context, period string, apiKey string) (*domain.SavingsAgg, error) {
	since, err := periodStart(period)
	if err != nil {
		return nil, err
	}
	var row struct {
		CacheHits        int64
		CacheTokensSaved int64
		CacheCostSaved   float64
		RTKCompressions  int64
		RTKBytesSaved    int64
		RTKTokensSaved   int64
		RTKCostSaved     float64
	}
	tx := r.db.WithContext(ctx).Model(&domain.UsageEntry{})
	if apiKey != "" {
		tx = tx.Where("api_key = ?", apiKey)
	}
	err = tx.Where("timestamp >= ?", since).
		Select(`
			COALESCE(SUM(CASE WHEN cache_hit THEN 1 ELSE 0 END), 0) as cache_hits,
			COALESCE(SUM(cache_tokens_saved), 0) as cache_tokens_saved,
			COALESCE(SUM(cache_cost_saved), 0) as cache_cost_saved,
			COALESCE(SUM(CASE WHEN rtk_compressed THEN 1 ELSE 0 END), 0) as rtk_compressions,
			COALESCE(SUM(rtk_bytes_saved), 0) as rtk_bytes_saved,
			COALESCE(SUM(rtk_tokens_saved), 0) as rtk_tokens_saved,
			COALESCE(SUM(rtk_cost_saved), 0) as rtk_cost_saved
		`).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	return &domain.SavingsAgg{
		CacheHits:        row.CacheHits,
		CacheTokensSaved: row.CacheTokensSaved,
		CacheCostSaved:   row.CacheCostSaved,
		RTKCompressions:  row.RTKCompressions,
		RTKBytesSaved:    row.RTKBytesSaved,
		RTKTokensSaved:   row.RTKTokensSaved,
		RTKCostSaved:     row.RTKCostSaved,
	}, nil
}

func periodStart(period string) (time.Time, error) {
	now := time.Now().UTC()
	switch period {
	case "", "24h":
		return now.Add(-24 * time.Hour), nil
	case "1h":
		return now.Add(-1 * time.Hour), nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), nil
	case "60d":
		return now.Add(-60 * 24 * time.Hour), nil
	default:
		return time.Time{}, fmt.Errorf("%w: unknown period %q", domain.ErrValidation, period)
	}
}
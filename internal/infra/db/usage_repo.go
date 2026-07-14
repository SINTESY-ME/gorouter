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

func (r *UsageRepo) Stats(ctx context.Context, period string) (*domain.UsageStats, error) {
	since, err := periodStart(period)
	if err != nil {
		return nil, err
	}
	s := &domain.UsageStats{
		ByProvider:  map[string]int{},
		ByModel:     map[string]int{},
		ByModelCost: map[string]float64{},
		ByApiKey:    map[string]int{},
	}
	// Totals
	var totals struct {
		Requests        int
		PromptTokens    int
		CompletionTokens int
		Cost            float64
	}
	if err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).Where("timestamp >= ?", since).
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
	if err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).Where("timestamp >= ?", since).
		Select("provider as key, COUNT(*) as count").Group("provider").Scan(&provRows).Error; err != nil {
		return nil, err
	}
	for _, row := range provRows {
		s.ByProvider[row.Key] = row.Count
	}

	// By model
	var modelRows []groupRow
	if err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).Where("timestamp >= ?", since).
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
	if err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).Where("timestamp >= ?", since).
		Select("model as key, COALESCE(SUM(cost), 0) as cost").Group("model").Scan(&costRows).Error; err != nil {
		return nil, err
	}
	for _, row := range costRows {
		s.ByModelCost[row.Key] = row.Cost
	}

	// By api_key (non-empty only)
	var keyRows []groupRow
	if err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).Where("timestamp >= ? AND api_key != ''", since).
		Select("api_key as key, COUNT(*) as count").Group("api_key").Scan(&keyRows).Error; err != nil {
		return nil, err
	}
	for _, row := range keyRows {
		s.ByApiKey[row.Key] = row.Count
	}

	// Daily bucketing (driver-specific date expression)
	daily, err := r.daily(ctx, since)
	if err != nil {
		return nil, err
	}
	s.Daily = daily
	return s, nil
}

func (r *UsageRepo) daily(ctx context.Context, since time.Time) ([]domain.UsageDailyPoint, error) {
	// Bucket by day. SQLite: date(timestamp), Postgres: date_trunc('day', timestamp)
	var dateExpr string
	if r.db.Dialector.Name() == "postgres" {
		dateExpr = "to_char(date_trunc('day', timestamp), 'YYYY-MM-DD')"
	} else {
		dateExpr = "date(timestamp)"
	}
	var rows []struct {
		Date     string
		Requests int
		Tokens   int
		Cost     float64
	}
	if err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).Where("timestamp >= ?", since).
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
		AvgLatencyMs int64
		Requests     int
	}
	err := r.db.WithContext(ctx).Model(&domain.UsageEntry{}).
		Where("status < 400 AND completion_tokens > 0 AND latency_ms > 0").
		Select(`model,
			AVG(completion_tokens * 1000.0 / latency_ms) as avg_tps,
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
			AvgLatencyMs: row.AvgLatencyMs,
			Requests:     row.Requests,
		}
	}
	return out, nil
}

func periodStart(period string) (time.Time, error) {
	now := time.Now().UTC()
	switch period {
	case "", "24h":
		return now.Add(-24 * time.Hour), nil
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
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

// Record inserts a usage entry and its combo_executions rows in a single
// transaction. The entry's ComboChain (gorm:"-") is used to derive the
// combo_executions rows. The entry's ID is populated on success.
func (r *UsageRepo) Record(ctx context.Context, e *domain.UsageEntry) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(e).Error; err != nil {
			return err
		}
		if len(e.ComboChain) == 0 {
			return nil
		}
		executions := make([]domain.ComboExecution, len(e.ComboChain))
		for i, name := range e.ComboChain {
			executions[i] = domain.ComboExecution{
				UsageID:   e.ID,
				ComboName: name,
				Depth:     i,
			}
		}
		return tx.Create(&executions).Error
	})
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
		ByProvider:    map[string]int{},
		ByModel:       map[string]int{},
		ByModelCost:   map[string]float64{},
		ByApiKey:      map[string]int{},
		ByCombo:       map[string]int{},
		ByComboTokens: map[string]int{},
		ByComboCost:   map[string]float64{},
		ByEndpoint:    map[string]int{},
		Bucket:        bucket,
	}
	tx := r.db.WithContext(ctx).Model(&domain.UsageEntry{})
	if q.ApiKey != "" {
		tx = tx.Where("api_key = ?", q.ApiKey)
	}
	// Totals + success/error split
	var totals struct {
		Requests         int64
		PromptTokens     int64
		CompletionTokens int64
		Cost             float64
		Successful       int64
		Errors           int64
		CacheHits        int64
		ComboReqs        int64
		CostSaved        float64
		TokensSaved      int64
	}
	if err := tx.Where("timestamp >= ? AND timestamp < ?", from, to).
		Select(`
			COUNT(DISTINCT request_id) as requests,
			COALESCE(SUM(CASE WHEN status < 400 THEN prompt_tokens ELSE 0 END), 0) as prompt_tokens,
			COALESCE(SUM(CASE WHEN status < 400 THEN completion_tokens ELSE 0 END), 0) as completion_tokens,
			COALESCE(SUM(CASE WHEN status < 400 THEN cost ELSE 0 END), 0) as cost,
			COUNT(DISTINCT CASE WHEN status < 400 THEN request_id END) as successful,
			COUNT(DISTINCT request_id) - COUNT(DISTINCT CASE WHEN status < 400 THEN request_id END) as errors,
			COALESCE(SUM(CASE WHEN cache_hit THEN 1 ELSE 0 END), 0) as cache_hits,
			COUNT(DISTINCT CASE WHEN id IN (SELECT DISTINCT usage_id FROM combo_executions) THEN request_id END) as combo_reqs,
			COALESCE(SUM(cache_cost_saved + rtk_cost_saved), 0) as cost_saved,
			COALESCE(SUM(cache_tokens_saved + rtk_tokens_saved), 0) as tokens_saved
		`).
		Scan(&totals).Error; err != nil {
		return nil, err
	}
	s.Requests = int(totals.Requests)
	s.PromptTokens = int(totals.PromptTokens)
	s.CompletionTokens = int(totals.CompletionTokens)
	s.Cost = totals.Cost
	s.SuccessfulRequests = int(totals.Successful)
	s.ErrorRequests = int(totals.Errors)
	s.ComboRequests = int(totals.ComboReqs)
	s.CacheHits = totals.CacheHits
	s.CostSaved = totals.CostSaved
	s.TokensSaved = totals.TokensSaved
	if s.Requests > 0 {
		s.ErrorRate = float64(s.ErrorRequests) / float64(s.Requests)
		s.CacheHitRate = float64(s.CacheHits) / float64(s.Requests)
		s.CostPerRequest = s.Cost / float64(s.Requests)
		totalTokens := s.PromptTokens + s.CompletionTokens
		if totalTokens > 0 && s.Cost > 0 {
			s.TokensPerDollar = float64(totalTokens) / (s.Cost * 1000)
		}
	}

	// Performance averages — only over successful requests with valid timings
	var perf struct {
		AvgTTFTMs    float64
		AvgLatencyMs float64
		AvgTPS       float64
	}
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ? AND status < 400", from, to).
		Select(`
			COALESCE(AVG(ttft_ms), 0) as avg_ttft_ms,
			COALESCE(AVG(latency_ms), 0) as avg_latency_ms,
			COALESCE(AVG(CASE WHEN ttft_ms > 0 AND latency_ms > ttft_ms
				THEN completion_tokens * 1000.0 / (latency_ms - ttft_ms)
				WHEN latency_ms > 0 AND completion_tokens > 0
				THEN completion_tokens * 1000.0 / latency_ms
				ELSE 0 END), 0) as avg_tps
		`).
		Scan(&perf).Error; err != nil {
		return nil, err
	}
	s.AvgTTFTMs = int64(perf.AvgTTFTMs)
	s.AvgLatencyMs = int64(perf.AvgLatencyMs)
	s.AvgTPS = perf.AvgTPS
	// Percentiles — Postgres uses percentile_cont, SQLite uses a manual
	// approximation (avg of 95th/99th value ordered + offset). Both run in
	// separate queries because the percentile aggregate syntaxes differ.
	if r.db.Dialector.Name() == "postgres" {
		var pct struct {
			P50, P95, P99 float64
		}
		if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ? AND status < 400 AND latency_ms > 0", from, to).
			Raw(`SELECT
				percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms) AS p50,
				percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) AS p95,
				percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms) AS p99
				FROM usage_entries WHERE timestamp >= ? AND timestamp < ? AND status < 400 AND latency_ms > 0`, from, to).
			Scan(&pct).Error; err != nil {
			return nil, err
		}
		s.P50LatencyMs = int64(pct.P50)
		s.P95LatencyMs = int64(pct.P95)
		s.P99LatencyMs = int64(pct.P99)
	} else {
		// SQLite: compute percentiles in Go from a sample of latencies.
		// Pull a bounded sample (5000 rows) to keep this fast.
		var samples []int64
		if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ? AND status < 400 AND latency_ms > 0", from, to).
			Order("latency_ms").
			Limit(5000).
			Pluck("latency_ms", &samples).Error; err != nil {
			return nil, err
		}
		s.P50LatencyMs = percentile(samples, 0.50)
		s.P95LatencyMs = percentile(samples, 0.95)
		s.P99LatencyMs = percentile(samples, 0.99)
	}

	// By provider
	type groupRow struct{ Key string; Count int64 }
	var provRows []groupRow
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("provider as key, COUNT(*) as count").Group("provider").Scan(&provRows).Error; err != nil {
		return nil, err
	}
	for _, row := range provRows {
		s.ByProvider[row.Key] = int(row.Count)
	}

	// By model
	var modelRows []groupRow
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("model as key, COUNT(*) as count").Group("model").Scan(&modelRows).Error; err != nil {
		return nil, err
	}
	for _, row := range modelRows {
		s.ByModel[row.Key] = int(row.Count)
	}

	// By model cost
	type costRow struct {
		Key  string
		Cost float64
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
		s.ByApiKey[row.Key] = int(row.Count)
	}

	// By combo — JOIN combo_executions so every combo in the chain
	// (parent + child) gets credit for the request.
	var comboRows []groupRow
	comboTx := r.db.WithContext(ctx).Table("combo_executions").
		Joins("JOIN usage_entries ON usage_entries.id = combo_executions.usage_id").
		Where("usage_entries.timestamp >= ? AND usage_entries.timestamp < ? AND usage_entries.status < 400", from, to)
	if q.ApiKey != "" {
		comboTx = comboTx.Where("usage_entries.api_key = ?", q.ApiKey)
	}
	if err := comboTx.
		Group("combo_executions.combo_name").
		Select("combo_executions.combo_name as key, COUNT(*) as count").
		Scan(&comboRows).Error; err != nil {
		return nil, err
	}
	for _, row := range comboRows {
		s.ByCombo[row.Key] = int(row.Count)
	}

	// By combo tokens — sum of prompt+completion tokens per combo (via JOIN).
	type comboTokenRow struct {
		Key    string
		Tokens int64
	}
	var comboTokenRows []comboTokenRow
	comboTokenTx := r.db.WithContext(ctx).Table("combo_executions").
		Joins("JOIN usage_entries ON usage_entries.id = combo_executions.usage_id").
		Where("usage_entries.timestamp >= ? AND usage_entries.timestamp < ? AND usage_entries.status < 400", from, to)
	if q.ApiKey != "" {
		comboTokenTx = comboTokenTx.Where("usage_entries.api_key = ?", q.ApiKey)
	}
	if err := comboTokenTx.
		Group("combo_executions.combo_name").
		Select("combo_executions.combo_name as key, COALESCE(SUM(usage_entries.prompt_tokens + usage_entries.completion_tokens), 0) as tokens").
		Scan(&comboTokenRows).Error; err != nil {
		return nil, err
	}
	for _, row := range comboTokenRows {
		s.ByComboTokens[row.Key] = int(row.Tokens)
	}

	// By combo cost — sum of cost per combo (via JOIN).
	type comboCostRow struct {
		Key  string
		Cost float64
	}
	var comboCostRows []comboCostRow
	comboCostTx := r.db.WithContext(ctx).Table("combo_executions").
		Joins("JOIN usage_entries ON usage_entries.id = combo_executions.usage_id").
		Where("usage_entries.timestamp >= ? AND usage_entries.timestamp < ? AND usage_entries.status < 400", from, to)
	if q.ApiKey != "" {
		comboCostTx = comboCostTx.Where("usage_entries.api_key = ?", q.ApiKey)
	}
	if err := comboCostTx.
		Group("combo_executions.combo_name").
		Select("combo_executions.combo_name as key, COALESCE(SUM(usage_entries.cost), 0) as cost").
		Scan(&comboCostRows).Error; err != nil {
		return nil, err
	}
	for _, row := range comboCostRows {
		s.ByComboCost[row.Key] = row.Cost
	}

	// By endpoint
	var endpointRows []groupRow
	if err := tx.Session(&gorm.Session{}).Where("timestamp >= ? AND timestamp < ?", from, to).
		Select("endpoint as key, COUNT(*) as count").Group("endpoint").Scan(&endpointRows).Error; err != nil {
		return nil, err
	}
	for _, row := range endpointRows {
		s.ByEndpoint[row.Key] = int(row.Count)
	}

	// Time series with dynamic bucket (includes errors + TPS for the chart)
	series, err := r.timeseries(ctx, from, to, bucket, q.ApiKey)
	if err != nil {
		return nil, err
	}
	s.Daily = series
	return s, nil
}

// percentile returns the nearest-rank percentile of an ordered sample.
// The sample MUST be sorted ascending. Returns 0 for empty samples.
func percentile(sample []int64, p float64) int64 {
	if len(sample) == 0 {
		return 0
	}
	idx := int(float64(len(sample)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sample) {
		idx = len(sample) - 1
	}
	return sample[idx]
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
		Requests int64
		Tokens   int64
		Cost     float64
		Errors   int64
		AvgTPS   float64
	}
	tx := r.db.WithContext(ctx).Model(&domain.UsageEntry{})
	if apiKey != "" {
		tx = tx.Where("api_key = ?", apiKey)
	}
	if err := tx.Where("timestamp >= ? AND timestamp < ?", from, to).
		Select(dateExpr + ` as date,
			COUNT(DISTINCT request_id) as requests,
			COALESCE(SUM(CASE WHEN status < 400 THEN prompt_tokens + completion_tokens ELSE 0 END), 0) as tokens,
			COALESCE(SUM(CASE WHEN status < 400 THEN cost ELSE 0 END), 0) as cost,
			COALESCE(SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END), 0) as errors,
			COALESCE(AVG(CASE WHEN status < 400 AND ttft_ms > 0 AND latency_ms > ttft_ms
				THEN completion_tokens * 1000.0 / (latency_ms - ttft_ms)
				WHEN status < 400 AND latency_ms > 0 AND completion_tokens > 0
				THEN completion_tokens * 1000.0 / latency_ms
				ELSE NULL END), 0) as avg_tps`).
		Group(dateExpr).Order("date").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.UsageDailyPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.UsageDailyPoint{
			Date:     row.Date,
			Requests: int(row.Requests),
			Tokens:   int(row.Tokens),
			Cost:     row.Cost,
			Errors:   int(row.Errors),
			AvgTPS:   row.AvgTPS,
		})
	}
	return out, nil
}

func (r *UsageRepo) History(ctx context.Context, q domain.HistoryQuery) ([]domain.UsageEntry, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	tx := r.db.WithContext(ctx).Model(&domain.UsageEntry{})
	if !q.From.IsZero() {
		tx = tx.Where("timestamp >= ?", q.From)
	}
	if !q.To.IsZero() {
		tx = tx.Where("timestamp < ?", q.To)
	}
	if q.Model != "" {
		tx = tx.Where("model = ?", q.Model)
	}
	if q.ApiKey != "" {
		tx = tx.Where("api_key = ?", q.ApiKey)
	}
	if q.Search != "" {
		like := "%" + q.Search + "%"
		tx = tx.Where("model LIKE ? OR provider LIKE ? OR endpoint LIKE ?", like, like, like)
	}
	if q.Combo != "" {
		tx = tx.Where("id IN (SELECT usage_id FROM combo_executions WHERE combo_name = ?)", q.Combo)
	}
	type reqRow struct {
		RequestID string
		Latest    time.Time
	}
	var reqRows []reqRow
	if err := tx.Select("request_id, MAX(timestamp) as latest").
		Group("request_id").
		Order("latest DESC").
		Limit(limit).
		Scan(&reqRows).Error; err != nil {
		return nil, err
	}
	if len(reqRows) == 0 {
		return nil, nil
	}
	ids := make([]string, len(reqRows))
	for i, rr := range reqRows {
		ids[i] = rr.RequestID
	}
	var entries []domain.UsageEntry
	if err := r.db.WithContext(ctx).Where("request_id IN ?", ids).Order("request_id, attempt").Find(&entries).Error; err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return entries, nil
	}
	entryIDs := make([]int64, len(entries))
	for i := range entries {
		entryIDs[i] = entries[i].ID
	}
	var execs []domain.ComboExecution
	r.db.WithContext(ctx).Where("usage_id IN ?", entryIDs).Order("usage_id, depth").Find(&execs)
	byUsage := map[int64][]string{}
	for _, ce := range execs {
		byUsage[ce.UsageID] = append(byUsage[ce.UsageID], ce.ComboName)
	}
	for i := range entries {
		entries[i].ComboChain = byUsage[entries[i].ID]
	}
	return entries, nil
}

func (r *UsageRepo) ModelStats(ctx context.Context) (map[string]*domain.ModelStat, error) {
	var rows []struct {
		Model        string
		AvgTPS       float64
		AvgTTFTMs    float64
		AvgLatencyMs float64
		Requests     int64
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
			AvgTTFTMs:    int64(row.AvgTTFTMs),
			AvgLatencyMs: int64(row.AvgLatencyMs),
			Requests:     int(row.Requests),
		}
	}
	return out, nil
}

// ModelStatsByID aggregates per-model performance keyed by "provider/model".
func (r *UsageRepo) ModelStatsByID(ctx context.Context) (map[string]*domain.ModelStat, error) {
	var rows []struct {
		ID           string
		AvgTPS       float64
		AvgTTFTMs    float64
		AvgLatencyMs float64
		Requests     int64
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
			AvgTTFTMs:    int64(row.AvgTTFTMs),
			AvgLatencyMs: int64(row.AvgLatencyMs),
			Requests:     int(row.Requests),
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
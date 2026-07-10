package app

import (
	"context"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// asyncUsageRecorder wraps a UsageRepo with a buffered channel so that
// Record() calls on the hot path (teeReadCloser.Close) never block on a
// database INSERT. Entries are drained by a single background goroutine
// that batches inserts when the channel has accumulated entries.
//
// If the channel is full (should only happen under extreme load or during
// shutdown), the entry is dropped rather than blocking the request path.
type AsyncUsageRecorder struct {
	repo    domain.UsageRepo
	entries chan domain.UsageEntry
	wg      sync.WaitGroup
}

const usageChanSize = 4096

func NewAsyncUsageRecorder(repo domain.UsageRepo) *AsyncUsageRecorder {
	r := &AsyncUsageRecorder{
		repo:    repo,
		entries: make(chan domain.UsageEntry, usageChanSize),
	}
	r.wg.Add(1)
	go r.drain()
	return r
}

func (r *AsyncUsageRecorder) Record(ctx context.Context, e domain.UsageEntry) error {
	select {
	case r.entries <- e:
	default:
		// Channel full — drop the entry rather than blocking the hot path.
	}
	return nil
}

func (r *AsyncUsageRecorder) drain() {
	defer r.wg.Done()
	ctx := context.Background()
	for e := range r.entries {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = r.repo.Record(cctx, e)
		cancel()
	}
}

func (r *AsyncUsageRecorder) Stats(ctx context.Context, period string) (*domain.UsageStats, error) {
	return r.repo.Stats(ctx, period)
}

func (r *AsyncUsageRecorder) History(ctx context.Context, limit int) ([]domain.UsageEntry, error) {
	return r.repo.History(ctx, limit)
}

func (r *AsyncUsageRecorder) ModelStats(ctx context.Context) (map[string]*domain.ModelStat, error) {
	return r.repo.ModelStats(ctx)
}

// Close drains pending entries. Call during graceful shutdown.
func (r *AsyncUsageRecorder) Close() {
	close(r.entries)
	r.wg.Wait()
}
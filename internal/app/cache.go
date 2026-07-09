package app

import (
	"context"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// apiKeyCache wraps an ApiKeyRepo with a short-TTL in-memory cache for
// GetByKey — the hot-path lookup on every /v1/* request when RequireKey is
// on. Reads are lock-free on the fast path (single entry, RWMutex RLock).
// Writes (Create/Update/Delete) invalidate the cache so the dashboard never
// serves stale keys.
type apiKeyCache struct {
	repo domain.ApiKeyRepo

	mu       sync.RWMutex
	byKey    map[string]*cachedKey
	ttl      time.Duration
}

type cachedKey struct {
	key      *domain.ApiKey
	expiresAt time.Time
}

func NewApiKeyCache(repo domain.ApiKeyRepo, ttl time.Duration) domain.ApiKeyRepo {
	return &apiKeyCache{
		repo:  repo,
		byKey: make(map[string]*cachedKey),
		ttl:   ttl,
	}
}

func (c *apiKeyCache) GetByKey(ctx context.Context, key string) (*domain.ApiKey, error) {
	now := time.Now()
	c.mu.RLock()
	if e, ok := c.byKey[key]; ok && e.expiresAt.After(now) {
		c.mu.RUnlock()
		return e.key, nil
	}
	c.mu.RUnlock()

	ak, err := c.repo.GetByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.byKey[key] = &cachedKey{key: ak, expiresAt: now.Add(c.ttl)}
	c.mu.Unlock()
	return ak, nil
}

func (c *apiKeyCache) invalidate(key string) {
	c.mu.Lock()
	delete(c.byKey, key)
	c.mu.Unlock()
}

func (c *apiKeyCache) invalidateAll() {
	c.mu.Lock()
	c.byKey = make(map[string]*cachedKey)
	c.mu.Unlock()
}

func (c *apiKeyCache) List(ctx context.Context) ([]domain.ApiKey, error) {
	return c.repo.List(ctx)
}

func (c *apiKeyCache) Create(ctx context.Context, k *domain.ApiKey) error {
	if err := c.repo.Create(ctx, k); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}

func (c *apiKeyCache) Update(ctx context.Context, k *domain.ApiKey) error {
	if err := c.repo.Update(ctx, k); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}

func (c *apiKeyCache) Delete(ctx context.Context, id string) error {
	if err := c.repo.Delete(ctx, id); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}

func (c *apiKeyCache) Validate(ctx context.Context, key string) (bool, error) {
	ak, err := c.GetByKey(ctx, key)
	if err != nil {
		return false, err
	}
	return ak != nil && ak.IsActive, nil
}

// connCache wraps a ConnectionRepo with a TTL cache for ListByProvider —
// the hot-path lookup that runs on every chat/passthrough request. The full
// provider list is cached as one snapshot keyed by providerID. Writes
// invalidate the entire cache (the dataset is small).
type connCache struct {
	repo domain.ConnectionRepo

	mu     sync.RWMutex
	byProv map[string]*cachedConns
	ttl    time.Duration
}

type cachedConns struct {
	conns    []domain.Connection
	expiresAt time.Time
}

func NewConnCache(repo domain.ConnectionRepo, ttl time.Duration) domain.ConnectionRepo {
	return &connCache{
		repo:   repo,
		byProv: make(map[string]*cachedConns),
		ttl:    ttl,
	}
}

func (c *connCache) ListByProvider(ctx context.Context, providerID string) ([]domain.Connection, error) {
	now := time.Now()
	c.mu.RLock()
	if e, ok := c.byProv[providerID]; ok && e.expiresAt.After(now) {
		c.mu.RUnlock()
		return e.conns, nil
	}
	c.mu.RUnlock()

	conns, err := c.repo.ListByProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.byProv[providerID] = &cachedConns{conns: conns, expiresAt: now.Add(c.ttl)}
	c.mu.Unlock()
	return conns, nil
}

func (c *connCache) invalidateAll() {
	c.mu.Lock()
	c.byProv = make(map[string]*cachedConns)
	c.mu.Unlock()
}

func (c *connCache) List(ctx context.Context) ([]domain.Connection, error) {
	return c.repo.List(ctx)
}

func (c *connCache) Get(ctx context.Context, id string) (*domain.Connection, error) {
	return c.repo.Get(ctx, id)
}

func (c *connCache) Create(ctx context.Context, conn *domain.Connection) error {
	if err := c.repo.Create(ctx, conn); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}

func (c *connCache) Update(ctx context.Context, conn *domain.Connection) error {
	if err := c.repo.Update(ctx, conn); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}

func (c *connCache) Delete(ctx context.Context, id string) error {
	if err := c.repo.Delete(ctx, id); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}

func (c *connCache) Reorder(ctx context.Context, orderedIDs []string) error {
	if err := c.repo.Reorder(ctx, orderedIDs); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}

func (c *connCache) SetRateLimited(ctx context.Context, id string, until interface{}) error {
	if err := c.repo.SetRateLimited(ctx, id, until); err != nil {
		return err
	}
	c.invalidateAll()
	return nil
}
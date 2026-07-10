package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// Refresher renews OAuth tokens on connections before upstream calls.
type Refresher struct {
	Manager *Manager
	Repo    domain.ConnectionRepo
	mu      sync.Mutex
	locks   map[string]*sync.Mutex // per connection
}

// EnsureAccess refreshes the connection's access token if needed.
// Mutates conn in place and persists when refreshed.
func (r *Refresher) EnsureAccess(ctx context.Context, conn *domain.Connection) error {
	if conn == nil || conn.RefreshToken == "" {
		return nil
	}
	// Refresh if expired or within 2 minutes.
	if !conn.TokenExpiresAt.IsZero() && time.Until(conn.TokenExpiresAt) > 2*time.Minute {
		return nil
	}
	if r.Manager == nil || r.Repo == nil {
		return nil
	}
	lock := r.lockFor(conn.ID)
	lock.Lock()
	defer lock.Unlock()

	// re-check after lock
	fresh, err := r.Repo.Get(ctx, conn.ID)
	if err == nil && fresh != nil {
		*conn = *fresh
		if !conn.TokenExpiresAt.IsZero() && time.Until(conn.TokenExpiresAt) > 2*time.Minute {
			return nil
		}
	}

	tok, err := r.Manager.Refresh(ctx, conn.ProviderID, conn.RefreshToken)
	if err != nil {
		return fmt.Errorf("oauth refresh %s: %w", conn.ProviderID, err)
	}
	conn.APIKey = tok.AccessToken
	if tok.RefreshToken != "" {
		conn.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		conn.TokenExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	meta := ParseMeta(conn.Meta)
	if meta == nil {
		meta = map[string]string{}
	}
	if tok.Email != "" {
		meta["email"] = tok.Email
	}
	if tok.ProjectID != "" {
		meta["project_id"] = tok.ProjectID
	}
	if tok.AccountID != "" {
		meta["account_id"] = tok.AccountID
	}
	if b, err := json.Marshal(meta); err == nil {
		conn.Meta = string(b)
	}
	return r.Repo.Update(ctx, conn)
}

func (r *Refresher) lockFor(id string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = make(map[string]*sync.Mutex)
	}
	if r.locks[id] == nil {
		r.locks[id] = &sync.Mutex{}
	}
	return r.locks[id]
}

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/redis"
)

// HealthProber owns the background probing of unhealthy (model, connection)
// pairs. It depends on HealthTracker for state, the ConnectionSelector for
// provider config, and the Executor/Translator to make real upstream calls.
// Nil-safe: a nil HealthProber disables probing.
type HealthProber struct {
	Health      *HealthTracker
	Connections domain.ConnectionRepo
	Executor    domain.Executor
	Translator  domain.Translator
	Selector    *ConnectionSelector
	// Shared, when set, coordinates probes across instances via Redis: only
	// one instance probes a failing pair and all share the result. Nil makes
	// each instance probe independently.
	Shared *redis.SharedProbe
}

func NewHealthProber(health *HealthTracker, conns domain.ConnectionRepo, exec domain.Executor, tr domain.Translator, sel *ConnectionSelector) *HealthProber {
	return &HealthProber{
		Health:      health,
		Connections: conns,
		Executor:    exec,
		Translator:  tr,
		Selector:    sel,
	}
}

// sharedResultWait bounds how long an instance waits for another instance's
// probe result before releasing its local in-flight flag and letting the next
// request retry (eventual consistency).
const sharedResultWait = 5 * time.Second

// waitForSharedResult polls Redis for another instance's fresh probe result,
// applying it locally when found.
func (h *HealthProber) waitForSharedResult(modelStr, connID string) bool {
	deadline := time.Now().Add(sharedResultWait)
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if healthy, ok := h.Shared.FreshResult(context.Background(), modelStr, connID); ok {
			if healthy {
				h.Health.MarkHealthy(modelStr, connID)
			} else {
				h.Health.ProbeFailed(modelStr, connID)
			}
			return true
		}
	}
	return false
}

// RunProbe sends a minimal chat request to an unhealthy (model, connection)
// pair to check if the key has recovered. On 2xx it marks healthy; otherwise
// it clears the probe-in-flight flag so the next request can launch a new
// probe. Does not record usage. When Shared is set, probes are coordinated
// across instances via a Redis lock and shared results.
func (h *HealthProber) RunProbe(modelStr string, m domain.ModelID, connID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, probeCtxKey{}, true)

	// Shared coordination: reuse a fresh result from another instance, or
	// acquire the probe lock before running the (expensive) probe ourselves.
	if h.Shared != nil {
		if healthy, ok := h.Shared.FreshResult(ctx, modelStr, connID); ok {
			if healthy {
				h.Health.MarkHealthy(modelStr, connID)
			} else {
				h.Health.ProbeFailed(modelStr, connID)
			}
			return
		}
		if !h.Shared.AcquireLock(ctx, modelStr, connID) {
			// Another instance is probing; use its result when it lands.
			if h.waitForSharedResult(modelStr, connID) {
				return
			}
			h.Health.ProbeFailed(modelStr, connID)
			return
		}
		defer h.Shared.ReleaseLock(context.Background(), modelStr, connID)
	}

	conns, err := h.Connections.ListByProvider(ctx, m.Provider)
	if err != nil || len(conns) == 0 {
		h.Health.ProbeFailed(modelStr, connID)
		slog.Debug("health probe: no connections for provider", "model", modelStr, "conn", connID)
		return
	}
	var conn *domain.Connection
	for i := range conns {
		if conns[i].ID == connID {
			conn = &conns[i]
			break
		}
	}
	if conn == nil {
		h.Health.ProbeFailed(modelStr, connID)
		slog.Debug("health probe: connection not found", "model", modelStr, "conn", connID)
		return
	}
	if !conn.IsActive {
		h.Health.ProbeFailed(modelStr, connID)
		slog.Debug("health probe: connection inactive", "model", modelStr, "conn", connID)
		return
	}

	probeBody := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"."}],"max_tokens":1,"stream":false}`, m.Model))
	cfg := h.Selector.Config(m.Provider)
	targetFmt := cfg.Format
	if targetFmt == "" || targetFmt == domain.FormatAuto {
		targetFmt = domain.FormatOpenAI
	}
	translated, err := h.Translator.TranslateRequest(domain.FormatOpenAI, targetFmt, m.Model, probeBody)
	if err != nil {
		h.Health.ProbeFailed(modelStr, connID)
		slog.Debug("health probe: translate failed", "model", modelStr, "conn", connID, "error", err)
		return
	}
	execReq := domain.ExecuteRequest{
		ProviderID:    m.Provider,
		Connection:    conn,
		Config:        cfg,
		UpstreamModel: m.Model,
		Body:          io.NopCloser(bytes.NewReader(translated)),
		Stream:        false,
	}
	res, err := h.Executor.Execute(ctx, execReq)
	if err != nil {
		h.Health.ProbeFailed(modelStr, connID)
		slog.Debug("health probe: execute failed", "model", modelStr, "conn", connID, "error", err)
		return
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)

	if res.StatusCode >= 200 && res.StatusCode < 400 {
		h.Health.MarkHealthy(modelStr, connID)
		if h.Shared != nil {
			h.Shared.StoreResult(context.Background(), modelStr, connID, true)
		}
		slog.Info("health probe: connection recovered", "model", modelStr, "conn", connID)
	} else {
		h.Health.ProbeFailed(modelStr, connID)
		if h.Shared != nil {
			h.Shared.StoreResult(context.Background(), modelStr, connID, false)
		}
		slog.Debug("health probe: still unhealthy", "model", modelStr, "conn", connID, "status", res.StatusCode)
	}
}

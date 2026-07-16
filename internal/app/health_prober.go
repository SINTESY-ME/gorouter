package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// HealthProber owns the background probing of unhealthy (combo, model,
// connection) triples. It depends on HealthTracker for state, the
// ConnectionSelector for provider config, and the Executor/Translator to
// make real upstream calls. Nil-safe: a nil HealthProber disables probing.
type HealthProber struct {
	Health      *HealthTracker
	Connections domain.ConnectionRepo
	Executor    domain.Executor
	Translator  domain.Translator
	Selector    *ConnectionSelector
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

// LaunchProbes starts background probes for all unhealthy connections that
// don't already have a probe in flight.
func (h *HealthProber) LaunchProbes(comboName, modelStr string, m domain.ModelID, conns []domain.Connection) {
	for i := range conns {
		conn := &conns[i]
		if !conn.IsActive {
			continue
		}
		if h.Health.IsUnhealthy(comboName, modelStr, conn.ID) {
			if h.Health.TryStartProbe(comboName, modelStr, conn.ID) {
				go h.RunProbe(comboName, modelStr, m, conn.ID)
			}
		}
	}
}

// RunProbe sends a minimal chat request to an unhealthy triple to check if
// the key has recovered. On 2xx it marks healthy; otherwise it clears the
// probe-in-flight flag so the next request can launch a new probe. Does
// not record usage.
func (h *HealthProber) RunProbe(comboName, modelStr string, m domain.ModelID, connID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, probeCtxKey{}, true)

	conns, err := h.Connections.ListByProvider(ctx, m.Provider)
	if err != nil || len(conns) == 0 {
		h.Health.ProbeFailed(comboName, modelStr, connID)
		slog.Debug("health probe: no connections for provider", "combo", comboName, "model", modelStr, "conn", connID)
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
		h.Health.ProbeFailed(comboName, modelStr, connID)
		slog.Debug("health probe: connection not found", "combo", comboName, "model", modelStr, "conn", connID)
		return
	}
	if !conn.IsActive || conn.RateLimitedUntil.After(time.Now()) {
		h.Health.ProbeFailed(comboName, modelStr, connID)
		slog.Debug("health probe: connection inactive or rate-limited", "combo", comboName, "model", modelStr, "conn", connID)
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
		h.Health.ProbeFailed(comboName, modelStr, connID)
		slog.Debug("health probe: translate failed", "combo", comboName, "model", modelStr, "conn", connID, "error", err)
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
		h.Health.ProbeFailed(comboName, modelStr, connID)
		slog.Debug("health probe: execute failed", "combo", comboName, "model", modelStr, "conn", connID, "error", err)
		return
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)

	if res.StatusCode >= 200 && res.StatusCode < 400 {
		h.Health.MarkHealthy(comboName, modelStr, connID)
		slog.Info("health probe: connection recovered", "combo", comboName, "model", modelStr, "conn", connID)
	} else {
		h.Health.ProbeFailed(comboName, modelStr, connID)
		slog.Debug("health probe: still unhealthy", "combo", comboName, "model", modelStr, "conn", connID, "status", res.StatusCode)
	}
}
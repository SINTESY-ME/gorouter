package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// The /mcp endpoint runs Gateway.Sync on EVERY incoming request (see
// handleMCPGateway), so concurrent JSON-RPC messages from agents race against
// each other. Run with -race: this test must stay clean.
func TestGatewayConcurrentSyncAndHandleMessage(t *testing.T) {
	url, closeSrv := startTestMCPServer(t)
	defer closeSrv()

	m := NewManager(newFakeRepo())
	cfg := &domain.MCPClient{
		ID:             "c1",
		Name:           "local",
		ConnectionType: domain.MCPTypeHTTP,
		URL:            url,
		AuthType:       domain.MCPAuthNone,
		ToolsToExecute: []string{"*"},
	}
	m.mu.Lock()
	st := m.registerLocked(cfg)
	m.mu.Unlock()
	m.dial(st)

	gw := NewGateway(m, "1.0.0")
	ctx := context.Background()
	// Deliberately do NOT sync first: every concurrent request registers the
	// same tool, which is what two agents hitting /mcp at the same time do.

	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	})

	var wg sync.WaitGroup
	deadline := time.Now().Add(300 * time.Millisecond)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				gw.Sync(ctx)
			}
		}()
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				_ = gw.Server().HandleMessage(ctx, msg)
			}
		}()
	}
	wg.Wait()
}

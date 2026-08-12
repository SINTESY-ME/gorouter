// Package mcp implements domain.MCPManager: dialing upstream MCP servers
// (Streamable HTTP, SSE, stdio), discovering and caching their tools, syncing
// them in the background, and executing tool calls. It also owns the
// aggregated MCP gateway server (see gateway.go) that exposes every discovered
// tool behind a single /mcp endpoint.
//
// The MCP wire protocol is provided by github.com/mark3labs/mcp-go; this
// package owns the lifecycle: connect → initialize → list tools → sync →
// call tool, plus the in-memory registry that maps prefixed tool names back to
// their owning client.
package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// Defaults for client lifecycle. Tuned to keep startup fast (bounded
// initialize handshake) while not hammering upstream servers during sync.
const (
	// DialTimeout bounds the initialize handshake of a client.
	DialTimeout = 30 * time.Second
	// ToolSyncTimeout bounds a single tools/list refresh.
	ToolSyncTimeout = 10 * time.Second
	// DefaultSyncInterval is used when MCPClient.SyncSeconds is 0.
	DefaultSyncInterval = 10 * time.Minute
	// HealthInterval is the reconnect check interval for clients whose
	// connection drops.
	HealthInterval = 30 * time.Second
)

// clientState holds a registered client's live connection and tool cache.
type clientState struct {
	cfg       *domain.MCPClient
	conn      mcpClient
	state     domain.MCPConnectionState
	lastError string
	tools     map[string]domain.MCPTool // keyed by prefixed name
	lastSync  time.Time
	mu        sync.RWMutex
}

// Manager implements domain.MCPManager. All registered clients are tracked in
// a single registry guarded by a RWMutex; background sync/reconnect loops run
// per client until Close.
type Manager struct {
	repo domain.MCPClientRepo
	// now is injectable for tests.
	now func() time.Time

	mu      sync.RWMutex
	clients map[string]*clientState

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager builds a Manager. repo is used only to reload configs on
// reconnect; the live registry is populated by AddClient/Start.
func NewManager(repo domain.MCPClientRepo) *Manager {
	return &Manager{repo: repo, now: time.Now}
}

// Start loads every enabled client from the repo and dials it, then launches
// the background sync/reconnect loops. Idempotent.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.ctx != nil {
		m.mu.Unlock()
		return
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	if m.clients == nil {
		m.clients = make(map[string]*clientState)
	}

	clients, err := m.repo.List(ctx)
	if err != nil {
		m.mu.Unlock()
		return
	}
	var enabled []*clientState
	for i := range clients {
		c := clients[i]
		if c.Enabled {
			enabled = append(enabled, m.registerLocked(&c))
		} else {
			m.registerDisabledLocked(&c)
		}
	}
	m.mu.Unlock()

	// Dial and start loops outside the write lock: dial performs blocking
	// I/O and takes the read lock internally.
	for _, st := range enabled {
		m.dial(st)
		m.startLoop(st)
	}
}

// Close stops all background loops and disconnects every client.
func (m *Manager) Close() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.clients {
		st.mu.Lock()
		if st.conn != nil {
			st.conn.Close()
			st.conn = nil
		}
		st.mu.Unlock()
	}
}

// AddClient registers a client config, dials it, and discovers tools. A
// disabled client is registered without dialing.
func (m *Manager) AddClient(ctx context.Context, cfg *domain.MCPClient) error {
	m.mu.Lock()
	var st *clientState
	if cfg.Enabled {
		st = m.registerLocked(cfg)
	} else {
		st = m.registerDisabledLocked(cfg)
	}
	m.mu.Unlock()
	if !cfg.Enabled {
		return nil
	}
	m.dial(st)
	if m.ctx != nil {
		m.startLoop(st)
	}
	return nil
}

// UpdateClient replaces the config and re-dials. A disabled config is
// disconnected and not dialed.
func (m *Manager) UpdateClient(ctx context.Context, cfg *domain.MCPClient) error {
	m.mu.Lock()
	st, ok := m.clients[cfg.ID]
	if !ok {
		m.mu.Unlock()
		return domain.ErrNotFound
	}
	st.mu.Lock()
	st.cfg = cfg
	if st.conn != nil {
		st.conn.Close()
		st.conn = nil
	}
	st.tools = map[string]domain.MCPTool{}
	if cfg.Enabled {
		st.state = domain.MCPStateDisconnected
	} else {
		st.state = domain.MCPStateDisabled
	}
	st.mu.Unlock()
	m.mu.Unlock()
	if !cfg.Enabled {
		return nil
	}
	m.dial(st)
	return nil
}

// RemoveClient disconnects and forgets a client.
func (m *Manager) RemoveClient(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.clients[id]
	if !ok {
		return domain.ErrNotFound
	}
	st.mu.Lock()
	if st.conn != nil {
		st.conn.Close()
	}
	st.mu.Unlock()
	delete(m.clients, id)
	return nil
}

// Reconnect re-dials an existing client and re-discovers tools.
func (m *Manager) Reconnect(ctx context.Context, id string) error {
	m.mu.RLock()
	st, ok := m.clients[id]
	m.mu.RUnlock()
	if !ok {
		return domain.ErrNotFound
	}
	m.dial(st)
	return nil
}

// EnableClient re-connects a disabled client.
func (m *Manager) EnableClient(ctx context.Context, id string) error {
	m.mu.RLock()
	st, ok := m.clients[id]
	m.mu.RUnlock()
	if !ok {
		return domain.ErrNotFound
	}
	st.mu.Lock()
	st.cfg.Enabled = true
	st.state = domain.MCPStateDisconnected
	st.mu.Unlock()
	m.dial(st)
	if m.ctx != nil {
		m.startLoop(st)
	}
	return nil
}

// DisableClient disconnects a client and marks it disabled.
func (m *Manager) DisableClient(ctx context.Context, id string) error {
	m.mu.RLock()
	st, ok := m.clients[id]
	m.mu.RUnlock()
	if !ok {
		return domain.ErrNotFound
	}
	st.mu.Lock()
	st.cfg.Enabled = false
	st.state = domain.MCPStateDisabled
	if st.conn != nil {
		st.conn.Close()
		st.conn = nil
	}
	st.tools = map[string]domain.MCPTool{}
	st.mu.Unlock()
	return nil
}

// Status returns a snapshot of every registered client.
func (m *Manager) Status(ctx context.Context) []domain.MCPClientStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.MCPClientStatus, 0, len(m.clients))
	for _, st := range m.clients {
		st.mu.RLock()
		out = append(out, domain.MCPClientStatus{
			ClientID:   st.cfg.ID,
			Name:       st.cfg.Name,
			State:      st.state,
			Error:      st.lastError,
			ToolCount:  len(st.tools),
			LastSyncAt: st.lastSync,
		})
		st.mu.RUnlock()
	}
	return out
}

// GetTools returns the exposed tools of every enabled, connected client.
func (m *Manager) GetTools(ctx context.Context) []domain.MCPTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.MCPTool
	for _, st := range m.clients {
		st.mu.RLock()
		if st.state == domain.MCPStateConnected {
			for _, t := range st.tools {
				out = append(out, t)
			}
		}
		st.mu.RUnlock()
	}
	return out
}

// ExecuteTool runs a prefixed "<client>__<tool>" call against its owner.
func (m *Manager) ExecuteTool(ctx context.Context, name string, args string) (string, error) {
	clientName, _ := splitToolName(name)
	if clientName == "" {
		return "", errUnknownTool(name)
	}
	m.mu.RLock()
	var st *clientState
	for _, c := range m.clients {
		c.mu.RLock()
		cfgName := c.cfg.Name
		c.mu.RUnlock()
		if cfgName == clientName {
			st = c
			break
		}
	}
	m.mu.RUnlock()
	if st == nil {
		return "", errUnknownTool(name)
	}
	_, upstream := splitToolName(name)
	st.mu.RLock()
	conn := st.conn
	st.mu.RUnlock()
	if conn == nil {
		return "", errUnknownTool(name)
	}
	return callTool(ctx, conn, upstream, args)
}

// registerLocked inserts a fresh client state (caller holds m.mu).
func (m *Manager) registerLocked(cfg *domain.MCPClient) *clientState {
	st := &clientState{
		cfg:   cfg,
		state: domain.MCPStateDisconnected,
		tools: map[string]domain.MCPTool{},
	}
	if m.clients == nil {
		m.clients = make(map[string]*clientState)
	}
	m.clients[cfg.ID] = st
	return st
}

// registerDisabledLocked inserts a disabled client without dialing.
func (m *Manager) registerDisabledLocked(cfg *domain.MCPClient) *clientState {
	st := &clientState{
		cfg:   cfg,
		state: domain.MCPStateDisabled,
		tools: map[string]domain.MCPTool{},
	}
	if m.clients == nil {
		m.clients = make(map[string]*clientState)
	}
	m.clients[cfg.ID] = st
	return st
}

// startLoop launches the background sync + health loop for a client.
func (m *Manager) startLoop(st *clientState) {
	m.mu.RLock()
	parent := m.ctx
	m.mu.RUnlock()
	if parent == nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.loop(ctx, st)
	}()
}

// loop periodically re-syncs tools and reconnects a dropped client.
func (m *Manager) loop(ctx context.Context, st *clientState) {
	interval := m.syncInterval(st)
	health := HealthInterval
	if health <= 0 {
		health = HealthInterval
	}
	healthTicker := time.NewTicker(health)
	defer healthTicker.Stop()
	if interval <= 0 {
		// Sync disabled (0 = never, <0 = disabled): only health reconnect.
		for {
			select {
			case <-ctx.Done():
				return
			case <-healthTicker.C:
				m.healthCheck(st)
			}
		}
	}
	syncTicker := time.NewTicker(interval)
	defer syncTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-healthTicker.C:
			m.healthCheck(st)
		case <-syncTicker.C:
			m.sync(st)
		}
	}
}

// syncInterval resolves the per-client sync interval.
func (m *Manager) syncInterval(st *clientState) time.Duration {
	st.mu.RLock()
	s := st.cfg.SyncSeconds
	st.mu.RUnlock()
	if s < 0 {
		return -1
	}
	if s > 0 {
		return time.Duration(s) * time.Second
	}
	return DefaultSyncInterval
}

// healthCheck reconnects a connected-or-error client whose connection died.
func (m *Manager) healthCheck(st *clientState) {
	st.mu.RLock()
	conn := st.conn
	enabled := st.cfg.Enabled
	st.mu.RUnlock()
	if conn == nil {
		if enabled {
			m.dial(st)
		}
		return
	}
	if conn.isClosed() {
		st.mu.Lock()
		st.conn = nil
		st.state = domain.MCPStateDisconnected
		st.mu.Unlock()
		m.dial(st)
	}
}

// sync refreshes a client's tool cache via tools/list.
func (m *Manager) sync(st *clientState) {
	st.mu.RLock()
	conn := st.conn
	cfg := st.cfg
	st.mu.RUnlock()
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.backgroundCtx(), ToolSyncTimeout)
	defer cancel()
	tools, err := listTools(ctx, conn)
	if err != nil {
		// Keep existing tools on failure.
		return
	}
	st.mu.Lock()
	st.tools = filterTools(cfg, tools)
	st.lastSync = m.now()
	st.state = domain.MCPStateConnected
	st.lastError = ""
	st.mu.Unlock()
}

func (m *Manager) backgroundCtx() context.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

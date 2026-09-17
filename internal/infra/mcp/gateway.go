package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// gatewaySyncInterval is how often the gateway re-reads the tool registry on
// its own. The registry only moves when a background client sync finishes, and
// a client waiting for a notification is not sending requests, so polling is
// what keeps the announcement flowing when the endpoint is idle.
const gatewaySyncInterval = 30 * time.Second

// sessionHeartbeat keeps an idle server-to-client SSE stream from being
// reaped by an intermediary while it waits for notifications.
const sessionHeartbeat = 30 * time.Second

// Gateway is the aggregated MCP server behind the /mcp endpoint. It exposes
// every discovered tool (across all connected clients) as a single MCP server
// and proxies tools/call to the owning upstream client. It is re-synced
// whenever the tool registry changes.
//
// Sync runs per incoming /mcp request (see handleMCPGateway), so it must be
// safe against concurrent requests: mu serializes the whole re-registration,
// which also covers the registered map.
type Gateway struct {
	mu      sync.Mutex
	manager *Manager
	mcp     *server.MCPServer
	handler *server.StreamableHTTPServer
	version string
	// registered maps an exposed tool name to the fingerprint of what the MCP
	// server currently holds for it. Comparing a fingerprint against the
	// registry is what makes a changed description or schema a real change,
	// and not just a silent no-op.
	registered map[string]string
}

// NewGateway builds an empty aggregated server. Sync must be called after
// construction to populate tools.
//
// The transport is the library's Streamable HTTP server in stateful mode, so
// a session survives across requests (Mcp-Session-Id), GET opens the
// server-to-client stream notifications travel on, and DELETE ends a session.
// State is per process: the service runs a single replica.
func NewGateway(manager *Manager, version string) *Gateway {
	srv := server.NewMCPServer(
		"gorouter",
		version,
		server.WithToolCapabilities(true),
	)
	return &Gateway{
		manager:    manager,
		mcp:        srv,
		handler:    server.NewStreamableHTTPServer(srv, server.WithStateful(true), server.WithHeartbeatInterval(sessionHeartbeat)),
		version:    version,
		registered: map[string]string{},
	}
}

// Server exposes the underlying mcp-go server for HandleMessage.
func (g *Gateway) Server() *server.MCPServer { return g.mcp }

// Handler serves the /mcp endpoint: POST for JSON-RPC, GET for the
// server-to-client notification stream, DELETE to end a session.
func (g *Gateway) Handler() http.Handler { return g.handler }

// Start re-syncs the gateway on a timer until ctx is done, so tool changes
// discovered in the background are announced to connected clients even when
// the endpoint receives no traffic.
func (g *Gateway) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(gatewaySyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.Sync(ctx)
			}
		}
	}()
}

// Sync brings the registered tool set in line with the live registry. Tools
// that vanished are removed and tools that are new or changed are
// re-registered; the MCP server itself announces every such move to connected
// clients, because the gateway declares the tools.listChanged capability.
// Safe for concurrent use.
func (g *Gateway) Sync(ctx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.applyTools(g.manager.GetTools(ctx))
}

// applyTools reconciles the MCP server's tool set with available and reports
// whether anything moved. A tool is re-registered when its description or input
// schema changed, not only when its name first appears: the client caches both,
// so a stale description is a real divergence, and re-registering is also what
// makes the server announce it.
func (g *Gateway) applyTools(available []domain.MCPTool) bool {
	changed := false

	for name := range g.registered {
		if !containsTool(available, name) {
			g.mcp.DeleteTools(name)
			delete(g.registered, name)
			changed = true
		}
	}

	for _, t := range available {
		fp := toolFingerprint(t)
		if cur, ok := g.registered[t.Name]; ok && cur == fp {
			continue
		}
		// AddTool overwrites by name, so a changed description or schema is
		// replaced in place and announced exactly once.
		g.mcp.AddTool(mcp.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: inputSchemaOf(t),
		}, g.toolHandler(t.Name))
		g.registered[t.Name] = fp
		changed = true
	}

	return changed
}

// inputSchemaOf rebuilds the MCP tool schema from the registry entry, which
// carries it as a loose JSON object.
func inputSchemaOf(t domain.MCPTool) mcp.ToolInputSchema {
	schema := mcp.ToolInputSchema{Type: "object"}
	if s, ok := t.InputSchema["type"].(string); ok && s != "" {
		schema.Type = s
	}
	if props, ok := t.InputSchema["properties"].(map[string]any); ok {
		schema.Properties = props
	}
	if req, ok := t.InputSchema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				schema.Required = append(schema.Required, s)
			}
		}
	}
	return schema
}

// toolFingerprint is what makes a tool "the same" from the client's point of
// view: its description and the schema it must fill.
func toolFingerprint(t domain.MCPTool) string {
	schema, err := json.Marshal(t.InputSchema)
	if err != nil {
		return t.Description
	}
	return t.Description + "\x00" + string(schema)
}

func containsTool(tools []domain.MCPTool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// toolHandler proxies a tools/call request to the owning upstream client.
func (g *Gateway) toolHandler(name string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := json.Marshal(req.GetArguments())
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal tool arguments: %v", err)), nil
		}
		text, err := g.manager.ExecuteTool(ctx, name, string(args))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("tool execution failed: %v", err)), nil
		}
		return mcp.NewToolResultText(text), nil
	}
}

package domain

import (
	"context"
	"time"
)

// MCPConnectionType selects how gorouter dials an upstream MCP server.
type MCPConnectionType string

const (
	MCPTypeHTTP  MCPConnectionType = "http"  // Streamable HTTP (JSON-RPC over POST)
	MCPTypeSSE   MCPConnectionType = "sse"   // HTTP + SSE transport
	MCPTypeStdio MCPConnectionType = "stdio" // local process via stdio
)

// MCPAuthType selects how gorouter authenticates to an upstream MCP server.
type MCPAuthType string

const (
	MCPAuthNone   MCPAuthType = "none"   // no auth
	MCPAuthBearer MCPAuthType = "bearer" // Authorization: Bearer <token>
)

// MCPConnectionState mirrors the live state of an upstream MCP client.
type MCPConnectionState string

const (
	MCPStateConnected    MCPConnectionState = "connected"    // dialed and tools discovered
	MCPStateDisconnected MCPConnectionState = "disconnected" // not connected (yet)
	MCPStateError        MCPConnectionState = "error"        // last dial/sync failed
	MCPStateDisabled     MCPConnectionState = "disabled"     // intentionally disabled
)

// MCPClient is a registered upstream MCP server. It is configuration, not
// state: the live connection, discovered tools, and health are tracked
// in-memory by the MCPManager (domain.MCPManager).
type MCPClient struct {
	ID   string `json:"id" gorm:"primaryKey"`
	Name string `json:"name" gorm:"uniqueIndex"`
	// ConnectionType selects the transport: "http", "sse", or "stdio".
	ConnectionType MCPConnectionType `json:"connection_type"`
	// URL is the upstream endpoint for http/sse connections. Ignored for
	// stdio.
	URL string `json:"url,omitempty"`
	// Headers are extra headers sent with http/sse requests. When AuthType
	// is bearer, the Authorization header is derived from AuthToken and
	// does not need to be listed here.
	Headers map[string]string `json:"headers,omitempty" gorm:"serializer:json;type:text"`
	// StdioCommand and StdioArgs launch a local MCP server process. Only
	// used when ConnectionType is "stdio".
	StdioCommand string   `json:"stdio_command,omitempty"`
	StdioArgs    []string `json:"stdio_args,omitempty" gorm:"serializer:json;type:text"`
	AuthType     MCPAuthType `json:"auth_type"`
	// AuthToken is the bearer token. Never exposed in list JSON.
	AuthToken string `json:"-" gorm:"column:auth_token"`
	// ToolsToExecute is an include-only allow-list of tool names
	// (prefixed "<client>__<tool>"). ["*"] includes every discovered tool;
	// empty means none are exposed.
	ToolsToExecute []string `json:"tools_to_execute,omitempty" gorm:"serializer:json;type:text"`
	Enabled        bool     `json:"enabled" gorm:"default:true"`
	// SyncSeconds is the tool re-sync interval. 0 uses the default (10m);
	// negative disables background sync.
	SyncSeconds int       `json:"sync_seconds,omitempty" gorm:"column:sync_seconds;default:0"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MCPTool is a discovered tool from an upstream MCP server. Name is the
// sanitized, prefixed name ("<client>__<tool>") used in LLM tool calls.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	ClientID    string         `json:"client_id,omitempty"`
}

// MCPClientStatus is the runtime snapshot of a client for the dashboard.
type MCPClientStatus struct {
	ClientID   string            `json:"client_id"`
	Name       string            `json:"name"`
	State      MCPConnectionState `json:"state"`
	Error      string            `json:"error,omitempty"`
	ToolCount  int               `json:"tool_count"`
	LastSyncAt time.Time         `json:"last_sync_at,omitempty"`
}

// MCPClientRepo persists MCP client configurations.
type MCPClientRepo interface {
	List(ctx context.Context) ([]MCPClient, error)
	Get(ctx context.Context, id string) (*MCPClient, error)
	Create(ctx context.Context, c *MCPClient) error
	Update(ctx context.Context, c *MCPClient) error
	Delete(ctx context.Context, id string) error
}

// MCPManager owns the live upstream MCP client connections, the discovered
// tool registry, background tool sync, and the aggregated MCP gateway. It is
// the infrastructure port: the app layer depends on this interface, the
// infra/mcp package implements it.
type MCPManager interface {
	// Start launches background goroutines (tool sync, health). Safe to
	// call once after wiring; no-op when already started.
	Start(ctx context.Context)
	// Close stops background goroutines and disconnects all clients.
	Close()

	// AddClient dials a client and discovers its tools. On failure the
	// client is still registered in the Disconnected/Error state so the
	// dashboard can retry.
	AddClient(ctx context.Context, cfg *MCPClient) error
	// UpdateClient replaces the configuration and re-dials.
	UpdateClient(ctx context.Context, cfg *MCPClient) error
	// RemoveClient disconnects and forgets a client.
	RemoveClient(ctx context.Context, id string) error
	// Reconnect re-dials an existing client and re-discovers tools.
	Reconnect(ctx context.Context, id string) error
	// EnableClient re-connects a disabled client.
	EnableClient(ctx context.Context, id string) error
	// DisableClient disconnects a client and marks it disabled.
	DisableClient(ctx context.Context, id string) error

	// Status returns a snapshot of every registered client.
	Status(ctx context.Context) []MCPClientStatus
	// GetTools returns the exposed tools for all enabled clients, honoring
	// each client's ToolsToExecute allow-list. Names are prefixed
	// "<client>__<tool>".
	GetTools(ctx context.Context) []MCPTool
	// GetToolsByClients returns the exposed tools only for the given client
	// IDs (an empty list returns no tools). Honors the same allow-list as
	// GetTools.
	GetToolsByClients(ctx context.Context, clientIDs []string) []MCPTool
	// ExecuteTool runs a tool call against the owning client. name is the
	// prefixed "<client>__<tool>" name; args is the raw JSON arguments
	// object. Returns the tool result text.
	ExecuteTool(ctx context.Context, name string, args string) (string, error)
}

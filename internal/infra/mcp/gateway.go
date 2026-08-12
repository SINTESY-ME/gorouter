package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Gateway is the aggregated MCP server behind the /mcp endpoint. It exposes
// every discovered tool (across all connected clients) as a single MCP server
// and proxies tools/call to the owning upstream client. It is re-synced
// whenever the tool registry changes.
type Gateway struct {
	manager  *Manager
	mcp      *server.MCPServer
	version  string
	handlers map[string]bool // prefixed tool name → registered
}

// NewGateway builds an empty aggregated server. syncServer must be called
// after construction to populate tools.
func NewGateway(manager *Manager, version string) *Gateway {
	srv := server.NewMCPServer(
		"gorouter",
		version,
		server.WithToolCapabilities(true),
	)
	return &Gateway{manager: manager, mcp: srv, version: version, handlers: map[string]bool{}}
}

// Server exposes the underlying mcp-go server for HandleMessage.
func (g *Gateway) Server() *server.MCPServer { return g.mcp }

// Sync re-registers every tool from the manager. Tools that disappeared are
// removed; new or changed ones are registered.
func (g *Gateway) Sync(ctx context.Context) {
	available := g.manager.GetTools(ctx)

	// Remove tools that are no longer available.
	registered := g.mcp.ListTools()
	for name := range registered {
		if !containsTool(available, name) {
			g.mcp.DeleteTools(name)
			delete(g.handlers, name)
		}
	}

	// Add or refresh available tools.
	for _, t := range available {
		if g.handlers[t.Name] {
			continue
		}
		toolName := t.Name
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
		g.mcp.AddTool(mcp.Tool{
			Name:        toolName,
			Description: t.Description,
			InputSchema: schema,
		}, g.toolHandler(toolName))
		g.handlers[toolName] = true
	}
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

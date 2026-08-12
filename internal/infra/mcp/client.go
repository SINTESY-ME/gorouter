package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// mcpClient is the subset of the mcp-go client we depend on, so the registry
// can hold either a live *client.Client or a test double.
type mcpClient interface {
	Start(ctx context.Context) error
	Close() error
	Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
	isClosed() bool
}

// liveClient adapts *client.Client to mcpClient.
type liveClient struct{ c *client.Client }

func (l *liveClient) Start(ctx context.Context) error { return l.c.Start(ctx) }
func (l *liveClient) Close() error                    { return l.c.Close() }
func (l *liveClient) Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return l.c.Initialize(ctx, req)
}
func (l *liveClient) ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return l.c.ListTools(ctx, req)
}
func (l *liveClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return l.c.CallTool(ctx, req)
}
func (l *liveClient) isClosed() bool { return l.c == nil || l.c.GetTransport() == nil }

// dial connects to an upstream MCP server, performs the initialize handshake,
// and discovers its tools. On success the client state is updated with the
// live connection and tool cache; on failure it is marked Error with a
// message so the dashboard can surface it.
func (m *Manager) dial(st *clientState) {
	st.mu.RLock()
	cfg := st.cfg
	st.mu.RUnlock()

	ctx, cancel := context.WithTimeout(m.backgroundCtx(), DialTimeout)
	defer cancel()

	conn, err := connect(ctx, cfg)
	if err != nil {
		st.mu.Lock()
		if st.conn != nil {
			st.conn.Close()
			st.conn = nil
		}
		st.state = domain.MCPStateError
		st.lastError = err.Error()
		st.mu.Unlock()
		return
	}

	tools, err := listTools(ctx, conn)
	if err != nil {
		conn.Close()
		st.mu.Lock()
		st.conn = nil
		st.state = domain.MCPStateError
		st.lastError = fmt.Sprintf("list tools: %v", err)
		st.mu.Unlock()
		return
	}

	st.mu.Lock()
	if st.conn != nil {
		st.conn.Close()
	}
	st.conn = conn
	st.state = domain.MCPStateConnected
	st.lastError = ""
	st.tools = filterTools(cfg, tools)
	st.lastSync = m.now()
	st.mu.Unlock()
}

// connect builds and starts the transport for the given config.
func connect(ctx context.Context, cfg *domain.MCPClient) (mcpClient, error) {
	headers := make(map[string]string, len(cfg.Headers)+1)
	for k, v := range cfg.Headers {
		headers[k] = v
	}
	if cfg.AuthType == domain.MCPAuthBearer && cfg.AuthToken != "" {
		headers["Authorization"] = "Bearer " + cfg.AuthToken
	}

	var tr transport.Interface
	var err error
	switch cfg.ConnectionType {
	case domain.MCPTypeHTTP:
		tr, err = transport.NewStreamableHTTP(cfg.URL, transport.WithHTTPHeaders(headers))
	case domain.MCPTypeSSE:
		tr, err = transport.NewSSE(cfg.URL, transport.WithHeaders(headers))
	case domain.MCPTypeStdio:
		tr = transport.NewStdio(cfg.StdioCommand, nil, cfg.StdioArgs...)
	default:
		return nil, fmt.Errorf("unsupported connection type %q", cfg.ConnectionType)
	}
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	c := client.NewClient(tr)
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("start transport: %w", err)
	}
	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "gorouter",
				Version: "1.0.0",
			},
		},
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	return &liveClient{c: c}, nil
}

// listTools retrieves every tool from a connected client with their raw
// upstream names. Prefixing and allow-list filtering are applied later by
// filterTools (which knows the client config).
func listTools(ctx context.Context, conn mcpClient) ([]domain.MCPTool, error) {
	res, err := conn.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	tools := make([]domain.MCPTool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema := map[string]any{}
		if t.RawInputSchema != nil {
			_ = jsonUnmarshal(t.RawInputSchema, &schema)
		} else if t.InputSchema.Type != "" {
			schema["type"] = t.InputSchema.Type
			if t.InputSchema.Properties != nil {
				schema["properties"] = t.InputSchema.Properties
			}
			if len(t.InputSchema.Required) > 0 {
				schema["required"] = t.InputSchema.Required
			}
		}
		tools = append(tools, domain.MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return tools, nil
}

// callTool executes a tool against a connected client and returns the
// extracted text result.
func callTool(ctx context.Context, conn mcpClient, upstreamTool, args string) (string, error) {
	var arguments any
	if strings.TrimSpace(args) == "" {
		arguments = map[string]any{}
	} else if err := jsonUnmarshal([]byte(args), &arguments); err != nil {
		return "", fmt.Errorf("parse tool arguments: %w", err)
	}
	res, err := conn.CallTool(ctx, mcp.CallToolRequest{
		Request: mcp.Request{Method: "tools/call"},
		Params:  mcp.CallToolParams{Name: upstreamTool, Arguments: arguments},
	})
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("tool call stream ended unexpectedly")
		}
		return "", err
	}
	return extractText(res), nil
}

// extractText concatenates the text/image/audio content of a tool result.
func extractText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var sb strings.Builder
	for _, block := range res.Content {
		switch content := block.(type) {
		case mcp.TextContent:
			sb.WriteString(content.Text)
		case mcp.ImageContent:
			sb.WriteString(fmt.Sprintf("[image: %s, mime: %s]", content.Data, content.MIMEType))
		case mcp.AudioContent:
			sb.WriteString(fmt.Sprintf("[audio: %s, mime: %s]", content.Data, content.MIMEType))
		default:
			// Embedded resources and future types fall back to JSON.
			if raw, jerr := jsonMarshal(block); jerr == nil {
				sb.Write(raw)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

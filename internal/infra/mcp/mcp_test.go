package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// fakeRepo implements domain.MCPClientRepo backed by a map for tests.
type fakeRepo struct {
	clients map[string]*domain.MCPClient
	order   []string
}

func newFakeRepo() *fakeRepo { return &fakeRepo{clients: map[string]*domain.MCPClient{}} }
func (r *fakeRepo) List(ctx context.Context) ([]domain.MCPClient, error) {
	var out []domain.MCPClient
	for _, id := range r.order {
		out = append(out, *r.clients[id])
	}
	return out, nil
}
func (r *fakeRepo) Get(ctx context.Context, id string) (*domain.MCPClient, error) {
	c, ok := r.clients[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}
func (r *fakeRepo) Create(ctx context.Context, c *domain.MCPClient) error {
	r.clients[c.ID] = c
	r.order = append(r.order, c.ID)
	return nil
}
func (r *fakeRepo) Update(ctx context.Context, c *domain.MCPClient) error { return nil }
func (r *fakeRepo) Delete(ctx context.Context, id string) error           { return nil }

// startTestMCPServer runs a real MCP server (with one echo tool) behind an
// httptest server, returning its base URL and a shutdown func.
func startTestMCPServer(t *testing.T) (string, func()) {
	t.Helper()
	srv := server.NewMCPServer("test-server", "1.0.0", server.WithToolCapabilities(true))
	srv.AddTool(mcp.Tool{
		Name:        "echo",
		Description: "echoes the message",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{"message": map[string]any{"type": "string"}}},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msg := req.GetString("message", "")
		return mcp.NewToolResultText("echo: " + msg), nil
	})
	httpSrv := server.NewStreamableHTTPServer(srv)
	ts := httptest.NewServer(http.HandlerFunc(httpSrv.ServeHTTP))
	return ts.URL, ts.Close
}

func TestDialAndExecuteHTTP(t *testing.T) {
	url, close := startTestMCPServer(t)
	defer close()

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

	st.mu.RLock()
	state := st.state
	errStr := st.lastError
	toolCount := len(st.tools)
	st.mu.RUnlock()
	if state != domain.MCPStateConnected {
		t.Fatalf("expected connected, got %s (err=%s)", state, errStr)
	}
	if toolCount != 1 {
		t.Fatalf("expected 1 tool, got %d", toolCount)
	}

	got, err := m.ExecuteTool(context.Background(), "local__echo", `{"message":"oi"}`)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if got != "echo: oi" {
		t.Fatalf("unexpected result %q", got)
	}
}

func TestFilterToolsAllowList(t *testing.T) {
	cfg := &domain.MCPClient{
		Name:           "gh",
		ToolsToExecute: []string{"gh__allowed"},
	}
	tools := []domain.MCPTool{{Name: "allowed"}, {Name: "blocked"}}
	out := filterTools(cfg, tools)
	if len(out) != 1 {
		t.Fatalf("expected 1 filtered tool, got %d", len(out))
	}
	if _, ok := out["gh__allowed"]; !ok {
		t.Fatalf("expected gh__allowed in output, got %+v", out)
	}
}

func TestFilterToolsDenyByDefault(t *testing.T) {
	cfg := &domain.MCPClient{Name: "gh"} // no ToolsToExecute → deny-by-default
	tools := []domain.MCPTool{{Name: "allowed"}}
	if out := filterTools(cfg, tools); len(out) != 0 {
		t.Fatalf("expected 0 tools with empty allow-list, got %d", len(out))
	}
}

func TestSplitToolName(t *testing.T) {
	client, tool := splitToolName("gh__create_issue")
	if client != "gh" || tool != "create_issue" {
		t.Fatalf("got (%q, %q)", client, tool)
	}
	client, tool = splitToolName("gh__nested__tool")
	if client != "gh" || tool != "nested__tool" {
		t.Fatalf("nested: got (%q, %q)", client, tool)
	}
	client, _ = splitToolName("bare")
	if client != "" {
		t.Fatalf("bare should have empty client, got %q", client)
	}
}

func TestGatewaySyncAndJSONRPC(t *testing.T) {
	url, close := startTestMCPServer(t)
	defer close()

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gw.Sync(ctx)

	tools := gw.Server().ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool in gateway, got %d", len(tools))
	}

	// Send a tools/call JSON-RPC message through HandleMessage.
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "local__echo",
			"arguments": map[string]any{"message": "via gateway"},
		},
	})
	resp := gw.Server().HandleMessage(ctx, msg)
	if resp == nil {
		t.Fatalf("expected a response")
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal resp: %v", err)
	}
	if !bytes.Contains(b, []byte("echo: via gateway")) {
		t.Fatalf("gateway did not proxy tool call, got %s", b)
	}
}

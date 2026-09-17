package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// startUpstreamMCP runs a real MCP server exposing a single tool behind an
// httptest server — the upstream an agent would bring to gorouter.
func startUpstreamMCP(t *testing.T, toolName string) (string, func()) {
	t.Helper()
	srv := server.NewMCPServer("upstream-"+toolName, "1.0.0", server.WithToolCapabilities(true))
	srv.AddTool(mcp.Tool{
		Name:        toolName,
		Description: toolName + " tool",
		InputSchema: mcp.ToolInputSchema{Type: "object"},
	}, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(server.NewStreamableHTTPServer(srv).ServeHTTP))
	return ts.URL, ts.Close
}

// dialUpstream registers and connects one upstream client on the manager.
func dialUpstream(t *testing.T, m *Manager, id, name, url string) {
	t.Helper()
	cfg := &domain.MCPClient{
		ID:             id,
		Name:           name,
		ConnectionType: domain.MCPTypeHTTP,
		URL:            url,
		AuthType:       domain.MCPAuthNone,
		ToolsToExecute: []string{"*"},
	}
	m.mu.Lock()
	st := m.registerLocked(cfg)
	m.mu.Unlock()
	m.dial(st)
}

// waitTools waits until the manager cached the expected number of tools.
func waitTools(t *testing.T, m *Manager, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := len(m.GetTools(context.Background())); got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tools did not reach %d: got %d", want, len(m.GetTools(context.Background())))
}

func toolNames(tools []mcp.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		out = append(out, tl.Name)
	}
	return out
}

// An agent that already listed tools has no way to learn the list moved unless
// the server tells it: this drives the whole transport (session, tools/list,
// the server-to-client stream) with the real MCP client and asserts the
// notification announced by initialize actually arrives.
func TestGatewaySessionReceivesToolChangeNotification(t *testing.T) {
	urlA, closeA := startUpstreamMCP(t, "alpha")
	defer closeA()

	m := NewManager(newFakeRepo())
	dialUpstream(t, m, "c1", "srvA", urlA)
	waitTools(t, m, 1)

	gw := NewGateway(m, "1.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gw.Sync(ctx)
	defer func() { _ = gw.handler.Shutdown(context.Background()) }()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL, transport.WithContinuousListening())
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer c.Close()

	notes := make(chan string, 8)
	c.OnNotification(func(n mcp.JSONRPCNotification) { notes <- n.Method })

	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "gateway-test", Version: "1.0.0"},
		},
	}); err != nil {
		// A failed initialize means session support is broken end to end.
		t.Fatalf("initialize (session): %v", err)
	}

	listed, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := toolNames(listed.Tools); len(got) != 1 || got[0] != "srvA__alpha" {
		t.Fatalf("tools = %v, want [srvA__alpha]", got)
	}

	// A second upstream client shows up with its own tool.
	urlB, closeB := startUpstreamMCP(t, "beta")
	defer closeB()
	dialUpstream(t, m, "c2", "srvB", urlB)
	waitTools(t, m, 2)
	gw.Sync(ctx)

	select {
	case method := <-notes:
		if method != mcp.MethodNotificationToolsListChanged {
			t.Fatalf("notification = %q, want %q", method, mcp.MethodNotificationToolsListChanged)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the client waiting on the notification stream was never told the tool list changed")
	}

	// The session must keep working after the move.
	listed, err = c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools after change: %v", err)
	}
	if got := toolNames(listed.Tools); len(got) != 2 {
		t.Fatalf("tools after change = %v, want 2 entries", got)
	}
}

// The transport contract itself: initialize hands out a session, later
// requests carry it, an unknown session is refused, and DELETE ends it.
func TestGatewaySessionTransport(t *testing.T) {
	m := NewManager(newFakeRepo())
	gw := NewGateway(m, "1.0.0")
	defer func() { _ = gw.handler.Shutdown(context.Background()) }()
	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.LATEST_PROTOCOL_VERSION + `","capabilities":{},"clientInfo":{"name":"raw","version":"1"}}}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(init))
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", resp.StatusCode)
	}
	session := resp.Header.Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("initialize did not return a Mcp-Session-Id")
	}

	list := func(sessionID string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		out, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("tools/list: %v", err)
		}
		return out
	}

	ok := list(session)
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("tools/list with the returned session = %d, want 200", ok.StatusCode)
	}
	var envelope struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(ok.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	// A session the server never issued must not be served.
	bogus := list("not-a-real-session")
	defer bogus.Body.Close()
	if bogus.StatusCode == http.StatusOK {
		t.Fatal("a request with an unknown session id was served")
	}

	// DELETE ends the session, and the session stops being usable.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", session)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}
	defer del.Body.Close()
	if del.StatusCode != http.StatusOK {
		t.Fatalf("delete session = %d, want 200", del.StatusCode)
	}
	after := list(session)
	defer after.Body.Close()
	if after.StatusCode == http.StatusOK {
		t.Fatal("a terminated session still served requests")
	}
}

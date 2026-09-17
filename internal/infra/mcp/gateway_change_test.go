package mcp

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// visibleTools maps exposed tool name → description the client is being told.
func visibleTools(t *testing.T, m *Manager) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, tl := range m.GetTools(context.Background()) {
		out[tl.Name] = tl.Description
	}
	return out
}

// A tool whose description or schema changed is a change the client must hear
// about: it caches both, and the name alone looks unchanged. The MCP server
// only announces tools it is asked to add or delete, so the gateway has to
// notice the drift itself and re-register.
func TestGatewayReRegistersChangedTool(t *testing.T) {
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
		t.Fatalf("initialize: %v", err)
	}
	// Drain anything already queued so the assertion below is about the change.
	for len(notes) > 0 {
		<-notes
	}

	// The upstream now describes the same tool differently and adds an
	// argument — the shape of an upstream that redeployed its own tool.
	m.mu.RLock()
	st := m.clients["c1"]
	m.mu.RUnlock()
	if st == nil {
		t.Fatal("client state missing")
	}
	st.mu.Lock()
	for name, tl := range st.tools {
		tl.Description = "alpha tool v2"
		tl.InputSchema = map[string]any{
			"type":       "object",
			"properties": map[string]any{"sku": map[string]any{"type": "string"}},
		}
		st.tools[name] = tl
	}
	st.mu.Unlock()

	if got := visibleTools(t, m)["srvA__alpha"]; got != "alpha tool v2" {
		t.Fatalf("registry not updated: description = %q", got)
	}
	gw.Sync(ctx)

	select {
	case method := <-notes:
		if method != mcp.MethodNotificationToolsListChanged {
			t.Fatalf("notification = %q, want %q", method, mcp.MethodNotificationToolsListChanged)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a changed tool description was never announced to the connected client")
	}

	// And the client must see the new description when it re-lists.
	listed, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(listed.Tools))
	}
	if got := listed.Tools[0].Description; got != "alpha tool v2" {
		t.Fatalf("re-listed description = %q, want %q", got, "alpha tool v2")
	}
	if _, ok := listed.Tools[0].InputSchema.Properties["sku"]; !ok {
		t.Fatal("re-listed tool is missing the new argument")
	}

	// Nothing moved since, so a further sync must not wake the client again.
	gw.Sync(ctx)
	select {
	case method := <-notes:
		t.Fatalf("an unchanged registry announced %q", method)
	case <-time.After(300 * time.Millisecond):
	}
}

// The capability is announced in initialize, so a client that did nothing
// wrong must be able to rely on it: removing a client's tool is announced too.
func TestGatewayAnnouncesRemovedTool(t *testing.T) {
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
		t.Fatalf("initialize: %v", err)
	}
	for len(notes) > 0 {
		<-notes
	}

	if err := m.RemoveClient(ctx, "c1"); err != nil {
		t.Fatalf("remove client: %v", err)
	}
	gw.Sync(ctx)

	select {
	case method := <-notes:
		if method != mcp.MethodNotificationToolsListChanged {
			t.Fatalf("notification = %q, want %q", method, mcp.MethodNotificationToolsListChanged)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a removed tool was never announced to the connected client")
	}
}

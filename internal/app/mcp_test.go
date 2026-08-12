package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// mockMCPManager implements domain.MCPManager for tests.
type mockMCPManager struct {
	tools   []domain.MCPTool
	results map[string]string // prefixed tool name -> result text
	err     error
}

func (m *mockMCPManager) Start(ctx context.Context) {}
func (m *mockMCPManager) Close()                     {}
func (m *mockMCPManager) AddClient(ctx context.Context, cfg *domain.MCPClient) error {
	return m.err
}
func (m *mockMCPManager) UpdateClient(ctx context.Context, cfg *domain.MCPClient) error {
	return m.err
}
func (m *mockMCPManager) RemoveClient(ctx context.Context, id string) error { return m.err }
func (m *mockMCPManager) Reconnect(ctx context.Context, id string) error     { return m.err }
func (m *mockMCPManager) EnableClient(ctx context.Context, id string) error  { return m.err }
func (m *mockMCPManager) DisableClient(ctx context.Context, id string) error { return m.err }
func (m *mockMCPManager) Status(ctx context.Context) []domain.MCPClientStatus {
	return nil
}
func (m *mockMCPManager) GetTools(ctx context.Context) []domain.MCPTool { return m.tools }
func (m *mockMCPManager) ExecuteTool(ctx context.Context, name, args string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if r, ok := m.results[name]; ok {
		return r, nil
	}
	return "mock result for " + name, nil
}

// mockMCPClientRepo implements domain.MCPClientRepo for tests.
type mockMCPClientRepo struct {
	clients map[string]*domain.MCPClient
	order   []string
}

func newMockMCPClientRepo() *mockMCPClientRepo {
	return &mockMCPClientRepo{clients: map[string]*domain.MCPClient{}}
}
func (r *mockMCPClientRepo) List(ctx context.Context) ([]domain.MCPClient, error) {
	var out []domain.MCPClient
	for _, id := range r.order {
		out = append(out, *r.clients[id])
	}
	return out, nil
}
func (r *mockMCPClientRepo) Get(ctx context.Context, id string) (*domain.MCPClient, error) {
	c, ok := r.clients[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}
func (r *mockMCPClientRepo) Create(ctx context.Context, c *domain.MCPClient) error {
	r.clients[c.ID] = c
	r.order = append(r.order, c.ID)
	return nil
}
func (r *mockMCPClientRepo) Update(ctx context.Context, c *domain.MCPClient) error {
	if _, ok := r.clients[c.ID]; !ok {
		return domain.ErrNotFound
	}
	r.clients[c.ID] = c
	return nil
}
func (r *mockMCPClientRepo) Delete(ctx context.Context, id string) error {
	if _, ok := r.clients[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.clients, id)
	return nil
}

func testMCPTool(name, desc string) domain.MCPTool {
	return domain.MCPTool{
		Name:        name,
		Description: desc,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
	}
}

func TestInjectOpenAITools(t *testing.T) {
	svc := &MCPService{Manager: &mockMCPManager{tools: []domain.MCPTool{testMCPTool("github__create_issue", "create issue")}}}
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	out, err := svc.InjectTools(context.Background(), domain.FormatOpenAI, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	var req struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "github__create_issue" {
		t.Fatalf("expected 1 injected tool, got %+v", req.Tools)
	}
}

func TestInjectOpenAIToolsPreservesCallerTools(t *testing.T) {
	svc := &MCPService{Manager: &mockMCPManager{tools: []domain.MCPTool{testMCPTool("github__create_issue", "x")}}}
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"caller_tool"}}]}`)
	out, err := svc.InjectTools(context.Background(), domain.FormatOpenAI, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	if !bytes.Contains(out, []byte("caller_tool")) || !bytes.Contains(out, []byte("github__create_issue")) {
		t.Fatalf("expected both tools in body, got %s", out)
	}
}

func TestInjectResponsesTools(t *testing.T) {
	svc := &MCPService{Manager: &mockMCPManager{tools: []domain.MCPTool{testMCPTool("github__create_issue", "x")}}}
	body := []byte(`{"model":"gpt-4o","input":"hi"}`)
	out, err := svc.InjectTools(context.Background(), domain.FormatResponses, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	var req struct {
		Tools []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "github__create_issue" {
		t.Fatalf("expected 1 responses tool, got %+v", req.Tools)
	}
}

func TestInjectAnthropicTools(t *testing.T) {
	svc := &MCPService{Manager: &mockMCPManager{tools: []domain.MCPTool{testMCPTool("github__create_issue", "x")}}}
	body := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`)
	out, err := svc.InjectTools(context.Background(), domain.FormatAnthropic, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	var req struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "github__create_issue" {
		t.Fatalf("expected 1 anthropic tool, got %+v", req.Tools)
	}
}

func TestInjectToolsNoMCP(t *testing.T) {
	svc := &MCPService{} // Manager nil
	body := []byte(`{"model":"gpt-4o"}`)
	out, err := svc.InjectTools(context.Background(), domain.FormatOpenAI, body)
	if err != nil {
		t.Fatalf("InjectTools: %v", err)
	}
	if string(out) != string(body) {
		t.Fatalf("expected unchanged body, got %s", out)
	}
}

func TestExecuteToolChatFormat(t *testing.T) {
	svc := &MCPService{Manager: &mockMCPManager{results: map[string]string{"github__create_issue": "issue #42 created"}}}
	out, err := svc.ExecuteTool(context.Background(), "chat", "github__create_issue", `{"title":"x"}`)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	var msg struct {
		Role        string `json:"role"`
		ToolCallID  string `json:"tool_call_id"`
		Content     string `json:"content"`
	}
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Role != "tool" || msg.ToolCallID != "github__create_issue" || msg.Content != "issue #42 created" {
		t.Fatalf("unexpected chat tool message: %+v", msg)
	}
}

func TestExtractOpenAIToolCalls(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"github__create_issue","arguments":"{\"title\":\"x\"}"}}]}}]}`)
	calls, err := extractOpenAIToolCalls(body)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(calls) != 1 || calls[0].Function.Name != "github__create_issue" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestBuildAgentTurn(t *testing.T) {
	srv := NewRouterService(&mockComboRepo{}, &mockConnectionRepo{}, &mockExecutor{}, &mockTranslator{}, &mockUsageRepo{})
	srv.MCP = &MCPService{Manager: &mockMCPManager{results: map[string]string{"github__create_issue": "done"}}}
	prev := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"create issue"}]}`)
	resp := []byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"github__create_issue","arguments":"{}"}}]}}]}`)
	next, err := srv.buildAgentTurn(context.Background(), prev, resp, []openaiToolCall{{
		ID:   "call_1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "github__create_issue", Arguments: "{}"},
	}})
	if err != nil {
		t.Fatalf("buildAgentTurn: %v", err)
	}
	var req struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(next, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages (user+assistant+tool), got %d", len(req.Messages))
	}
	if req.Messages[2]["role"] != "tool" || req.Messages[2]["content"] != "done" {
		t.Fatalf("unexpected tool message: %+v", req.Messages[2])
	}
}

// TestRouteWithAgentLoop exercises the full loop through the router: the
// executor returns a tool-call response on every dispatch (the mock is
// static), so the loop must keep dispatching until maxAgentDepth and return
// the last tool-call response. The assertion is that MCP execution happened
// across multiple dispatches (exec.calls > 1) and the response still carries
// tool_calls (the client would take over).
func TestRouteWithAgentLoop(t *testing.T) {
	connRepo := twoProviderConnRepo()
	exec := &mockExecutor{bodies: map[string]string{
		"gpt-4o": `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"github__create_issue","arguments":"{}"}}]}}]}`,
	}, status: http.StatusOK}
	usage := &mockUsageRepo{}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)
	srv.MCP = &MCPService{Manager: &mockMCPManager{results: map[string]string{"github__create_issue": "issue created"}}}

	body := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, "openai/gpt-4o", false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("RouteChat: %v", err)
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(res.Body)
	if !bytes.Contains(buf, []byte("tool_calls")) {
		t.Fatalf("expected the last (tool-call) response to be returned after depth exhaustion, got %s", buf)
	}
	if exec.calls < 2 {
		t.Fatalf("expected multiple dispatches in agent loop, got %d", exec.calls)
	}
}

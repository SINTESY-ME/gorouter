package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/mcp"
)

// TestMCPDashboardCRUD exercises the /api/mcp/clients routes with an in-memory
// repo + manager (no upstream dials).
func TestMCPDashboardCRUD(t *testing.T) {
	repo := newMCPTestRepo()
	manager := mcp.NewManager(repo)
	svc := &app.MCPService{Repo: repo, Manager: manager}
	s := &Server{MCP: &MCP{Svc: svc, Manager: manager, Gateway: mcp.NewGateway(manager, "1.0.0")}}

	// Create
	body := []byte(`{"name":"github","connection_type":"http","url":"http://localhost:9999/mcp","auth_type":"none","tools_to_execute":["*"],"enabled":true}`)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/mcp/clients", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created domain.MCPClient
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected generated id")
	}

	// List
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/clients", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	var list []app.MCPClientView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "github" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Update
	upd := []byte(`{"name":"github2","connection_type":"http","url":"http://localhost:9999/mcp","auth_type":"none","tools_to_execute":["*"],"enabled":true}`)
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/mcp/clients/"+created.ID, bytes.NewReader(upd)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Delete
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/mcp/clients/"+created.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/clients", nil))
	var empty []app.MCPClientView
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("unmarshal empty list: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty list, got %+v", empty)
	}
}

// TestMCPGatewayDisabled verifies /api/mcp and /mcp no-op when MCP is nil.
func TestMCPGatewayDisabled(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/clients", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("clients when disabled = %d, want 200 (empty)", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/mcp/clients", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("create when disabled = %d, want 501", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{}`))))
	// Route exists but is gated by requireApiKey, which runs before the
	// (disabled) handler — no key → 401.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/mcp when disabled = %d, want 401", rec.Code)
	}
}

// newMCPTestRepo is an in-memory MCPClientRepo for handler tests.
func newMCPTestRepo() *mcpTestRepo { return &mcpTestRepo{clients: map[string]*domain.MCPClient{}} }

type mcpTestRepo struct {
	clients map[string]*domain.MCPClient
	order   []string
}

func (r *mcpTestRepo) List(ctx context.Context) ([]domain.MCPClient, error) {
	var out []domain.MCPClient
	for _, id := range r.order {
		c, ok := r.clients[id]
		if !ok {
			continue
		}
		out = append(out, *c)
	}
	return out, nil
}
func (r *mcpTestRepo) Get(ctx context.Context, id string) (*domain.MCPClient, error) {
	c, ok := r.clients[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}
func (r *mcpTestRepo) Create(ctx context.Context, c *domain.MCPClient) error {
	if r.clients == nil {
		r.clients = map[string]*domain.MCPClient{}
	}
	r.clients[c.ID] = c
	r.order = append(r.order, c.ID)
	return nil
}
func (r *mcpTestRepo) Update(ctx context.Context, c *domain.MCPClient) error {
	r.clients[c.ID] = c
	return nil
}
func (r *mcpTestRepo) Delete(ctx context.Context, id string) error {
	delete(r.clients, id)
	return nil
}

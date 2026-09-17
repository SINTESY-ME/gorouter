package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/mcp"
)

// MCP handles the dashboard CRUD of upstream MCP clients.
type MCP struct {
	Svc *app.MCPService
	// Manager is the infra MCP manager, exposing the aggregated gateway for
	// the /mcp endpoint.
	Manager *mcp.Manager
	Gateway *mcp.Gateway
}

// handleListMCPClients returns every client with its live status.
func (s *Server) handleListMCPClients(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	views, err := s.MCP.Svc.List(r.Context())
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// handleCreateMCPClient creates and dials an MCP client.
func (s *Server) handleCreateMCPClient(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	var req domain.MCPClient
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.MCP.Svc.Create(r.Context(), &req); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// handleUpdateMCPClient updates and re-dials an MCP client.
func (s *Server) handleUpdateMCPClient(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	id := chi.URLParam(r, "id")
	var req domain.MCPClient
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.MCP.Svc.Update(r.Context(), id, &req); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// handleDeleteMCPClient removes an MCP client.
func (s *Server) handleDeleteMCPClient(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.MCP.Svc.Delete(r.Context(), id); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReconnectMCPClient re-dials an MCP client.
func (s *Server) handleReconnectMCPClient(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.MCP.Svc.Reconnect(r.Context(), id); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reconnected"})
}

// handleEnableMCPClient re-enables a disabled client.
func (s *Server) handleEnableMCPClient(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.MCP.Svc.Enable(r.Context(), id); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "enabled"})
}

// handleDisableMCPClient disables a client without deleting it.
func (s *Server) handleDisableMCPClient(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.MCP.Svc.Disable(r.Context(), id); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "disabled"})
}

// handleMCPTools returns the exposed tools of every enabled client.
func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, s.MCP.Svc.Tools(r.Context()))
}

// handleMCPToolExecute runs a tool call in the requested format (chat,
// anthropic or responses). Body: {"name": "<client>__<tool>",
// "arguments": {...}, "call_id": "<optional>"}.
func (s *Server) handleMCPToolExecute(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	format := r.URL.Query().Get("format")
	var req struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		// CallID is the id of the model's function_call this result answers.
		// Anthropic clients use their tool_use id; Responses clients need the
		// original call_id back, not the tool name.
		CallID string `json:"call_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "tool name is required")
		return
	}
	args := ""
	if len(req.Arguments) > 0 {
		args = string(req.Arguments)
	}
	callID := req.CallID
	if callID == "" {
		callID = req.Name
	}
	out, err := s.MCP.Svc.ExecuteTool(r.Context(), format, req.Name, callID, args)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// handleMCPGateway is the aggregated MCP server endpoint. The transport is the
// library's Streamable HTTP server: POST carries JSON-RPC, GET opens the
// server-to-client stream that notifications arrive on, and DELETE ends the
// session named by Mcp-Session-Id.
//
// Every POST re-syncs the gateway first, so a client added moments ago is
// visible in the very request that lists tools.
func (s *Server) handleMCPGateway(w http.ResponseWriter, r *http.Request) {
	if s.MCP == nil || s.MCP.Gateway == nil {
		writeError(w, http.StatusNotImplemented, "mcp gateway not enabled")
		return
	}
	if r.Method == http.MethodPost {
		s.MCP.Gateway.Sync(r.Context())
	}
	s.MCP.Gateway.Handler().ServeHTTP(w, r)
}

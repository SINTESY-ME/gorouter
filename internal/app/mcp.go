// Package app holds the application services (use cases). Each service is a
// thin orchestrator that depends only on domain ports; infrastructure adapters
// are injected at the composition root.
package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jhon/gorouter/internal/domain"
)

// MCPService is the dashboard + inference use case for the MCP gateway. It
// owns the CRUD of upstream MCP clients and the format-aware tool injection
// into chat request bodies. The live connections and tool registry live in
// the injected domain.MCPManager (infra/mcp).
type MCPService struct {
	Repo    domain.MCPClientRepo
	Manager domain.MCPManager
}

// List returns all MCP clients with their runtime status merged in.
func (s *MCPService) List(ctx context.Context) ([]MCPClientView, error) {
	clients, err := s.Repo.List(ctx)
	if err != nil {
		return nil, err
	}
	statuses := map[string]domain.MCPClientStatus{}
	if s.Manager != nil {
		for _, st := range s.Manager.Status(ctx) {
			statuses[st.ClientID] = st
		}
	}
	views := make([]MCPClientView, 0, len(clients))
	for i := range clients {
		v := MCPClientView{MCPClient: &clients[i]}
		if st, ok := statuses[clients[i].ID]; ok {
			v.State = st.State
			v.Error = st.Error
			v.ToolCount = st.ToolCount
			v.LastSyncAt = st.LastSyncAt
		} else {
			v.State = domain.MCPStateDisconnected
		}
		// Never leak the auth token.
		v.AuthToken = ""
		views = append(views, v)
	}
	return views, nil
}

// MCPClientView is a client config plus its live status.
type MCPClientView struct {
	*domain.MCPClient
	State      domain.MCPConnectionState `json:"state"`
	Error      string                    `json:"error,omitempty"`
	ToolCount  int                       `json:"tool_count"`
	LastSyncAt time.Time                 `json:"last_sync_at,omitempty"`
}// Create validates and persists a client, then dials it.
func (s *MCPService) Create(ctx context.Context, c *domain.MCPClient) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now()
	}
	if err := validateMCPClient(c); err != nil {
		return err
	}
	if err := s.Repo.Create(ctx, c); err != nil {
		return err
	}
	if s.Manager != nil && c.Enabled {
		return s.Manager.AddClient(ctx, c)
	}
	return nil
}

// Update replaces a client config, re-dials, and re-persists.
func (s *MCPService) Update(ctx context.Context, id string, c *domain.MCPClient) error {
	existing, err := s.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	c.ID = id
	c.CreatedAt = existing.CreatedAt
	c.UpdatedAt = time.Now()
	// An empty auth token in the update means "unchanged": the client API
	// masks the stored token, so the frontend can't echo it back. Preserve
	// the existing value unless a new one was supplied.
	if c.AuthToken == "" {
		c.AuthToken = existing.AuthToken
	}
	if err := validateMCPClient(c); err != nil {
		return err
	}
	if s.Manager != nil {
		if !c.Enabled && existing.Enabled {
			_ = s.Manager.DisableClient(ctx, id)
		} else if c.Enabled {
			_ = s.Manager.UpdateClient(ctx, c)
		}
	}
	return s.Repo.Update(ctx, c)
}

// Delete removes a client and disconnects it.
func (s *MCPService) Delete(ctx context.Context, id string) error {
	if s.Manager != nil {
		_ = s.Manager.RemoveClient(ctx, id)
	}
	return s.Repo.Delete(ctx, id)
}

// Reconnect re-dials a client and returns its status.
func (s *MCPService) Reconnect(ctx context.Context, id string) error {
	if s.Manager == nil {
		return domain.ErrNotFound
	}
	return s.Manager.Reconnect(ctx, id)
}

// Enable turns a disabled client back on.
func (s *MCPService) Enable(ctx context.Context, id string) error {
	c, err := s.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.Manager != nil {
		if err := s.Manager.EnableClient(ctx, id); err != nil {
			return err
		}
	}
	c.Enabled = true
	c.UpdatedAt = time.Now()
	return s.Repo.Update(ctx, c)
}

// Disable turns a client off without deleting it.
func (s *MCPService) Disable(ctx context.Context, id string) error {
	c, err := s.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if s.Manager != nil {
		if err := s.Manager.DisableClient(ctx, id); err != nil {
			return err
		}
	}
	c.Enabled = false
	c.UpdatedAt = time.Now()
	return s.Repo.Update(ctx, c)
}

// Tools returns the exposed tools of every enabled client.
func (s *MCPService) Tools(ctx context.Context) []domain.MCPTool {
	if s.Manager == nil {
		return nil
	}
	return s.Manager.GetTools(ctx)
}

// InjectTools merges the exposed MCP tools into a chat request body in the
// given client format (OpenAI chat, Anthropic, or Responses). Existing tools
// keep precedence (the caller's tools win on name collision). The body is
// returned unchanged when MCP is disabled or there are no tools.
func (s *MCPService) InjectTools(ctx context.Context, format domain.Format, body []byte) ([]byte, error) {
	return s.InjectToolsForClients(ctx, format, body, nil)
}

// InjectToolsForClients merges the MCP tools of the given client IDs into a
// chat request body in the given client format. A nil clientIDs means "every
// enabled client" (used by the dashboard preview and legacy callers); a
// non-nil slice — even empty — filters to exactly those clients, so combos
// never receive tools from clients they did not declare.
func (s *MCPService) InjectToolsForClients(ctx context.Context, format domain.Format, body []byte, clientIDs []string) ([]byte, error) {
	if s.Manager == nil {
		return body, nil
	}
	var tools []domain.MCPTool
	if clientIDs != nil {
		tools = s.Manager.GetToolsByClients(ctx, clientIDs)
	} else {
		tools = s.Manager.GetTools(ctx)
	}
	if len(tools) == 0 {
		return body, nil
	}
	var out []byte
	var err error
	switch format {
	case domain.FormatAnthropic:
		out, err = injectAnthropicTools(body, tools)
	case domain.FormatResponses:
		out, err = injectResponsesTools(body, tools)
	default: // openai + auto fall back to openai shape
		out, err = injectOpenAITools(body, tools)
	}
	if err != nil {
		// Fail-open: never break the request because tool injection failed.
		return body, nil
	}
	return out, nil
}

// ExecuteTool runs a tool call in the requested format (chat or responses)
// and returns the formatted result body. Used by /v1/mcp/tool/execute.
func (s *MCPService) ExecuteTool(ctx context.Context, format string, toolName, arguments string) ([]byte, error) {
	if s.Manager == nil {
		return nil, domain.ErrNotFound
	}
	text, err := s.Manager.ExecuteTool(ctx, toolName, arguments)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(format) {
	case "responses":
		return json.Marshal(map[string]any{
			"type":   "function_call_output",
			"call_id": toolName,
			"output": text,
		})
	default: // chat
		return json.Marshal(map[string]any{
			"role": "tool",
			"content": text,
			"tool_call_id": toolName,
		})
	}
}

// validateMCPClient enforces the shape of a client config.
func validateMCPClient(c *domain.MCPClient) error {
	if c.Name == "" {
		return fmtValidation("name is required")
	}
	switch c.ConnectionType {
	case domain.MCPTypeHTTP, domain.MCPTypeSSE:
		if c.URL == "" {
			return fmtValidation("url is required for http/sse connections")
		}
	case domain.MCPTypeStdio:
		if c.StdioCommand == "" {
			return fmtValidation("stdio_command is required for stdio connections")
		}
	default:
		return fmtValidation("connection_type must be http, sse, or stdio")
	}
	switch c.AuthType {
	case "", domain.MCPAuthNone, domain.MCPAuthBearer:
	default:
		return fmtValidation("auth_type must be none or bearer")
	}
	return nil
}

// ---- format-aware tool injection ----

// injectOpenAITools appends OpenAI chat-style tools ({type:"function",
// function:{name,description,parameters}}).
func injectOpenAITools(body []byte, tools []domain.MCPTool) ([]byte, error) {
	var req struct {
		Tools json.RawMessage `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	merged, err := mergeOpenAITools(req.Tools, tools)
	if err != nil {
		return nil, err
	}
	return setField(body, "tools", merged)
}

func mergeOpenAITools(existing json.RawMessage, tools []domain.MCPTool) (json.RawMessage, error) {
	names := map[string]bool{}
	var out []map[string]any
	if len(existing) > 0 {
		var parsed []map[string]any
		if err := json.Unmarshal(existing, &parsed); err != nil {
			return nil, err
		}
		for _, t := range parsed {
			if fn, ok := t["function"].(map[string]any); ok {
				if n, ok := fn["name"].(string); ok && n != "" {
					names[n] = true
				}
			}
			out = append(out, t)
		}
	}
	for _, t := range tools {
		if names[t.Name] {
			continue
		}
		names[t.Name] = true
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return json.Marshal(out)
}

// injectResponsesTools appends Responses-style tools
// ({type:"function", name, description, parameters}).
func injectResponsesTools(body []byte, tools []domain.MCPTool) ([]byte, error) {
	var req struct {
		Tools json.RawMessage `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	var out []map[string]any
	if len(req.Tools) > 0 {
		var parsed []map[string]any
		if err := json.Unmarshal(req.Tools, &parsed); err != nil {
			return nil, err
		}
		for _, t := range parsed {
			if n, ok := t["name"].(string); ok && n != "" {
				names[n] = true
			}
			out = append(out, t)
		}
	}
	for _, t := range tools {
		if names[t.Name] {
			continue
		}
		names[t.Name] = true
		out = append(out, map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.InputSchema,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return setField(body, "tools", b)
}

// injectAnthropicTools appends Anthropic-style tools
// ({name, description, input_schema}).
func injectAnthropicTools(body []byte, tools []domain.MCPTool) ([]byte, error) {
	var req struct {
		Tools json.RawMessage `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	var out []map[string]any
	if len(req.Tools) > 0 {
		var parsed []map[string]any
		if err := json.Unmarshal(req.Tools, &parsed); err != nil {
			return nil, err
		}
		for _, t := range parsed {
			if n, ok := t["name"].(string); ok && n != "" {
				names[n] = true
			}
			out = append(out, t)
		}
	}
	for _, t := range tools {
		if names[t.Name] {
			continue
		}
		names[t.Name] = true
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"input_schema": t.InputSchema,
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return setField(body, "tools", b)
}

// setField overwrites a top-level JSON field, preserving all other fields.
func setField(body []byte, key string, value json.RawMessage) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m[key] = value
	return json.Marshal(m)
}

// Package app holds the application services (use cases). Each service is a
// thin orchestrator that depends only on domain ports; infrastructure adapters
// are injected at the composition root.
package app

import (
	"context"
	"encoding/json"
	"fmt"
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
} // Create validates and persists a client, then dials it.
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

// OwnedTools returns the set of tool names the gateway may execute for the
// given client IDs. It is the ownership boundary of the server-side agent
// loop: a tool outside this set belongs to the client, not to gorouter, and
// must never be executed (or answered) here. A nil clientIDs means "every
// enabled client" — matching GetTools, not GetToolsByClients.
func (s *MCPService) OwnedTools(ctx context.Context, clientIDs []string) map[string]bool {
	if s.Manager == nil {
		return nil
	}
	var tools []domain.MCPTool
	if clientIDs != nil {
		tools = s.Manager.GetToolsByClients(ctx, clientIDs)
	} else {
		tools = s.Manager.GetTools(ctx)
	}
	owned := make(map[string]bool, len(tools))
	for _, t := range tools {
		owned[t.Name] = true
	}
	return owned
}

// InjectTools merges the exposed MCP tools into a chat request body in the
// given client format (OpenAI chat, Anthropic, or Responses). A tool the caller
// already declared wins, including when only the spelling differs (see
// alreadyDeclared), and the body is returned unchanged when MCP is disabled,
// there are no tools, or every tool was already declared.
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

// ExecuteTool runs a tool call in the requested format (chat, anthropic or
// responses) and returns the formatted result body. Used by
// /v1/mcp/tool/execute. callID is the id of the model's function_call that
// this result answers (tool_use id, tool_call id or Responses call_id).
// A tool the gateway does not own is reported as not found.
func (s *MCPService) ExecuteTool(ctx context.Context, format string, toolName, callID, arguments string) ([]byte, error) {
	if s.Manager == nil {
		return nil, domain.ErrNotFound
	}
	if !s.OwnedTools(ctx, nil)[toolName] {
		return nil, fmt.Errorf("%w: mcp tool %q is not available", domain.ErrNotFound, toolName)
	}
	text, err := s.Manager.ExecuteTool(ctx, toolName, arguments)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(format) {
	case "responses":
		return json.Marshal(map[string]any{
			"type":    "function_call_output",
			"call_id": callID,
			"output":  text,
		})
	case "anthropic":
		return json.Marshal(map[string]any{
			"type":        "tool_result",
			"tool_use_id": callID,
			"content":     text,
		})
	default: // chat
		return json.Marshal(map[string]any{
			"role":         "tool",
			"content":      text,
			"tool_call_id": callID,
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

// harnessToolPrefix is how a harness that already speaks MCP itself (Claude
// Code, Hermes) names a tool it has connected: "mcp__<server>__<tool>". The
// gateway names the same upstream tool "<client>__<tool>", so the two
// conventions never collide on the raw string and a plain name comparison sees
// no overlap.
const harnessToolPrefix = "mcp__"

// toolNameKey folds a tool name to its comparison key. The comparison is
// case-insensitive because the server half of the name is spelled by whoever
// configured it, and the same server is routinely display-cased differently on
// each side.
func toolNameKey(name string) string { return strings.ToLower(name) }

// openAIToolName reads the name of a chat tool, which nests it under
// "function".
func openAIToolName(t map[string]any) string {
	fn, _ := t["function"].(map[string]any)
	n, _ := fn["name"].(string)
	return n
}

// flatToolName reads the name of a Responses or Anthropic tool, both of which
// carry it at the top level.
func flatToolName(t map[string]any) string {
	n, _ := t["name"].(string)
	return n
}

// declaredTools parses the caller's own tool list, returning it untouched along
// with the names it already claims. nameOf reads the name out of a tool because
// each wire format nests it differently.
func declaredTools(raw json.RawMessage, nameOf func(map[string]any) string) ([]map[string]any, map[string]bool, error) {
	names := map[string]bool{}
	if len(raw) == 0 {
		return nil, names, nil
	}
	var parsed []map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, nil, err
	}
	for _, t := range parsed {
		if n := nameOf(t); n != "" {
			names[toolNameKey(n)] = true
		}
	}
	return parsed, names, nil
}

// alreadyDeclared reports whether the caller already declared this upstream
// tool, under the gateway's own name or behind the harness prefix. When it did,
// the caller's definition wins: a second copy of the same capability spends
// tokens on a duplicate schema and leaves the model picking between two
// identical tools, which also picks the execution path (server-side loop versus
// the caller running it) by accident.
func alreadyDeclared(declared map[string]bool, name string) bool {
	return declared[toolNameKey(name)] || declared[toolNameKey(harnessToolPrefix+name)]
}

// markDeclared records an injected tool so the same name is not offered twice.
func markDeclared(declared map[string]bool, name string) {
	declared[toolNameKey(name)] = true
}

// injectOpenAITools appends OpenAI chat-style tools ({type:"function",
// function:{name,description,parameters}}).
func injectOpenAITools(body []byte, tools []domain.MCPTool) ([]byte, error) {
	var req struct {
		Tools json.RawMessage `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	merged, injected, err := mergeOpenAITools(req.Tools, tools)
	if err != nil {
		return nil, err
	}
	if !injected {
		// The caller already declared every tool: hand its request back
		// untouched rather than re-serializing a body that does not change.
		return body, nil
	}
	return setField(body, "tools", merged)
}

func mergeOpenAITools(existing json.RawMessage, tools []domain.MCPTool) (json.RawMessage, bool, error) {
	out, declared, err := declaredTools(existing, openAIToolName)
	if err != nil {
		return nil, false, err
	}
	injected := false
	for _, t := range tools {
		if alreadyDeclared(declared, t.Name) {
			continue
		}
		markDeclared(declared, t.Name)
		injected = true
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	if !injected {
		return existing, false, nil
	}
	merged, err := json.Marshal(out)
	if err != nil {
		return nil, false, err
	}
	return merged, true, nil
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
	out, declared, err := declaredTools(req.Tools, flatToolName)
	if err != nil {
		return nil, err
	}
	injected := false
	for _, t := range tools {
		if alreadyDeclared(declared, t.Name) {
			continue
		}
		markDeclared(declared, t.Name)
		injected = true
		out = append(out, map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.InputSchema,
		})
	}
	if !injected {
		return body, nil
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
	out, declared, err := declaredTools(req.Tools, flatToolName)
	if err != nil {
		return nil, err
	}
	injected := false
	for _, t := range tools {
		if alreadyDeclared(declared, t.Name) {
			continue
		}
		markDeclared(declared, t.Name)
		injected = true
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	if !injected {
		return body, nil
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

package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
)

// toolNameSep separates the client name from the tool name in prefixed tool
// names, e.g. "github__create_issue".
const toolNameSep = "__"

// errUnknownTool is returned when a tool name has no owning client or the
// client is not connected.
func errUnknownTool(name string) error {
	return fmt.Errorf("mcp tool %q is not available", name)
}

// prefixToolName builds the prefixed tool name "<client>__<tool>".
func prefixToolName(clientName, tool string) string {
	if clientName == "" {
		return tool
	}
	return clientName + toolNameSep + tool
}

// splitToolName splits a prefixed name back into (client, tool). Empty client
// is returned when there is no separator.
func splitToolName(name string) (string, string) {
	idx := strings.Index(name, toolNameSep)
	if idx < 0 {
		return "", name
	}
	return name[:idx], name[idx+len(toolNameSep):]
}

// filterTools applies the client's ToolsToExecute allow-list and prefixes each
// tool name with the client name. tools come from listTools with raw upstream
// names. The returned map is keyed by the prefixed name.
func filterTools(cfg *domain.MCPClient, tools []domain.MCPTool) map[string]domain.MCPTool {
	out := make(map[string]domain.MCPTool, len(tools))
	for _, t := range tools {
		upstream := t.Name
		prefixed := prefixToolName(cfg.Name, upstream)
		if !toolAllowed(cfg, prefixed, upstream) {
			continue
		}
		t.Name = prefixed
		t.ClientID = cfg.ID
		out[prefixed] = t
	}
	return out
}

// toolAllowed reports whether a discovered tool may be exposed. An empty or
// ["*"] allow-list includes everything; otherwise the prefixed and raw names
// must appear in the list.
func toolAllowed(cfg *domain.MCPClient, prefixed, upstream string) bool {
	if len(cfg.ToolsToExecute) == 0 {
		return false // deny-by-default
	}
	if slices.Contains(cfg.ToolsToExecute, "*") {
		return true
	}
	return slices.Contains(cfg.ToolsToExecute, prefixed) || slices.Contains(cfg.ToolsToExecute, upstream)
}

// jsonUnmarshal is a tiny indirection for testability and consistent error
// wrapping.
func jsonUnmarshal(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("json: %w", err)
	}
	return nil
}

// jsonMarshal marshals v to JSON, propagating errors.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

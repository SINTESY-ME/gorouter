package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/providers/executors"
)

// HTTPModelFetcher implements domain.ModelFetcher by GETting <BaseURL>/models
// (or anthropic's /v1/messages/models) with the connection's auth. It tolerates
// three response shapes: {"data": [...]}, {"models": [...]}, or a bare array.
type HTTPModelFetcher struct {
	Client *http.Client
}

func NewHTTPModelFetcher() *HTTPModelFetcher {
	return &HTTPModelFetcher{Client: &http.Client{Timeout: 10 * time.Second}}
}

func (f *HTTPModelFetcher) Fetch(ctx context.Context, c *domain.Connection, cfg *domain.ProviderConfig) ([]domain.ModelInfo, error) {
	url := f.modelsURL(cfg)
	if url == "" {
		return nil, nil
	}
	// Cloud Code Assist providers (gemini-cli, antigravity) expose no /models
	// route at all — asking for one answers 404, which used to surface in the
	// dashboard as "fetch models: status 404" and blocked the catalog preset
	// behind it. Their catalog lives at POST /v1internal:fetchAvailableModels.
	if isCloudCodeProvider(cfg) {
		return f.fetchCloudCode(ctx, c, cfg)
	}
	if isCodexProvider(cfg) {
		return f.fetchCodex(ctx, c, cfg)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	f.applyAuth(req, c, cfg)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		// The provider does not publish a model list. Not an error: sync
		// falls back to the catalog preset, and the dashboard stays quiet.
		slog.Info("model fetch: provider has no model list endpoint", "provider", cfg.ID, "status", resp.StatusCode, "url", url)
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch models: status %d", resp.StatusCode)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MB cap
	if err != nil {
		return nil, err
	}
	return parseModelList(buf)
}

// cloudCodeProviders are the product ids that speak Cloud Code Assist. Their
// transport is the giveaway when the configured base URL is empty or proxied;
// the host check catches the same providers under another id.
var cloudCodeProviders = map[string]bool{"gemini-cli": true, "antigravity": true}

// isCloudCodeProvider reports whether the provider speaks the Cloud Code
// Assist API, whose only model listing is v1internal:fetchAvailableModels.
func isCloudCodeProvider(cfg *domain.ProviderConfig) bool {
	if cfg == nil {
		return false
	}
	if cloudCodeProviders[cfg.ID] {
		return true
	}
	return strings.Contains(strings.TrimRight(cfg.ResolvedBaseURL, "/"), "cloudcode-pa.googleapis.com")
}

// cloudCodeBaseURL is the provider's transport root, defaulting to the Cloud
// Code Assist host for a product id whose config carries none.
func cloudCodeBaseURL(cfg *domain.ProviderConfig) string {
	base := strings.TrimRight(cfg.ResolvedBaseURL, "/")
	if base == "" {
		return "https://cloudcode-pa.googleapis.com"
	}
	return base
}

// cloudCodeUserAgent mimics the client each provider id belongs to: the
// endpoint is fronted per-product and answers 403 to an unknown client.
func cloudCodeUserAgent(providerID string) string {
	switch providerID {
	case "antigravity":
		return "antigravity/1.107.0 linux/amd64"
	case "gemini-cli":
		return "gemini-cli"
	default:
		return providerID
	}
}

// fetchCloudCode lists the models a Cloud Code Assist account may call.
// Response shape (probed 2026-09-18 against a live antigravity account):
// {"models": {"<id>": {"isInternal": bool, "maxTokens": n, ...}, ...}} — a MAP
// keyed by the model id the executor passes upstream verbatim, which is why
// the ids can go into the catalog untouched.
//
// A denial (401/403) or a missing route is reported as "no list" rather than
// an error: those providers are licensed per account, and a listing denial
// must not break sync for everyone else.
func (f *HTTPModelFetcher) fetchCloudCode(ctx context.Context, c *domain.Connection, cfg *domain.ProviderConfig) ([]domain.ModelInfo, error) {
	url := cloudCodeBaseURL(cfg) + "/v1internal:fetchAvailableModels"
	body := []byte("{}")
	if project := cloudCodeProject(c); project != "" {
		body, _ = json.Marshal(map[string]string{"project": project})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("User-Agent", cloudCodeUserAgent(cfg.ID))
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		slog.Warn("model fetch: cloud code listing unavailable",
			"provider", cfg.ID, "status", resp.StatusCode, "response_body", truncateForLog(string(raw), 300))
		return nil, nil
	}
	return parseCloudCodeModels(raw)
}

// parseCloudCodeModels reads the fetchAvailableModels payload. Entries the
// product marks internal (chat_*, tab_*) are IDE features, not callable chat
// models, and are dropped; the id the map is keyed by is kept verbatim
// because the executor forwards it unchanged.
func parseCloudCodeModels(raw []byte) ([]domain.ModelInfo, error) {
	var payload struct {
		Models map[string]struct {
			IsInternal  bool    `json:"isInternal"`
			MaxTokens   float64 `json:"maxTokens"`
			APIProvider string  `json:"apiProvider"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse cloud code models: %w", err)
	}
	out := make([]domain.ModelInfo, 0, len(payload.Models))
	for id, m := range payload.Models {
		if id == "" || m.IsInternal {
			continue
		}
		if strings.HasPrefix(id, "tab_") {
			continue
		}
		info := domain.ModelInfo{ID: id, Object: "model"}
		if m.MaxTokens > 0 {
			info.Metadata = domain.ModelMetadata{Context: int(m.MaxTokens)}
		}
		out = append(out, info)
	}
	return out, nil
}

// cloudCodeProject reads the Cloud Code project id the OAuth login stored.
func cloudCodeProject(c *domain.Connection) string {
	if c == nil || c.Meta == "" {
		return ""
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(c.Meta), &meta); err != nil {
		return ""
	}
	project, _ := meta["project_id"].(string)
	return project
}

// codexProviders are the product ids that speak the ChatGPT Codex backend.
// The host check catches the same backend under another id or a proxy.
var codexProviders = map[string]bool{"codex": true}

func isCodexProvider(cfg *domain.ProviderConfig) bool {
	if cfg == nil {
		return false
	}
	if codexProviders[cfg.ID] {
		return true
	}
	return strings.Contains(cfg.ResolvedBaseURL, "chatgpt.com/backend-api")
}

// fetchCodex lists the models the ChatGPT Codex backend offers the account.
// The listing route is GET <base>/models?client_version=<ver> (probed
// 2026-09-22): without the query param the server answers 400 "Field
// required", which is exactly what the generic fetch surfaced as
// "fetch models: status 400" — <base>/models is a valid route, it just has a
// required parameter the generic fetcher cannot know. The response is
// {"models":[{"slug":...}]} and each entry carries the catalog facts callers
// need: context window, the reasoning ladder with its default level, and the
// input modalities.
func (f *HTTPModelFetcher) fetchCodex(ctx context.Context, c *domain.Connection, cfg *domain.ProviderConfig) ([]domain.ModelInfo, error) {
	base := strings.TrimRight(cfg.ResolvedBaseURL, "/")
	if base == "" {
		return nil, nil
	}
	url := base + "/models?client_version=" + executors.CodexClientVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	f.applyAuth(req, c, cfg)
	req.Header.Set("User-Agent", "codex_cli_rs/"+executors.CodexClientVersion)
	if id := codexAccountID(c); id != "" {
		req.Header.Set("chatgpt-account-id", id)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		// No listing route: sync falls back to the catalog preset.
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		// Unlike Cloud Code, this route exists — a refusal here is an auth or
		// account problem the operator must see, not silence.
		return nil, fmt.Errorf("fetch models: status %d", resp.StatusCode)
	}
	return parseCodexModels(raw)
}

// parseCodexModels reads the codex listing. Every entry the account's plan
// exposes is kept, including the ones the CLI hides from its own picker
// (visibility "hide" — gpt-reserve and the internal auto-review model): the
// catalog is the operator's toolbox and the dashboard toggles decide what
// routes. Levels are stored in canonical order; codex-specific levels outside
// the ladder (gpt-5.6-terra states "ultra") are dropped by the same rule the
// rest of the router applies — an unknown level would break combo validation
// and effort degradation anyway.
func parseCodexModels(raw []byte) ([]domain.ModelInfo, error) {
	var payload struct {
		Models []struct {
			Slug                      string   `json:"slug"`
			ContextWindow             int      `json:"context_window"`
			InputModalities           []string `json:"input_modalities"`
			SupportsParallelToolCalls bool     `json:"supports_parallel_tool_calls"`
			DefaultReasoningLevel     string   `json:"default_reasoning_level"`
			SupportedReasoningLevels  []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse codex models: %w", err)
	}
	out := make([]domain.ModelInfo, 0, len(payload.Models))
	for _, m := range payload.Models {
		if m.Slug == "" {
			continue
		}
		info := domain.ModelInfo{ID: m.Slug, Object: "model"}
		if m.ContextWindow > 0 {
			info.ContextLength = m.ContextWindow
			info.Metadata.Context = m.ContextWindow
		}
		for _, modality := range m.InputModalities {
			if modality == "image" {
				info.Metadata.SupportsVision = true
			}
		}
		if m.SupportsParallelToolCalls {
			info.Metadata.SupportsToolCall = true
		}
		if len(m.SupportedReasoningLevels) > 0 {
			levels := make([]string, 0, len(m.SupportedReasoningLevels))
			for _, l := range m.SupportedReasoningLevels {
				levels = append(levels, l.Effort)
			}
			ladder := domain.SortEfforts(levels)
			if len(ladder) > 0 {
				info.ReasoningEfforts = ladder
				info.Metadata.SupportsReasoning = true
				info.Metadata.Reasoning = domain.ReasoningCapabilities{
					Known:             true,
					SupportsReasoning: true,
					Efforts:           ladder,
					EffortsStated:     true,
				}
				if domain.EffortRank(m.DefaultReasoningLevel) >= 0 {
					info.Metadata.Reasoning.DefaultEffort = m.DefaultReasoningLevel
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// codexAccountID reads the ChatGPT account id the OAuth login stored; the
// backend scopes its answers to it.
func codexAccountID(c *domain.Connection) string {
	if c == nil || c.Meta == "" {
		return ""
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(c.Meta), &meta); err != nil {
		return ""
	}
	id, _ := meta["account_id"].(string)
	return id
}

func truncateForLog(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (f *HTTPModelFetcher) modelsURL(cfg *domain.ProviderConfig) string {
	base := strings.TrimRight(cfg.ResolvedBaseURL, "/")
	if base == "" {
		return ""
	}
	if cfg.Format == domain.FormatAnthropic {
		return base + "/messages/models"
	}
	return base + "/models"
}

func (f *HTTPModelFetcher) applyAuth(req *http.Request, c *domain.Connection, cfg *domain.ProviderConfig) {
	switch cfg.Auth {
	case domain.AuthXAPIKey:
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case domain.AuthNone:
		// nothing
	default:
		if cfg.Format == domain.FormatGemini {
			req.Header.Set("x-goog-api-key", c.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
	}
}

// parseModelList tolerates {"data":[...]}, {"models":[...]}, {"results":[...]}
// (some providers) and a bare array. Each entry may carry id, name, or model.
func parseModelList(buf []byte) ([]domain.ModelInfo, error) {
	var arr []map[string]any
	if err := json.Unmarshal(buf, &arr); err == nil {
		return mapModels(arr), nil
	}
	var obj struct {
		Data    []map[string]any `json:"data"`
		Models  []map[string]any `json:"models"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(buf, &obj); err != nil {
		return nil, fmt.Errorf("parse model list: %w", err)
	}
	if obj.Data != nil {
		return mapModels(obj.Data), nil
	}
	if obj.Models != nil {
		return mapModels(obj.Models), nil
	}
	if obj.Results != nil {
		return mapModels(obj.Results), nil
	}
	return nil, nil
}

func mapModels(in []map[string]any) []domain.ModelInfo {
	out := make([]domain.ModelInfo, 0, len(in))
	for _, m := range in {
		id := firstStr(m, "id", "name", "model")
		if id == "" {
			continue
		}
		// Gemini API returns 'models/gemini-1.5-pro'. Strip the prefix so
		// it doesn't get duplicated in executor URL building.
		id = strings.TrimPrefix(id, "models/")
		mi := domain.ModelInfo{ID: id, Object: "model", OwnedBy: firstStr(m, "owned_by", "owner", "provider", "organization")}
		mt := firstStr(m, "model_type")
		ef := firstStr(m, "endpoint_format")
		if k := providerModelTypeToKind(mt, ef); k != "" {
			mi.Kind = k
		}
		// Whatever the provider stated about the model travels with it: it is
		// the first link of the metadata chain, and a field it does not state
		// is looked up in the external registries instead.
		mi.Metadata = providerModelMetadata(m)
		out = append(out, mi)
	}
	return out
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

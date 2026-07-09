package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jhon/gorouter/internal/domain"
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

func (f *HTTPModelFetcher) Fetch(ctx context.Context, c *domain.Connection) ([]domain.ModelInfo, error) {
	url := f.modelsURL(c)
	if url == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	f.applyAuth(req, c)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch models: status %d", resp.StatusCode)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MB cap
	if err != nil {
		return nil, err
	}
	return parseModelList(buf)
}

func (f *HTTPModelFetcher) modelsURL(c *domain.Connection) string {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, "/v1") && base != "/v1" {
		base = base[:len(base)-3]
	}
	if c.Format == domain.FormatAnthropic {
		return base + "/v1/messages/models"
	}
	return base + "/v1/models"
}

func (f *HTTPModelFetcher) applyAuth(req *http.Request, c *domain.Connection) {
	switch c.Auth {
	case domain.AuthXAPIKey:
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case domain.AuthNone:
		// nothing
	default:
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
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
		mi := domain.ModelInfo{ID: id, Object: "model", OwnedBy: firstStr(m, "owned_by", "owner", "provider", "organization")}
		// Extract Kind from provider metadata if available (e.g. OpenAdapter
		// sends model_type and endpoint_format).
		mt := firstStr(m, "model_type")
		ef := firstStr(m, "endpoint_format")
		if k := providerModelTypeToKind(mt, ef); k != "" {
			mi.Kind = k
		}
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
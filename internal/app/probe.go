package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// ProviderProbe validates a connection by probing the upstream's models
// endpoint. When the format is "auto", it tries the formats in order of
// preference (openai -> responses -> anthropic -> gemini) and returns the
// first that responds successfully.
//
// For each format the probe tries version prefixes in order:
//   - openai/responses/anthropic: "/v1" first, then "" (no prefix)
//   - gemini: "/v1beta"
//
// The first prefix that returns a non-empty model list wins. The resolved
// base URL (base + prefix) is returned in ProbeResult and persisted by the
// caller so that the executor and fetcher never need to resolve URLs at
// runtime — they just concatenate the endpoint path.
type ProviderProbe struct {
	Client *http.Client
}

func NewProviderProbe() *ProviderProbe {
	return &ProviderProbe{Client: &http.Client{Timeout: 15 * time.Second}}
}

// ProbeResult is the outcome of a probe: the detected format (if auto), the
// resolved base URL ready for consumption, the fetched model list, and any
// error.
type ProbeResult struct {
	Format   domain.Format
	BaseURL  string // resolved base URL (includes version prefix, e.g. /v1)
	Models   []domain.ModelInfo
	Error    error
}

// versionPrefixes returns the version path prefixes to try for a format,
// in priority order. The first that yields a non-empty model list wins.
func versionPrefixes(f domain.Format) []string {
	if f == domain.FormatGemini {
		return []string{"/v1beta"}
	}
	return []string{"/v1", ""}
}

// Probe validates a connection configuration against the upstream. If the
// format is "auto", it detects the best format. The resolved base URL
// (with version prefix) is returned in the result.
func (p *ProviderProbe) Probe(ctx context.Context, conn *domain.Connection, cfg *domain.ProviderConfig) ProbeResult {
	if cfg.Format != "auto" && cfg.Format != "" {
		// Fixed format: just validate.
		models, resolved, err := p.tryFormat(ctx, conn, cfg, cfg.Format)
		return ProbeResult{Format: cfg.Format, BaseURL: resolved, Models: models, Error: err}
	}

	// Auto: try formats in priority order — openai first (most common and
	// widely supported), then responses (newer OpenAI format), then the
	// others.
	for _, f := range []domain.Format{domain.FormatOpenAI, domain.FormatResponses, domain.FormatAnthropic, domain.FormatGemini} {
		probeCfg := *cfg
		probeCfg.Format = f
		models, resolved, err := p.tryFormat(ctx, conn, &probeCfg, f)
		if err == nil && len(models) > 0 {
			return ProbeResult{Format: f, BaseURL: resolved, Models: models}
		}
	}
	return ProbeResult{Error: fmt.Errorf("could not detect provider format: all probes failed")}
}

// tryFormat probes a single format by hitting its models endpoint with
// each candidate version prefix. Returns the model list, the resolved base
// URL (base + winning prefix), and any error.
func (p *ProviderProbe) tryFormat(ctx context.Context, conn *domain.Connection, cfg *domain.ProviderConfig, f domain.Format) ([]domain.ModelInfo, string, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		return nil, "", fmt.Errorf("empty base url")
	}

	var lastErr error
	for _, prefix := range versionPrefixes(f) {
		url := base + prefix + "/models"
		models, err := p.probeURL(ctx, conn, cfg, f, url)
		if err == nil && len(models) > 0 {
			return models, base + prefix, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("probe %s: no models returned", f)
	}
	return nil, "", lastErr
}

// probeURL sends a GET to the models endpoint and parses the model list.
func (p *ProviderProbe) probeURL(ctx context.Context, conn *domain.Connection, cfg *domain.ProviderConfig, f domain.Format, url string) ([]domain.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	applyAuthForProbe(req, conn, cfg, f)
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("probe %s: status %d", f, resp.StatusCode)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseModelList(buf)
}

func applyAuthForProbe(req *http.Request, conn *domain.Connection, cfg *domain.ProviderConfig, f domain.Format) {
	switch cfg.Auth {
	case domain.AuthXAPIKey:
		req.Header.Set("x-api-key", conn.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case domain.AuthNone:
		// nothing
	default:
		if f == domain.FormatGemini {
			q := req.URL.Query()
			q.Set("key", conn.APIKey)
			req.URL.RawQuery = q.Encode()
		} else {
			req.Header.Set("Authorization", "Bearer "+conn.APIKey)
		}
	}
}

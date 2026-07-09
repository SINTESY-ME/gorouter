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
// preference (responses -> openai -> anthropic -> gemini) and returns the
// first that responds successfully.
//
// Probe order (most modern first):
//  1. responses — /v1/responses (OpenAI 2025 format)
//  2. openai    — /v1/models (most common OpenAI-compatible)
//  3. anthropic — /v1/messages with anthropic headers
//  4. gemini    — /v1beta/models
type ProviderProbe struct {
	Client *http.Client
}

func NewProviderProbe() *ProviderProbe {
	return &ProviderProbe{Client: &http.Client{Timeout: 15 * time.Second}}
}

// ProbeResult is the outcome of a probe: the detected format (if auto), the
// fetched model list, and any error.
type ProbeResult struct {
	Format domain.Format
	Models []domain.ModelInfo
	Error  error
}

// Probe validates a connection configuration against the upstream. If the
// format is "auto", it detects the best format. The base_url is normalized
// (trailing /v1 stripped) before probing.
func (p *ProviderProbe) Probe(ctx context.Context, conn *domain.Connection) ProbeResult {
	conn = normalizeConn(conn)

	if conn.Format != "auto" && conn.Format != "" {
		// Fixed format: just validate.
		models, err := p.tryFormat(ctx, conn, conn.Format)
		return ProbeResult{Format: conn.Format, Models: models, Error: err}
	}

	// Auto: try formats in priority order — openai first (most common and
	// widely supported), then responses (newer OpenAI format), then the
	// others. /v1/models is the same endpoint for both openai and responses,
	// so openai is tried first as the safer default.
	for _, f := range []domain.Format{domain.FormatOpenAI, domain.FormatResponses, domain.FormatAnthropic, domain.FormatGemini} {
		probe := *conn
		probe.Format = f
		models, err := p.tryFormat(ctx, &probe, f)
		if err == nil && len(models) > 0 {
			return ProbeResult{Format: f, Models: models}
		}
	}
	return ProbeResult{Error: fmt.Errorf("could not detect provider format: all probes failed")}
}

// tryFormat probes a single format by hitting its models endpoint.
func (p *ProviderProbe) tryFormat(ctx context.Context, conn *domain.Connection, f domain.Format) ([]domain.ModelInfo, error) {
	url := modelsURLForFormat(conn.BaseURL, f)
	if url == "" {
		return nil, fmt.Errorf("empty base url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	applyAuthForProbe(req, conn, f)
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

func modelsURLForFormat(baseURL string, f domain.Format) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return ""
	}
	switch f {
	case domain.FormatAnthropic:
		return base + "/v1/models"
	case domain.FormatGemini:
		return base + "/v1beta/models"
	default: // openai, responses
		return base + "/v1/models"
	}
}

func applyAuthForProbe(req *http.Request, conn *domain.Connection, f domain.Format) {
	switch conn.Auth {
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

// normalizeConn returns a copy of conn with a normalized BaseURL (no trailing
// /v1, no trailing slash). The original is not modified.
func normalizeConn(conn *domain.Connection) *domain.Connection {
	c := *conn
	c.BaseURL = NormalizeBaseURL(c.BaseURL)
	return &c
}

// NormalizeBaseURL strips a trailing /v1 and any trailing slashes from the
// base URL so the executor can consistently append /v1/<endpoint>.
func NormalizeBaseURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, "/")
	// Strip trailing /v1 if present (but keep it if the URL is just "/v1").
	if strings.HasSuffix(u, "/v1") && u != "/v1" {
		u = u[:len(u)-3]
	}
	return u
}
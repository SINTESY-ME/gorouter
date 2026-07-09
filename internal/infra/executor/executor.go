// Package executor implements domain.Executor as a thin HTTP reverse proxy.
//
// The executor's only job is to send one upstream request and return the
// response. It does not buffer streaming bodies and does not know about
// combos or accounts. Format translation, if any, has already been applied
// to the request body by the Translator before this is called.
package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// HTTPExecutor calls an upstream OpenAI/Anthropic/Gemini-compatible endpoint.
// It is safe for concurrent use; one *http.Client is shared for connection
// pooling.
type HTTPExecutor struct {
	Client  *http.Client
	Timeout time.Duration
}

// NewHTTPExecutor builds an executor with the given upstream timeout.
// Transport is the standard library default (connection-pooled).
func NewHTTPExecutor(timeout time.Duration) *HTTPExecutor {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 200
	tr.MaxIdleConnsPerHost = 50
	return &HTTPExecutor{
		Client:  &http.Client{Transport: tr, Timeout: 0},
		Timeout: timeout,
	}
}

// Execute sends the request to the upstream. The body is consumed. The
// returned Body must be closed by the caller.
//
// For streaming requests the upstream timeout is applied only to the headers
// phase (the client Timeout is left at 0 so long streams aren't killed).
func (e *HTTPExecutor) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	url := buildURL(req)
	if url == "" {
		return nil, fmt.Errorf("executor: empty upstream url for provider %q", req.ProviderID)
	}

	body := req.Body
	if body == nil {
		body = io.NopCloser(strings.NewReader(""))
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("executor: build request: %w", err)
	}
	applyAuth(httpReq, req)
	applyHeaders(httpReq, req)
	if httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	client := e.Client
	if !req.Stream && e.Timeout > 0 {
		// Non-streaming: per-request timeout via a child client to avoid
		// affecting concurrent streaming calls.
		toClient := *client
		toClient.Timeout = e.Timeout
		client = &toClient
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return &domain.ExecuteResult{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       resp.Body,
		Stream:     isEventStream(resp) || req.Stream,
	}, nil
}

// buildURL picks the path based on the connection's declared format. Custom
// OpenAI-compatible nodes may override the base url; we honor it verbatim
// and append the canonical path.
func buildURL(req domain.ExecuteRequest) string {
	base := strings.TrimRight(req.Connection.BaseURL, "/")
	if base == "" {
		return ""
	}
	// Strip trailing /v1 so we don't get /v1/v1/chat/completions.
	if strings.HasSuffix(base, "/v1") && base != "/v1" {
		base = base[:len(base)-3]
	}
	switch req.Endpoint {
	case "embeddings":
		return base + "/v1/embeddings"
	case "images/generations":
		return base + "/v1/images/generations"
	case "audio/speech":
		return base + "/v1/audio/speech"
	case "audio/transcriptions":
		return base + "/v1/audio/transcriptions"
	}
	switch req.Connection.Format {
	case domain.FormatAnthropic:
		// Anthropic native: base + /v1/messages. Caller has already built
		// the body in anthropic format if needed.
		return base + "/v1/messages"
	case domain.FormatGemini:
		// Gemini: base + /v1beta/models/<model>:generateContent or
		// :streamGenerateContent?alt=sse for streaming.
		if req.Stream {
			return fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", base, req.UpstreamModel)
		}
		return fmt.Sprintf("%s/v1beta/models/%s:generateContent", base, req.UpstreamModel)
	case domain.FormatResponses:
		return base + "/v1/responses"
	default:
		return base + "/v1/chat/completions"
	}
}

func applyAuth(h *http.Request, req domain.ExecuteRequest) {
	switch req.Connection.Auth {
	case domain.AuthXAPIKey:
		h.Header.Set("x-api-key", req.Connection.APIKey)
		h.Header.Set("anthropic-version", "2023-06-01")
	case domain.AuthNone:
		// nothing
	case domain.AuthBearer:
		fallthrough
	default:
		// Gemini also uses ?key= but for compatibility with OpenAI-style
		// gemini-compatible proxies (OpenRouter, etc.) we default to Bearer.
		if isGeminiNative(req.Connection.Format, req.Connection.Auth) {
			q := h.URL.Query()
			q.Set("key", req.Connection.APIKey)
			h.URL.RawQuery = q.Encode()
		} else {
			h.Header.Set("Authorization", "Bearer "+req.Connection.APIKey)
		}
	}
}

func isGeminiNative(format domain.Format, auth domain.AuthScheme) bool {
	return format == domain.FormatGemini && auth == domain.AuthBearer
}

func applyHeaders(h *http.Request, req domain.ExecuteRequest) {
	for k, v := range req.Headers {
		h.Header.Set(k, v)
	}
}

func isEventStream(resp *http.Response) bool {
	return strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
}
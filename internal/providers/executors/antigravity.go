package executors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/providers/oauth"
)

func init() {
	Register("antigravity", func() domain.Executor { return NewAntigravityExecutor() })
}

// AntigravityExecutor talks to Google Cloud Code Assist with Antigravity headers & payload envelope.
type AntigravityExecutor struct {
	Client *http.Client
}

func NewAntigravityExecutor() *AntigravityExecutor {
	return &AntigravityExecutor{Client: &http.Client{Timeout: 0}}
}

func (e *AntigravityExecutor) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var openAI map[string]any
	_ = json.Unmarshal(raw, &openAI)
	model := req.UpstreamModel
	if model == "" {
		if m, ok := openAI["model"].(string); ok {
			model = m
		}
	}
	// 1:1 Model pass-through: pass req.UpstreamModel verbatim to Cloud Code API
	geminiReq := openAI
	if _, has := openAI["contents"]; !has {
		geminiReq = openaiToGeminiBody(openAI)
	}
	meta := oauth.ParseMeta(req.Connection.Meta)
	project := meta["project_id"]
	wrap := map[string]any{
		"project":     project,
		"model":       model,
		"userAgent":   "antigravity",
		"requestType": "agent",
		"request":     geminiReq,
	}
	b, _ := json.Marshal(wrap)
	path := "https://cloudcode-pa.googleapis.com/v1internal:generateContent"
	if req.Stream {
		path = "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+req.Connection.APIKey)
	httpReq.Header.Set("User-Agent", "antigravity/1.107.0 linux/amd64")
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := e.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	stream := req.Stream || strings.Contains(ct, "text/event-stream")

	var body io.ReadCloser = resp.Body
	if resp.StatusCode < 400 {
		if stream {
			body = unwrapCloudCodeStream(ctx, body)
		} else {
			buf, _ := io.ReadAll(body)
			body.Close()
			var wrap map[string]any
			if json.Unmarshal(buf, &wrap) == nil {
				if r, ok := wrap["response"]; ok {
					buf, _ = json.Marshal(r)
				}
			}
			body = io.NopCloser(bytes.NewReader(buf))
		}
	}

	return &domain.ExecuteResult{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       body,
		Stream:     stream,
	}, nil
}

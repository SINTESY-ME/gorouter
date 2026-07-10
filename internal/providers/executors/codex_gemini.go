package executors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/providers/oauth"
)

func init() {
	Register("codex", func() domain.Executor { return NewCodexExecutor() })
	Register("gemini-cli", func() domain.Executor { return NewGeminiCLIExecutor() })
}

// CodexExecutor talks to ChatGPT Codex backend (Responses API).
type CodexExecutor struct {
	Client *http.Client
}

func NewCodexExecutor() *CodexExecutor {
	return &CodexExecutor{Client: &http.Client{Timeout: 0}}
}

func (e *CodexExecutor) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	url := "https://chatgpt.com/backend-api/codex/responses"
	body := req.Body
	if body == nil {
		body = io.NopCloser(strings.NewReader("{}"))
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+req.Connection.APIKey)
	httpReq.Header.Set("originator", "codex_cli_rs")
	httpReq.Header.Set("User-Agent", "codex_cli_rs/0.136.0")
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	meta := oauth.ParseMeta(req.Connection.Meta)
	if id := meta["account_id"]; id != "" {
		httpReq.Header.Set("chatgpt-account-id", id)
	}
	resp, err := e.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	stream := req.Stream || strings.Contains(ct, "text/event-stream")
	return &domain.ExecuteResult{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       resp.Body,
		Stream:     stream,
	}, nil
}

// GeminiCLIExecutor talks to Google Cloud Code Assist.
type GeminiCLIExecutor struct {
	Client *http.Client
}

func NewGeminiCLIExecutor() *GeminiCLIExecutor {
	return &GeminiCLIExecutor{Client: &http.Client{Timeout: 0}}
}

func (e *GeminiCLIExecutor) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	// Incoming body is OpenAI-format after translation pivot, or already gemini.
	// We wrap for Cloud Code Assist: {project, model, request}.
	var openAI map[string]any
	_ = json.Unmarshal(raw, &openAI)
	model := req.UpstreamModel
	if model == "" {
		if m, ok := openAI["model"].(string); ok {
			model = m
		}
	}
	// Build a minimal gemini request from OpenAI messages if needed.
	geminiReq := openAI
	if _, has := openAI["contents"]; !has {
		geminiReq = openaiToGeminiBody(openAI)
	}
	meta := oauth.ParseMeta(req.Connection.Meta)
	project := meta["project_id"]
	wrap := map[string]any{
		"project": project,
		"model":   model,
		"request": geminiReq,
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
	httpReq.Header.Set("User-Agent", "gemini-cli")
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := e.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return &domain.ExecuteResult{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       resp.Body,
		Stream:     req.Stream || strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"),
	}, nil
}

func openaiToGeminiBody(openAI map[string]any) map[string]any {
	out := map[string]any{}
	msgs, _ := openAI["messages"].([]any)
	var contents []map[string]any
	var system string
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		role, _ := mm["role"].(string)
		content := fmt.Sprint(mm["content"])
		if role == "system" {
			system = content
			continue
		}
		gRole := "user"
		if role == "assistant" {
			gRole = "model"
		}
		contents = append(contents, map[string]any{
			"role":  gRole,
			"parts": []map[string]any{{"text": content}},
		})
	}
	out["contents"] = contents
	if system != "" {
		out["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
	}
	return out
}

// Multi dispatches to a specialized executor by connection ProviderID,
// falling back to Default.
type Multi struct {
	Default domain.Executor
}

func (m *Multi) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	if req.Connection != nil {
		if f := Lookup(req.Connection.ProviderID); f != nil {
			return f().Execute(ctx, req)
		}
	}
	if m.Default == nil {
		return nil, fmt.Errorf("no executor")
	}
	return m.Default.Execute(ctx, req)
}

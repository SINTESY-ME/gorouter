package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// webhookPayload is the event posted to the configured webhook URL (Slack,
// Datadog, or any HTTP endpoint).
type webhookPayload struct {
	Event            string  `json:"event"` // "request.completed" | "request.failed"
	RequestID        string  `json:"request_id"`
	Model            string  `json:"model"`
	Provider         string  `json:"provider,omitempty"`
	Endpoint         string  `json:"endpoint,omitempty"`
	Status           int     `json:"status"`
	Stream           bool    `json:"stream"`
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	Cost             float64 `json:"cost,omitempty"`
	LatencyMs        int64   `json:"latency_ms,omitempty"`
	Error            string  `json:"error,omitempty"`
	Timestamp        string  `json:"timestamp"`
}

// WebhookLoggingHook posts request events (success and failure) to an HTTP
// webhook. It is fail-open: a webhook outage is logged and never affects the
// request path. Configure the target URL via GOROUTER_HOOK_WEBHOOK_URL and
// enable it with "webhook_logging" in hooks_enabled.
type WebhookLoggingHook struct {
	url     string
	client  *http.Client
	timeout time.Duration
}

// NewWebhookLoggingHook builds a hook that posts to url. An empty url makes
// the hook a no-op.
func NewWebhookLoggingHook(url string) *WebhookLoggingHook {
	return &WebhookLoggingHook{url: url, client: &http.Client{Timeout: 5 * time.Second}, timeout: 5 * time.Second}
}

// fire posts the payload asynchronously. Never blocks the request path.
func (h *WebhookLoggingHook) fire(p webhookPayload) {
	if h.url == "" {
		return
	}
	body, err := json.Marshal(p)
	if err != nil {
		return
	}
	go func() {
		dctx, cancel := context.WithTimeout(context.Background(), h.timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(dctx, http.MethodPost, h.url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := h.client.Do(req)
		if err != nil {
			slog.Warn("webhook_logging: post failed", "url", h.url, "err", err)
			return
		}
		resp.Body.Close()
	}()
}

// PostCall reports a completed request. Never errors.
func (h *WebhookLoggingHook) PostCall(_ context.Context, hc *domain.HookContext, res *domain.HookResponse) error {
	h.fire(webhookPayload{
		Event:            "request.completed",
		RequestID:        hc.RequestID,
		Model:            res.Model,
		Provider:         res.Provider,
		Endpoint:         hc.Endpoint,
		Status:           res.StatusCode,
		Stream:           res.Stream,
		PromptTokens:     res.PromptTokens,
		CompletionTokens: res.CompletionTokens,
		Cost:             res.Cost,
		LatencyMs:        res.LatencyMs,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

// PostCallFailure reports a failed request. Never errors.
func (h *WebhookLoggingHook) PostCallFailure(_ context.Context, hc *domain.HookContext, status int, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	h.fire(webhookPayload{
		Event:     "request.failed",
		RequestID: hc.RequestID,
		Model:     hc.Model,
		Endpoint:  hc.Endpoint,
		Status:    status,
		Error:     msg,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

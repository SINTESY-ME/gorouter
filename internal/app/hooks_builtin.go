package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/metrics"
)

func init() {
	RegisterHook("keyword_moderation", func() any {
		return NewKeywordModerationHook(splitCSV(os.Getenv("GOROUTER_HOOK_MODERATION_PATTERNS")))
	})
	RegisterHook("prompt_injection_heuristic", func() any { return &PromptInjectionHeuristicHook{} })
	RegisterHook("request_logging", func() any { return &RequestLoggingHook{} })
	RegisterHook("prometheus", func() any { return metrics.NewHook() })
	RegisterHook("webhook_logging", func() any {
		return NewWebhookLoggingHook(os.Getenv("GOROUTER_HOOK_WEBHOOK_URL"))
	})
}

// splitCSV splits a comma-separated env string into trimmed, non-empty items.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// messageText flattens a message content value (string or array of parts) into
// plain text, so hooks can match against both OpenAI and Anthropic shapes.
func messageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, p := range v {
			if m, ok := p.(map[string]any); ok && m["type"] == "text" {
				if t, ok := m["text"].(string); ok {
					sb.WriteString(t)
					sb.WriteByte(' ')
				}
			}
		}
		return sb.String()
	}
	return ""
}

// chatMessages extracts the chat message list from a request body. Returns
// false when the body is not a parseable chat request (hooks fail open).
func chatMessages(body []byte) ([]struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}, bool) {
	var probe struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, false
	}
	return probe.Messages, true
}

// KeywordModerationHook is a pre-call guardrail that rejects requests whose
// messages match any configured pattern (fail-closed). Patterns are
// comma-separated regexes from GOROUTER_HOOK_MODERATION_PATTERNS; with none
// configured the hook is a no-op.
type KeywordModerationHook struct {
	patterns []*regexp.Regexp
}

// NewKeywordModerationHook builds a hook from regex patterns. Invalid patterns
// are skipped with a warning instead of failing startup.
func NewKeywordModerationHook(patterns []string) *KeywordModerationHook {
	h := &KeywordModerationHook{}
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			slog.Warn("keyword_moderation: ignoring invalid pattern", "pattern", p, "err", err)
			continue
		}
		h.patterns = append(h.patterns, re)
	}
	return h
}

func (h *KeywordModerationHook) PreCall(_ context.Context, hc *domain.HookContext) error {
	if len(h.patterns) == 0 {
		return nil
	}
	messages, ok := chatMessages(hc.Body)
	if !ok {
		return nil // cannot inspect — not a moderation decision
	}
	for _, msg := range messages {
		text := messageText(msg.Content)
		for _, re := range h.patterns {
			if re.MatchString(text) {
				return &domain.HookRejectError{Status: http.StatusBadRequest, Message: "request blocked by keyword moderation"}
			}
		}
	}
	return nil
}

// promptInjectionPatterns is a small, deterministic set of heuristics that
// flag common attempts to override the system prompt or remove safety rules.
var promptInjectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|earlier)\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|earlier)\s+instructions`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(jailbroken|unrestricted|without\s+(rules|restrictions))`),
	regexp.MustCompile(`(?i)act\s+as\s+if\s+(you\s+)?have\s+no\s+(rules|restrictions|limits)`),
}

// PromptInjectionHeuristicHook is a pre-call guardrail that blocks user
// messages matching known prompt-injection patterns. Fail-closed and local.
type PromptInjectionHeuristicHook struct{}

func (h *PromptInjectionHeuristicHook) PreCall(_ context.Context, hc *domain.HookContext) error {
	if hc.Endpoint != "" {
		return nil // chat-only
	}
	messages, ok := chatMessages(hc.Body)
	if !ok {
		return nil
	}
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		text := messageText(msg.Content)
		for _, re := range promptInjectionPatterns {
			if re.MatchString(text) {
				return &domain.HookRejectError{Status: http.StatusBadRequest, Message: "request blocked: potential prompt injection"}
			}
		}
	}
	return nil
}

// RequestLoggingHook logs every completed request and failure as structured
// slog lines. It never returns errors, so enabling it is fail-open by design.
type RequestLoggingHook struct{}

func (h *RequestLoggingHook) PostCall(_ context.Context, hc *domain.HookContext, res *domain.HookResponse) error {
	slog.Info("request completed",
		"request_id", hc.RequestID,
		"model", hc.Model,
		"provider", res.Provider,
		"status", res.StatusCode,
		"stream", res.Stream,
		"prompt_tokens", res.PromptTokens,
		"completion_tokens", res.CompletionTokens,
		"cost", res.Cost,
		"latency_ms", res.LatencyMs,
	)
	return nil
}

func (h *RequestLoggingHook) PostCallFailure(_ context.Context, hc *domain.HookContext, status int, err error) error {
	slog.Warn("request failed",
		"request_id", hc.RequestID,
		"model", hc.Model,
		"status", status,
		"error", err,
	)
	return nil
}

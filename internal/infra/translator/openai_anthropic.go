package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/sse"
)

// defaultPairs wires the implemented format pairs. Each entry lives in its
// own file (openai_anthropic.go, etc.) and registers here.
var defaultPairs = map[[2]domain.Format]pair{}

func register(from, to domain.Format, p pair) {
	defaultPairs[[2]domain.Format{from, to}] = p
}

// ------ OpenAI -> Anthropic (request) ------
//
// Translates an OpenAI chat/completions request into an Anthropic /v1/messages
// request. We carry the fields that matter for chat: messages (split into
// system + conversation), max_tokens, temperature, top_p, stop, stream.
// Tool calls and modalities are intentionally out of scope for v1.

type openaiRequest struct {
	Model       string         `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	MaxTokens   *int           `json:"max_tokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
	Stream      bool           `json:"stream"`
	User        string         `json:"user,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content json.RawMessage `json:"content"` // string or array of parts
}

type anthropicRequest struct {
	Model     string          `json:"model"`
	System    json.RawMessage `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
	Temperature *float64       `json:"temperature,omitempty"`
	TopP      *float64        `json:"top_p,omitempty"`
	Stop      []string        `json:"stop_sequences,omitempty"`
	Stream    bool            `json:"stream"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func init() {
	register(domain.FormatOpenAI, domain.FormatAnthropic, pair{
		translateRequest: translateOpenAIToAnthropicRequest,
		translateResponseJSON: translateAnthropicToOpenAIResponseJSON,
		translateResponseStream: anthropicStreamToOpenAI,
	})
	register(domain.FormatAnthropic, domain.FormatOpenAI, pair{
		// Client speaks Anthropic, upstream speaks OpenAI. We translate
		// request only; the response path is not on the hot path for this
		// direction (Anthropic-client to OpenAI-upstream is rare) and is
		// stubbed.
		translateRequest: translateAnthropicToOpenAIRequest,
		translateResponseJSON: translateOpenAIToAnthropicResponseJSON,
		translateResponseStream: openAIStreamToAnthropic,
	})
}

func translateOpenAIToAnthropicRequest(upstreamModel string, body []byte) ([]byte, error) {
	var r openaiRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("openai->anthropic: parse: %w", err)
	}
	out := anthropicRequest{Model: upstreamModel, Stream: r.Stream}
	if r.MaxTokens != nil {
		out.MaxTokens = *r.MaxTokens
	} else {
		out.MaxTokens = 4096
	}
	out.Temperature = r.Temperature
	out.TopP = r.TopP
	out.Stop = parseStop(r.Stop)
	for _, m := range r.Messages {
		if m.Role == "system" {
			out.System = m.Content
			continue
		}
		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		out.Messages = append(out.Messages, anthropicMessage{Role: role, Content: m.Content})
	}
	return json.Marshal(out)
}

func translateAnthropicToOpenAIRequest(upstreamModel string, body []byte) ([]byte, error) {
	// Best-effort for the rare direction; we surface a clear error rather
	// than silently dropping fields. This is filled in by openai_anthropic.go
	// for the request path; see translateAnthropicToOpenAIRequestImpl in
	// openai_anthropic.go.
	return translateAnthropicToOpenAIRequestImpl(upstreamModel, body)
}

func translateAnthropicToOpenAIResponseJSON(body []byte) ([]byte, error) {
	return translateAnthropicToOpenAIResponseJSONImpl(body)
}

func translateOpenAIToAnthropicResponseJSON(body []byte) ([]byte, error) {
	return translateOpenAIToAnthropicResponseJSONImpl(body)
}

func anthropicStreamToOpenAI(ctx context.Context, r io.ReadCloser) (io.ReadCloser, error) {
	return newAnthropicToOpenAIStreamReader(ctx, r)
}

func openAIStreamToAnthropic(ctx context.Context, r io.ReadCloser) (io.ReadCloser, error) {
	return newOpenAIToAnthropicStreamReader(ctx, r)
}

// parseStop normalises OpenAI's stop (string | string[] | null) into a
// string slice for Anthropic's stop_sequences.
func parseStop(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return []string{s}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}

// asStringContent returns the text from an OpenAI content field that may be
// a plain string or an array of parts. Used only for stream/event builders
// where we need a best-effort textual content.
func asStringContent(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err == nil {
		for _, p := range parts {
			if t, ok := p["type"].(string); ok && t == "text" {
				if text, ok := p["text"].(string); ok {
					s += text
				}
			}
		}
	}
	return s
}

// Usage from SSE stream events may be missing; we keep a 0 default.
type sseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens  int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// readEvent (utility) — wraps sse.ParseEvent with the SSE reader used by
// our stream adapters.
func readEvent(br *sseReader) (data string, done bool, err error) {
	return sse.ParseEvent(br.r)
}
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// OpenCode (opencode-go / opencode-zen) requires OpenCode-shaped routing
// headers on every chat request. Since 2026-09-06 the upstream rejects
// requests without x-opencode-session with a deterministic 400
// ("Request is missing x-opencode-session"), which used to burn a combo
// fallback slot and could even fail the whole combo when it was the last
// viable candidate. These headers mirror what the OpenCode client itself
// sends: a session ID (stable per conversation), a per-request ID, and a
// client identifier.
const (
	opencodeHeaderSession   = "x-opencode-session"
	opencodeHeaderRequest   = "x-opencode-request"
	opencodeHeaderClient    = "x-opencode-client"
	opencodeHeaderUserAgent = "User-Agent"
	opencodeClientValue     = "cli"

	// opencodeUserAgent identifies THIS gateway as the client of the OpenCode
	// relay. OpenCode asks every client to name itself "rather than a generic
	// SDK or HTTP-library name" (opencode.ai/docs/go — "Where can I use it?");
	// with no explicit value Go sends "Go-http-client/1.1", which is exactly
	// the generic library name the relay asks clients to avoid.
	opencodeUserAgent = "gorouter/1.0"

	opencodeIDLength    = 26
	opencodeIDRandomLen = opencodeIDLength - 12
	opencodeBase62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// opencodeHostedProviders lists the provider IDs that route to
// opencode-hosted models and therefore need the OpenCode headers.
var opencodeHostedProviders = map[string]bool{
	"opencode-go":  true,
	"opencode-zen": true,
}

// needsOpencodeHeaders reports whether the provider is an OpenCode-hosted
// transport that requires the OpenCode routing headers.
func needsOpencodeHeaders(providerID string) bool {
	return opencodeHostedProviders[strings.ToLower(providerID)]
}

// opencodeSessionID builds an OpenCode-shaped session ID ("ses_" + 26 chars:
// 12 hex time bits + 14 random base62 chars). The client generates one per
// conversation; the router synthesizes a fresh one per request, which is a
// valid degenerate session (routing still works, caching affinity is just
// per-request).
func opencodeSessionID(now time.Time) (string, error) {
	id, err := opencodeID(true, now)
	if err != nil {
		return "", err
	}
	return "ses_" + id, nil
}

// opencodeRequestID builds an OpenCode-shaped request/message ID ("msg_" + id).
func opencodeRequestID(now time.Time) (string, error) {
	id, err := opencodeID(false, now)
	if err != nil {
		return "", err
	}
	return "msg_" + id, nil
}

// opencodeID mirrors the OpenCode client's ID shape: 8 bytes encoding
// (unix-millis << 12 | counter), inverted when descending, emitted as 12 hex
// chars (high 6 bytes) plus 14 random base62 chars.
func opencodeID(descending bool, t time.Time) (string, error) {
	now := uint64(t.UnixMilli())*0x1000 + uint64(1)
	if descending {
		now = ^now
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], now)
	random, err := opencodeRandomBase62(opencodeIDRandomLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x%s", buf[2:], random), nil
}

func opencodeRandomBase62(length int) (string, error) {
	out := make([]byte, length)
	max := big.NewInt(int64(len(opencodeBase62Chars)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("opencode headers: %w", err)
		}
		out[i] = opencodeBase62Chars[n.Int64()]
	}
	return string(out), nil
}

// opencodeRouting carries the session-identity inputs for one upstream call.
type opencodeRouting struct {
	// ClientSession is the x-opencode-session value the CALLER supplied, if any.
	// The relay pins requests sharing a session to one backend, so a value that
	// came from the real client must survive the hop (opencode.ai/docs/go: the
	// docs tell proxies to preserve it when forwarding).
	ClientSession string
	// ConversationKey is a conversation-stable key derived from the request body
	// (see opencodeConversationKey). Used when the caller supplied no session.
	ConversationKey string
}

// applyOpencodeHeaders sets the OpenCode routing headers on the executor
// request when the target provider is OpenCode-hosted.
//
// Session identity is chosen in priority order: the caller's own session, then
// a conversation-stable derived key, then (last resort) a freshly generated ID.
// The generated fallback keeps the pre-existing behavior for bodies we cannot
// derive a conversation from — e.g. passthrough endpoints — where a per-request
// ID costs cache affinity but never fails the request.
func applyOpencodeHeaders(headers map[string]string, providerID string, routing opencodeRouting, now time.Time) error {
	if !needsOpencodeHeaders(providerID) {
		return nil
	}
	session := routing.ClientSession
	if session == "" {
		session = routing.ConversationKey
	}
	if session == "" {
		generated, err := opencodeSessionID(now)
		if err != nil {
			return err
		}
		session = generated
	}
	requestID, err := opencodeRequestID(now)
	if err != nil {
		return err
	}
	headers[opencodeHeaderSession] = session
	headers[opencodeHeaderRequest] = requestID
	headers[opencodeHeaderClient] = opencodeClientValue
	headers[opencodeHeaderUserAgent] = opencodeUserAgent
	return nil
}

// opencodeClientCtxKey stores the caller's x-opencode-session value so the
// router can preserve it without widening every signature in between.
type opencodeClientCtxKey struct{}

// WithOpencodeClientSession marks ctx with the caller's OpenCode session ID.
// An empty session is a no-op: a missing header must not shadow the derived
// conversation key.
func WithOpencodeClientSession(ctx context.Context, session string) context.Context {
	if session == "" {
		return ctx
	}
	return context.WithValue(ctx, opencodeClientCtxKey{}, session)
}

func opencodeClientSessionFromCtx(ctx context.Context) string {
	s, _ := ctx.Value(opencodeClientCtxKey{}).(string)
	return s
}

// opencodeConversationKey derives a conversation-stable OpenCode session key
// from an OpenAI-format chat body.
//
// The anchor is the FIRST user message: a client replays the same conversation
// prefix on every turn, so that message is immutable for the life of the
// conversation, while two different conversations differ. OpenCode uses the
// session header to pin a conversation to one backend for routing and prompt
// caching, so minting a fresh ID per request (the previous behavior) defeats
// exactly the affinity the header exists to create.
//
// The system message is deliberately NOT hashed: it can carry per-turn volatile
// content (timestamps, memory, injected context), and an unstable key breaks the
// affinity silently — the failure mode is invisible (cache misses), so the
// derivation prefers the most stable anchor over the most discriminating one.
// Context compaction may rewrite the first user message; the key then rotates
// once, which is acceptable because the whole prompt changed at that point.
//
// Returns "" when the body carries no readable user message (passthrough
// endpoints, non-chat shapes); the caller falls back to a generated ID.
func opencodeConversationKey(body []byte) string {
	text := firstUserMessageText(body)
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return "ses_" + opencodeOpaqueID(sum[:])
}

// firstUserMessageText returns the text of the first user-role message, or ""
// when the body is not an OpenAI chat request or carries no user message.
func firstUserMessageText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var envelope struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	for _, m := range envelope.Messages {
		if m.Role != "user" {
			continue
		}
		return contentText(m.Content)
	}
	return ""
}

// contentText flattens a message content field into plain text: a bare string,
// or an array of parts ({"type":"text"|"input_text","text":...}).
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// opencodeOpaqueID renders digest bytes in the OpenCode session ID shape:
// 12 hex chars + 14 base62 chars, i.e. the same 26-char body opencodeID emits.
func opencodeOpaqueID(digest []byte) string {
	if len(digest) < 20 {
		return ""
	}
	var b strings.Builder
	for _, by := range digest[:6] {
		fmt.Fprintf(&b, "%02x", by)
	}
	for _, by := range digest[6:20] {
		b.WriteByte(opencodeBase62Chars[int(by)%len(opencodeBase62Chars)])
	}
	return b.String()
}

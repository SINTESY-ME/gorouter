package app

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNeedsOpencodeHeaders(t *testing.T) {
	if !needsOpencodeHeaders("opencode-go") {
		t.Error("opencode-go must require OpenCode headers")
	}
	if !needsOpencodeHeaders("opencode-zen") {
		t.Error("opencode-zen must require OpenCode headers")
	}
	if needsOpencodeHeaders("openai") {
		t.Error("openai must not require OpenCode headers")
	}
	if needsOpencodeHeaders("") {
		t.Error("empty provider must not require OpenCode headers")
	}
}

func TestApplyOpencodeHeadersNoopForOtherProviders(t *testing.T) {
	headers := map[string]string{}
	if err := applyOpencodeHeaders(headers, "openai", opencodeRouting{}, time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(headers) != 0 {
		t.Errorf("expected no headers for non-opencode provider, got %v", headers)
	}
}

func TestApplyOpencodeHeadersSetsRoutingHeaders(t *testing.T) {
	headers := map[string]string{}
	if err := applyOpencodeHeaders(headers, "opencode-go", opencodeRouting{}, time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	session, ok := headers["x-opencode-session"]
	if !ok {
		t.Fatal("x-opencode-session must be set")
	}
	if !strings.HasPrefix(session, "ses_") {
		t.Errorf("session ID must be OpenCode-shaped (ses_*), got %q", session)
	}
	if len(session) != len("ses_")+26 {
		t.Errorf("session ID must be ses_ + 26 chars, got length %d", len(session))
	}
	request, ok := headers["x-opencode-request"]
	if !ok {
		t.Fatal("x-opencode-request must be set")
	}
	if !strings.HasPrefix(request, "msg_") {
		t.Errorf("request ID must be OpenCode-shaped (msg_*), got %q", request)
	}
	if headers["x-opencode-client"] != "cli" {
		t.Errorf("x-opencode-client must be %q, got %q", "cli", headers["x-opencode-client"])
	}
}

// The relay tells clients to name themselves instead of leaning on the HTTP
// library default; without an explicit value Go sends "Go-http-client/1.1".
func TestApplyOpencodeHeadersSetsOwnUserAgent(t *testing.T) {
	headers := map[string]string{}
	if err := applyOpencodeHeaders(headers, "opencode-go", opencodeRouting{}, time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := headers["User-Agent"]; got != opencodeUserAgent {
		t.Errorf("User-Agent must be %q, got %q", opencodeUserAgent, got)
	}
	if strings.Contains(headers["User-Agent"], "Go-http-client") {
		t.Error("User-Agent must not be the generic Go library name")
	}
}

func TestApplyOpencodeHeadersPreservesCallerSession(t *testing.T) {
	headers := map[string]string{}
	routing := opencodeRouting{ClientSession: "ses_from_the_caller", ConversationKey: "ses_derived"}
	if err := applyOpencodeHeaders(headers, "opencode-go", routing, time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := headers["x-opencode-session"]; got != "ses_from_the_caller" {
		t.Errorf("caller session must win over the derived key, got %q", got)
	}
}

func TestApplyOpencodeHeadersUsesConversationKeyOverGenerated(t *testing.T) {
	headers := map[string]string{}
	routing := opencodeRouting{ConversationKey: opencodeConversationKey([]byte(
		`{"messages":[{"role":"user","content":"same conversation"}]}`))}
	if routing.ConversationKey == "" {
		t.Fatal("conversation key must be derived from a user message")
	}
	if err := applyOpencodeHeaders(headers, "opencode-go", routing, time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := headers["x-opencode-session"]; got != routing.ConversationKey {
		t.Errorf("derived conversation key must be used, got %q", got)
	}
}

func TestOpencodeConversationKeyStableAcrossTurns(t *testing.T) {
	// Same conversation, later turn: history grew, first user message unchanged.
	first := []byte(`{"messages":[{"role":"system","content":"sys A"},{"role":"user","content":"build me a router"},{"role":"assistant","content":"ok"}]}`)
	later := []byte(`{"messages":[{"role":"system","content":"sys A with a per-turn timestamp 12:01"},{"role":"user","content":"build me a router"},{"role":"assistant","content":"ok"},{"role":"user","content":"now add tests"},{"role":"tool","content":"...tool output..."}]}`)
	if a, b := opencodeConversationKey(first), opencodeConversationKey(later); a != b {
		t.Errorf("key must be stable across turns of one conversation: %q != %q", a, b)
	}
	if a := opencodeConversationKey(first); !strings.HasPrefix(a, "ses_") || len(a) != len("ses_")+26 {
		t.Errorf("derived key must be OpenCode-shaped (ses_ + 26 chars), got %q", a)
	}
}

func TestOpencodeConversationKeyDiffersPerConversation(t *testing.T) {
	a := opencodeConversationKey([]byte(`{"messages":[{"role":"user","content":"conversation one"}]}`))
	b := opencodeConversationKey([]byte(`{"messages":[{"role":"user","content":"conversation two"}]}`))
	if a == b {
		t.Error("different first user messages must produce different keys")
	}
}

func TestOpencodeConversationKeyHandlesContentParts(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"part one "},{"type":"text","text":"part two"}]}]}`)
	got := opencodeConversationKey(body)
	want := opencodeConversationKey([]byte(`{"messages":[{"role":"user","content":"part one part two"}]}`))
	if got == "" || got != want {
		t.Errorf("array content parts must hash like their concatenated text: %q vs %q", got, want)
	}
}

func TestOpencodeClientSessionContextRoundTrip(t *testing.T) {
	ctx := WithOpencodeClientSession(context.Background(), "ses_from_caller")
	if got := opencodeClientSessionFromCtx(ctx); got != "ses_from_caller" {
		t.Errorf("session must survive the context hop, got %q", got)
	}
	// An empty caller session must NOT shadow the derived conversation key.
	bare := WithOpencodeClientSession(context.Background(), "")
	if got := opencodeClientSessionFromCtx(bare); got != "" {
		t.Errorf("empty session must be a no-op, got %q", got)
	}
	if got := opencodeClientSessionFromCtx(context.Background()); got != "" {
		t.Errorf("absent session must read as empty, got %q", got)
	}
}

func TestOpencodeConversationKeyEmptyWhenUnderivable(t *testing.T) {
	for name, body := range map[string]string{
		"no messages":     `{"messages":[]}`,
		"system only":     `{"messages":[{"role":"system","content":"only a system prompt"}]}`,
		"empty body":      ``,
		"not json":        `model=whatever`,
		"no messages key": `{"prompt":"completion shape"}`,
	} {
		if got := opencodeConversationKey([]byte(body)); got != "" {
			t.Errorf("%s: expected no key (caller falls back to a generated ID), got %q", name, got)
		}
	}
}

func TestOpencodeSessionIDsAreUniqueAndShaped(t *testing.T) {
	now := time.Now()
	seen := map[string]bool{}
	sessionRe := regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
	for i := 0; i < 50; i++ {
		id, err := opencodeSessionID(now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !sessionRe.MatchString(id) {
			t.Errorf("session %d %q does not match OpenCode shape", i, id)
		}
		if seen[id] {
			t.Errorf("duplicate session ID %q", id)
		}
		seen[id] = true
	}
}

func TestOpencodeRequestIDShaped(t *testing.T) {
	id, err := opencodeRequestID(time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(id, "msg_") {
		t.Errorf("request ID must be msg_*, got %q", id)
	}
	if len(id) != len("msg_")+26 {
		t.Errorf("request ID must be msg_ + 26 chars, got length %d", len(id))
	}
}

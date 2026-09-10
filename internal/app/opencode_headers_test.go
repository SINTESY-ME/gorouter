package app

import (
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
	if err := applyOpencodeHeaders(headers, "openai", time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(headers) != 0 {
		t.Errorf("expected no headers for non-opencode provider, got %v", headers)
	}
}

func TestApplyOpencodeHeadersSetsRoutingHeaders(t *testing.T) {
	headers := map[string]string{}
	if err := applyOpencodeHeaders(headers, "opencode-go", time.Now()); err != nil {
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
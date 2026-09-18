package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/executor"
)

// TestOpencodeHeadersReachTheWire drives the REAL executor against a local
// server and asserts what the upstream would actually receive. The unit tests
// above only prove the header map is populated; this one proves the map becomes
// an HTTP header, which is the part the OpenCode relay validates.
func TestOpencodeHeadersReachTheWire(t *testing.T) {
	var (
		mu       sync.Mutex
		received []http.Header
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Clone())
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	exec := executor.NewHTTPExecutor(10 * time.Second)
	cfg := &domain.ProviderConfig{
		ID:              "opencode-go",
		Format:          domain.FormatOpenAI,
		ResolvedBaseURL: srv.URL,
	}
	conn := &domain.Connection{ID: "test-conn", ProviderID: "opencode-go", APIKey: "test-key"}

	// Two turns of ONE conversation: the history grows, the first user message
	// does not change. The session ID the upstream sees must be identical.
	firstTurn := []byte(`{"messages":[{"role":"user","content":"wire test conversation"}]}`)
	laterTurn := []byte(`{"messages":[{"role":"user","content":"wire test conversation"},{"role":"assistant","content":"ok"},{"role":"user","content":"next turn"}]}`)

	for _, body := range [][]byte{firstTurn, laterTurn} {
		headers := map[string]string{}
		routing := opencodeRouting{ConversationKey: opencodeConversationKey(body)}
		if err := applyOpencodeHeaders(headers, "opencode-go", routing, time.Now()); err != nil {
			t.Fatalf("applyOpencodeHeaders: %v", err)
		}
		res, err := exec.Execute(context.Background(), domain.ExecuteRequest{
			ProviderID:    "opencode-go",
			Connection:    conn,
			Config:        cfg,
			UpstreamModel: "deepseek-v4.1-flash",
			Body:          io.NopCloser(strings.NewReader(string(body))),
			Headers:       headers,
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		res.Body.Close()
	}

	if len(received) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(received))
	}
	for i, h := range received {
		if got := h.Get("User-Agent"); got != opencodeUserAgent {
			t.Errorf("turn %d: wire User-Agent = %q, want %q", i+1, got, opencodeUserAgent)
		}
		if strings.Contains(h.Get("User-Agent"), "Go-http-client") {
			t.Errorf("turn %d: wire User-Agent leaked the generic Go library name", i+1)
		}
		if got := h.Get("x-opencode-client"); got != opencodeClientValue {
			t.Errorf("turn %d: wire x-opencode-client = %q, want %q", i+1, got, opencodeClientValue)
		}
		if !strings.HasPrefix(h.Get("x-opencode-request"), "msg_") {
			t.Errorf("turn %d: wire x-opencode-request = %q, want a msg_* id", i+1, h.Get("x-opencode-request"))
		}
	}
	first, second := received[0].Get("x-opencode-session"), received[1].Get("x-opencode-session")
	if first == "" {
		t.Fatal("wire x-opencode-session must be set")
	}
	if first != second {
		t.Errorf("session must be stable across turns of one conversation: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "ses_") || len(first) != len("ses_")+26 {
		t.Errorf("wire session must be OpenCode-shaped (ses_ + 26 chars), got %q", first)
	}
	// Request IDs are per-call on purpose: a stable session must not freeze them.
	if received[0].Get("x-opencode-request") == received[1].Get("x-opencode-request") {
		t.Error("x-opencode-request must differ per call")
	}
}

// A caller-supplied session must reach the wire unchanged: the docs tell proxies
// to preserve it when forwarding, and a rewritten value breaks affinity for the
// real client.
func TestOpencodeCallerSessionReachesTheWire(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-opencode-session")
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	headers := map[string]string{}
	routing := opencodeRouting{
		ClientSession:   "ses_caller_owns_this_one",
		ConversationKey: opencodeConversationKey([]byte(`{"messages":[{"role":"user","content":"ignored"}]}`)),
	}
	if err := applyOpencodeHeaders(headers, "opencode-go", routing, time.Now()); err != nil {
		t.Fatalf("applyOpencodeHeaders: %v", err)
	}
	exec := executor.NewHTTPExecutor(10 * time.Second)
	res, err := exec.Execute(context.Background(), domain.ExecuteRequest{
		ProviderID: "opencode-go",
		Connection: &domain.Connection{ID: "c", ProviderID: "opencode-go", APIKey: "k"},
		Config:     &domain.ProviderConfig{ID: "opencode-go", Format: domain.FormatOpenAI, ResolvedBaseURL: srv.URL},
		Body:       io.NopCloser(strings.NewReader(`{"messages":[]}`)),
		Headers:    headers,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	res.Body.Close()
	if got != "ses_caller_owns_this_one" {
		t.Errorf("caller session must reach the wire unchanged, got %q", got)
	}
}

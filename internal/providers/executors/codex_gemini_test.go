package executors

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestCodexExecutorSendsIdentityHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	e := &CodexExecutor{Client: srv.Client(), BaseURL: srv.URL + "/responses"}
	conn := &domain.Connection{
		APIKey: "access-token",
		Meta:   `{"account_id":"acct-123"}`,
	}
	res, err := e.Execute(context.Background(), domain.ExecuteRequest{
		Connection:    conn,
		Body:          io.NopCloser(strings.NewReader(`{"model":"gpt-5-codex"}`)),
		Stream:        true,
		UpstreamModel: "gpt-5-codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	for key, want := range map[string]string{
		"Authorization":                     "Bearer access-token",
		"Chatgpt-Account-Id":                "acct-123",
		"Openai-Beta":                       "responses=experimental",
		"Originator":                        "codex_cli_rs",
		"Version":                           "0.144.1",
		"X-Openai-Internal-Codex-Residency": "",
	} {
		if key == "X-Openai-Internal-Codex-Residency" {
			continue
		}
		if got.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, got.Get(key), want)
		}
	}
}

func TestCodexExecutorSendsTokenResidency(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-openai-internal-codex-residency")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	e := &CodexExecutor{Client: srv.Client(), BaseURL: srv.URL}
	access := "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9kYXRhX3Jlc2lkZW5jeSI6ImV1In19.sig"
	_, err := e.Execute(context.Background(), domain.ExecuteRequest{
		Connection: &domain.Connection{APIKey: access},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "eu" {
		t.Fatalf("residency = %q, want eu", got)
	}
}

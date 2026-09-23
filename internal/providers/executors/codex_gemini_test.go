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
		"Version":                           CodexClientVersion,
		"User-Agent":                        "codex_cli_rs/" + CodexClientVersion,
		"X-Codex-Beta-Features":             "responses_websockets",
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

// TestCodexExecutorForwardsCallerVersion proves the caller's own Codex version
// travels upstream when the caller reported one, and that the gateway claim is
// kept when it did not (a non-Codex caller must not be given a fabricated
// lower version, which would gate models off for callers that never asked).
func TestCodexExecutorForwardsCallerVersion(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "caller reported a version",
			ctx:  domain.WithCodexClientVersion(context.Background(), "0.154.0"),
			want: "0.154.0",
		},
		{
			name: "no caller identity",
			ctx:  context.Background(),
			want: CodexClientVersion,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()

			e := &CodexExecutor{Client: srv.Client(), BaseURL: srv.URL}
			res, err := e.Execute(tc.ctx, domain.ExecuteRequest{
				Connection: &domain.Connection{APIKey: "tok"},
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Stream:     true,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()

			if got.Get("Version") != tc.want {
				t.Errorf("Version = %q, want %q", got.Get("Version"), tc.want)
			}
			if wantUA := "codex_cli_rs/" + tc.want; got.Get("User-Agent") != wantUA {
				t.Errorf("User-Agent = %q, want %q", got.Get("User-Agent"), wantUA)
			}
		})
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

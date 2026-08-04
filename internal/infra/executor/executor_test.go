package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

func slowUpstream(delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{}`)
	}))
}

func TestExecutePerRequestTimeout(t *testing.T) {
	up := slowUpstream(300 * time.Millisecond)
	defer up.Close()

	e := NewHTTPExecutor(time.Second)
	req := domain.ExecuteRequest{
		ProviderID:    "openai",
		Connection:    &domain.Connection{ID: "c1", APIKey: "test"},
		Config:        &domain.ProviderConfig{ID: "openai", ResolvedBaseURL: up.URL, Format: domain.FormatOpenAI},
		UpstreamModel: "gpt-4",
		Body:          io.NopCloser(strings.NewReader(`{}`)),
		Timeout:       50 * time.Millisecond,
	}
	start := time.Now()
	_, err := e.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected a timeout error with a 50ms per-request timeout")
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatal("per-request timeout did not bound the call")
	}
}

func TestExecuteDefaultTimeout(t *testing.T) {
	up := slowUpstream(300 * time.Millisecond)
	defer up.Close()

	e := NewHTTPExecutor(2 * time.Second)
	req := domain.ExecuteRequest{
		ProviderID:    "openai",
		Connection:    &domain.Connection{ID: "c1", APIKey: "test"},
		Config:        &domain.ProviderConfig{ID: "openai", ResolvedBaseURL: up.URL, Format: domain.FormatOpenAI},
		UpstreamModel: "gpt-4",
		Body:          io.NopCloser(strings.NewReader(`{}`)),
	}
	res, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("default timeout (2s) should allow a 300ms call: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// A provider with no /models route is not a failure: sync falls back to the
// catalog preset. Before this, a 404 surfaced in the dashboard as
// "fetch models: status 404" and hid the preset behind it.
func TestModelFetcherTreatsMissingModelRouteAsNoList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewHTTPModelFetcher()
	models, err := f.Fetch(context.Background(),
		&domain.Connection{ProviderID: "plain", APIKey: "k"},
		&domain.ProviderConfig{ID: "plain", Format: domain.FormatOpenAI, Auth: domain.AuthBearer, ResolvedBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("a 404 on /models must not be an error, got: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected no models, got %d", len(models))
	}
}

// The Cloud Code Assist listing is a POST to v1internal:fetchAvailableModels
// with the product's own user agent and the OAuth bearer token — the /models
// route this used to call does not exist.
func TestModelFetcherUsesCloudCodeListing(t *testing.T) {
	var gotPath, gotMethod, gotUA, gotAuth string
	var sentBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotUA, gotAuth = r.URL.Path, r.Method, r.UserAgent(), r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sentBody)
		_, _ = w.Write([]byte(`{"models":{
			"gemini-3.6-flash-high":{"isInternal":false,"maxTokens":1048576},
			"chat_20706":{"isInternal":true,"maxTokens":16384},
			"tab_flash_lite_preview":{"maxTokens":16384},
			"claude-sonnet-4-6":{"maxTokens":250000}
		}}`))
	}))
	defer srv.Close()

	f := NewHTTPModelFetcher()
	conn := &domain.Connection{ProviderID: "antigravity", APIKey: "token-abc", Meta: `{"project_id":"proj-1"}`}
	cfg := &domain.ProviderConfig{ID: "antigravity", Format: domain.FormatGemini, Auth: domain.AuthBearer, ResolvedBaseURL: srv.URL}
	models, err := f.Fetch(context.Background(), conn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1internal:fetchAvailableModels" {
		t.Fatalf("called %s %s, want POST /v1internal:fetchAvailableModels", gotMethod, gotPath)
	}
	if gotUA != "antigravity/1.107.0 linux/amd64" {
		t.Fatalf("user agent %q does not identify the client", gotUA)
	}
	if gotAuth != "Bearer token-abc" {
		t.Fatalf("authorization header %q is not the OAuth bearer token", gotAuth)
	}
	if project, _ := sentBody["project"].(string); project != "proj-1" {
		t.Fatalf("request body %v did not carry the stored project", sentBody)
	}

	ids := map[string]int{}
	for _, m := range models {
		ids[m.ID] = m.Metadata.Context
	}
	if len(models) != 2 {
		t.Fatalf("expected the 2 callable models, got %d: %v", len(models), models)
	}
	if ids["gemini-3.6-flash-high"] != 1048576 {
		t.Fatalf("model context not carried from maxTokens: %v", ids)
	}
	if _, ok := ids["chat_20706"]; ok {
		t.Fatal("internal model leaked into the catalog")
	}
	if _, ok := ids["tab_flash_lite_preview"]; ok {
		t.Fatal("tab-completion model leaked into the catalog")
	}
}

// A listing denial is the account's business, not a sync failure: the catalog
// preset stands and sync reports success.
func TestModelFetcherCloudCodeDenialIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"The caller does not have permission"}}`))
	}))
	defer srv.Close()

	f := NewHTTPModelFetcher()
	models, err := f.Fetch(context.Background(),
		&domain.Connection{ProviderID: "gemini-cli", APIKey: "token"},
		&domain.ProviderConfig{ID: "gemini-cli", Format: domain.FormatGemini, Auth: domain.AuthBearer, ResolvedBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("a licensing denial must not break sync, got: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected no models on denial, got %d", len(models))
	}
}

// Genuine transport failures still have to surface: swallowing them would let
// a broken provider look like an empty one.
func TestModelFetcherReportsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := NewHTTPModelFetcher()
	_, err := f.Fetch(context.Background(),
		&domain.Connection{ProviderID: "plain", APIKey: "k"},
		&domain.ProviderConfig{ID: "plain", Format: domain.FormatOpenAI, Auth: domain.AuthBearer, ResolvedBaseURL: srv.URL})
	if err == nil {
		t.Fatal("a 500 from the provider must be reported")
	}
}

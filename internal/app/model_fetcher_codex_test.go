package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/providers/executors"
)

// testToken builds a bearer-looking value at runtime on purpose: a
// secret-shaped string literal in source gets rewritten by redaction and
// breaks the file it sits in.
var testToken = strings.Repeat("a", 24)

// The codex listing route exists but requires ?client_version= — without it
// the server answers 400 and the dashboard showed "fetch models: status 400".
// The claim must be the executor's own: the listing gate and the call gate are
// the same comparison, and disagreeing claims would offer models the call
// gate then rejects ("model is not supported").
func TestModelFetcherUsesCodexListing(t *testing.T) {
	var gotPath, gotQuery, gotUA, gotAuth, gotAccount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotUA, gotAuth = r.URL.Path, r.UserAgent(), r.Header.Get("Authorization")
		gotQuery = r.URL.Query().Get("client_version")
		gotAccount = r.Header.Get("chatgpt-account-id")
		_, _ = w.Write([]byte(`{"models":[
			{"slug":"gpt-5.6-terra","display_name":"GPT-5.6 Terra","context_window":272000,
			 "input_modalities":["text","image"],"supports_parallel_tool_calls":true,
			 "default_reasoning_level":"medium","visibility":"list",
			 "supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"},{"effort":"ultra"}]},
			{"slug":"gpt-5.5","context_window":128000,"input_modalities":["text"],
			 "default_reasoning_level":"medium",
			 "supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]},
			{"slug":"","context_window":1}
		]}`))
	}))
	defer srv.Close()

	f := NewHTTPModelFetcher()
	conn := &domain.Connection{ProviderID: "codex", APIKey: testToken, Meta: `{"account_id":"acct-1"}`}
	cfg := &domain.ProviderConfig{ID: "codex", Format: domain.FormatResponses, Auth: domain.AuthBearer, ResolvedBaseURL: srv.URL}
	models, err := f.Fetch(context.Background(), conn, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/models" {
		t.Fatalf("listing path = %q, want /models", gotPath)
	}
	if gotQuery != executors.CodexClientVersion {
		t.Fatalf("client_version = %q, want the executor claim %q", gotQuery, executors.CodexClientVersion)
	}
	if gotUA != "codex_cli_rs/"+executors.CodexClientVersion {
		t.Fatalf("user agent = %q, want the executor identity", gotUA)
	}
	if gotAuth != "Bearer "+testToken {
		t.Fatalf("authorization = %q, want the connection bearer", gotAuth)
	}
	if gotAccount != "acct-1" {
		t.Fatalf("chatgpt-account-id = %q, want the OAuth meta's account", gotAccount)
	}

	if len(models) != 2 {
		t.Fatalf("models = %d, want 2 (slug-less entries dropped)", len(models))
	}
	terra := models[0]
	if terra.ID != "gpt-5.6-terra" || terra.ContextLength != 272000 || terra.Metadata.Context != 272000 {
		t.Fatalf("terra = %+v, want slug as id and the stated context window", terra)
	}
	if !terra.Metadata.SupportsVision || !terra.Metadata.SupportsToolCall {
		t.Fatalf("terra modalities/tool calls not mapped: %+v", terra.Metadata)
	}
	// "ultra" is outside the canonical ladder and is dropped like every other
	// unknown level; the default "medium" survives.
	wantLadder := []string{"low", "medium", "high", "xhigh", "max"}
	if !reflect.DeepEqual(terra.Metadata.Reasoning.Efforts, wantLadder) {
		t.Fatalf("terra ladder = %v, want %v", terra.Metadata.Reasoning.Efforts, wantLadder)
	}
	if terra.Metadata.Reasoning.DefaultEffort != "medium" || !terra.Metadata.Reasoning.EffortsStated {
		t.Fatalf("terra default/stated = %+v", terra.Metadata.Reasoning)
	}
	if !reflect.DeepEqual(terra.ReasoningEfforts, wantLadder) {
		t.Fatalf("terra public ladder = %v", terra.ReasoningEfforts)
	}

	five := models[1]
	if five.Metadata.SupportsVision || five.Metadata.SupportsToolCall {
		t.Fatalf("gpt-5.5 must not claim vision/tool calls it did not state: %+v", five.Metadata)
	}
}

// A refusal from a route that exists is an auth or account problem the
// operator must see; only a missing listing route degrades to "no list".
func TestModelFetcherCodexSurfacesRefusals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	f := NewHTTPModelFetcher()
	models, err := f.Fetch(context.Background(),
		&domain.Connection{ProviderID: "codex", APIKey: testToken},
		&domain.ProviderConfig{ID: "codex", Format: domain.FormatResponses, Auth: domain.AuthBearer, ResolvedBaseURL: srv.URL})
	if err == nil || len(models) != 0 {
		t.Fatalf("want surfaced error and no models, got models=%d err=%v", len(models), err)
	}
}

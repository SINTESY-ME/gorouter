package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

func TestWebhookLoggingHookPosts(t *testing.T) {
	var mu sync.Mutex
	var got webhookPayload
	received := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		mu.Lock()
		received <- struct{}{}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := NewWebhookLoggingHook(ts.URL)
	if err := h.PostCall(context.Background(), &domain.HookContext{RequestID: "r1", Endpoint: ""}, &domain.HookResponse{
		Model: "gpt-4o", Provider: "openai", StatusCode: 200, PromptTokens: 10, CompletionTokens: 5, Cost: 0.001, LatencyMs: 100,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook did not receive the event")
	}
	if got.Event != "request.completed" || got.Model != "gpt-4o" || got.PromptTokens != 10 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestWebhookLoggingHookNoURL(t *testing.T) {
	h := NewWebhookLoggingHook("")
	if err := h.PostCall(context.Background(), &domain.HookContext{}, &domain.HookResponse{}); err != nil {
		t.Fatal(err)
	}
	if err := h.PostCallFailure(context.Background(), &domain.HookContext{}, 500, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookLoggingHookFailurePayload(t *testing.T) {
	received := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := NewWebhookLoggingHook(ts.URL)
	if err := h.PostCallFailure(context.Background(), &domain.HookContext{RequestID: "r2", Model: "gpt-4"}, 503, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook did not receive the failure event")
	}
}

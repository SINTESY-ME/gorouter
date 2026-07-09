package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// mockExecutor implements domain.Executor for testing. By default it returns
// status/body for every call; if failModels is populated, the model (from
// req.UpstreamModel) is looked up and failModels[model] overrides the
// default status. `called` records the sequence of UpstreamModel values per
// Execute call, in order, so tests can assert which models were attempted.
type mockExecutor struct {
	mu         sync.Mutex
	calls      int
	status      int
	body       string
	stream     bool
	headers     http.Header
	failModels map[string]int // model -> HTTP status (overrides default)
	called     []string       // sequence of UpstreamModel per Execute call
}

func (m *mockExecutor) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	m.mu.Lock()
	m.calls++
	model := req.UpstreamModel
	status := m.status
	if m.failModels != nil {
		if s, ok := m.failModels[model]; ok {
			status = s
		}
	}
	m.called = append(m.called, model)
	m.mu.Unlock()
	hdr := m.headers
	if hdr == nil {
		hdr = http.Header{}
		hdr.Set("Content-Type", "application/json")
	}
	return &domain.ExecuteResult{
		StatusCode: status,
		Headers:    hdr,
		Body:       io.NopCloser(bytes.NewReader([]byte(m.body))),
		Stream:     m.stream,
	}, nil
}

// mockComboRepo implements domain.ComboRepo for testing.
type mockComboRepo struct {
	combos map[string]*domain.Combo
}

func (r *mockComboRepo) List(ctx context.Context) ([]domain.Combo, error) {
	var out []domain.Combo
	for _, c := range r.combos {
		out = append(out, *c)
	}
	return out, nil
}
func (r *mockComboRepo) Get(ctx context.Context, id string) (*domain.Combo, error) {
	if c, ok := r.combos[id]; ok {
		return c, nil
	}
	return nil, domain.ErrNotFound
}
func (r *mockComboRepo) GetByName(ctx context.Context, name string) (*domain.Combo, error) {
	for _, c := range r.combos {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *mockComboRepo) Create(ctx context.Context, c *domain.Combo) error { return nil }
func (r *mockComboRepo) Update(ctx context.Context, c *domain.Combo) error { return nil }
func (r *mockComboRepo) Delete(ctx context.Context, id string) error        { return nil }

// mockConnectionRepo implements domain.ConnectionRepo for testing.
type mockConnectionRepo struct {
	conns []domain.Connection
}

func (r *mockConnectionRepo) List(ctx context.Context) ([]domain.Connection, error) {
	return r.conns, nil
}
func (r *mockConnectionRepo) ListByProvider(ctx context.Context, providerID string) ([]domain.Connection, error) {
	var out []domain.Connection
	for _, c := range r.conns {
		if c.ProviderID == providerID {
			out = append(out, c)
		}
	}
	return out, nil
}
func (r *mockConnectionRepo) Get(ctx context.Context, id string) (*domain.Connection, error) {
	for _, c := range r.conns {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *mockConnectionRepo) Create(ctx context.Context, c *domain.Connection) error    { return nil }
func (r *mockConnectionRepo) Update(ctx context.Context, c *domain.Connection) error    { return nil }
func (r *mockConnectionRepo) Delete(ctx context.Context, id string) error               { return nil }
func (r *mockConnectionRepo) Reorder(ctx context.Context, orderedIDs []string) error    { return nil }
func (r *mockConnectionRepo) SetRateLimited(ctx context.Context, id string, until interface{}) error {
	return nil
}

// mockUsageRepo implements domain.UsageRepo for testing.
type mockUsageRepo struct {
	mu      sync.Mutex
	entries []domain.UsageEntry
}

func (r *mockUsageRepo) Record(ctx context.Context, e domain.UsageEntry) error {
	r.mu.Lock()
	r.entries = append(r.entries, e)
	r.mu.Unlock()
	return nil
}
func (r *mockUsageRepo) Stats(ctx context.Context, period string) (*domain.UsageStats, error) {
	return &domain.UsageStats{}, nil
}
func (r *mockUsageRepo) History(ctx context.Context, limit int) ([]domain.UsageEntry, error) {
	return r.entries, nil
}

// mockTranslator implements domain.Translator as passthrough (OpenAI->OpenAI).
type mockTranslator struct{}

func (m *mockTranslator) Supports(from, to domain.Format) bool { return true }
func (m *mockTranslator) TranslateRequest(from, to domain.Format, upstreamModel string, body []byte) ([]byte, error) {
	if upstreamModel == "" {
		return body, nil
	}
	return rewriteModel(body, upstreamModel), nil
}
func (m *mockTranslator) TranslateResponseJSON(from, to domain.Format, body []byte) ([]byte, error) {
	return body, nil
}
func (m *mockTranslator) TranslateResponseStream(ctx context.Context, from, to domain.Format, r io.ReadCloser) (io.ReadCloser, error) {
	return r, nil
}

// rewriteModel is duplicated from the translator package for the mock.
func rewriteModel(body []byte, upstreamModel string) []byte {
	if upstreamModel == "" {
		return body
	}
	return body
}

func TestRouteSingle_NonStreaming_UsageRecorded(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID:         "c1",
			ProviderID: "openai",
			Name:       "test",
			Format:     domain.FormatOpenAI,
			Auth:       domain.AuthBearer,
			IsActive:   true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	modelStr, _ := extractModel(body)
	res, err := srv.RouteChat(context.Background(), body, modelStr, false, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	// Read and close the body to trigger usage recording
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if !bytes.Contains(buf, []byte("hello")) {
		t.Error("body should contain 'hello'")
	}

	// Wait briefly for usage recording (happens in Close)
	time.Sleep(50 * time.Millisecond)

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.entries) != 1 {
		t.Fatalf("usage entries: got %d want 1", len(usage.entries))
	}
	e := usage.entries[0]
	if e.PromptTokens != 10 {
		t.Errorf("prompt tokens: got %d want 10", e.PromptTokens)
	}
	if e.CompletionTokens != 20 {
		t.Errorf("completion tokens: got %d want 20", e.CompletionTokens)
	}
	if e.ApiKey != "test-key" {
		t.Errorf("api key: got %q want 'test-key'", e.ApiKey)
	}
}

func TestRouteCombo_OrderedFallback(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "mycombo",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID:         "c1",
			ProviderID: "openai",
			Name:       "test",
			Format:     domain.FormatOpenAI,
			Auth:       domain.AuthBearer,
			IsActive:   true,
		}},
	}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"mycombo","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	res.Body.Close()
	time.Sleep(50 * time.Millisecond)
}

func TestRouteSingle_ModelNotFound(t *testing.T) {
	srv := NewRouterService(&mockComboRepo{}, &mockConnectionRepo{}, &mockExecutor{}, &mockTranslator{}, &mockUsageRepo{})
	body := []byte(`{"model":"nonexistent","messages":[]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
}

func TestRoutePassthrough_Embeddings_UsageRecorded(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"object":"list","data":[{"embedding":[0.1,0.2],"index":0}],"usage":{"prompt_tokens":8,"total_tokens":8}}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID:         "c1",
			ProviderID: "openai",
			Name:       "test",
			Format:     domain.FormatOpenAI,
			Auth:       domain.AuthBearer,
			IsActive:   true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/text-embedding-3-small","input":"hello"}`)
	res, err := srv.RoutePassthrough(context.Background(), body, extractModelMust(body), "embeddings", "test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	if res.Stream {
		t.Error("passthrough should not be streaming")
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Contains(buf, []byte("embedding")) {
		t.Error("body should contain 'embedding'")
	}
	time.Sleep(50 * time.Millisecond)

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.entries) != 1 {
		t.Fatalf("usage entries: got %d want 1", len(usage.entries))
	}
	e := usage.entries[0]
	if e.Endpoint != "embeddings" {
		t.Errorf("endpoint: got %q want 'embeddings'", e.Endpoint)
	}
	if e.PromptTokens != 8 {
		t.Errorf("prompt tokens: got %d want 8", e.PromptTokens)
	}
}

func TestRoutePassthrough_Images(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"created":123,"data":[{"url":"https://example.com/img.png"}]}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID:         "c1",
			ProviderID: "openai",
			Name:       "test",
			Format:     domain.FormatOpenAI,
			Auth:       domain.AuthBearer,
			IsActive:   true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/dall-e-3","prompt":"a cat"}`)
	res, err := srv.RoutePassthrough(context.Background(), body, extractModelMust(body), "images/generations", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Contains(buf, []byte("img.png")) {
		t.Error("body should contain image url")
	}
	time.Sleep(50 * time.Millisecond)

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.entries) != 1 {
		t.Fatalf("usage entries: got %d want 1", len(usage.entries))
	}
	if usage.entries[0].Endpoint != "images/generations" {
		t.Errorf("endpoint: got %q want 'images/generations'", usage.entries[0].Endpoint)
	}
}

func TestRoutePassthrough_AudioSpeech(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   "binary-audio-data",
		stream: false,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID:         "c1",
			ProviderID: "openai",
			Name:       "test",
			Format:     domain.FormatOpenAI,
			Auth:       domain.AuthBearer,
			IsActive:   true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/tts-1","input":"hello","voice":"alloy"}`)
	res, err := srv.RoutePassthrough(context.Background(), body, extractModelMust(body), "audio/speech", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	if res.Stream {
		t.Error("audio should not be streaming")
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(buf) != "binary-audio-data" {
		t.Errorf("body: got %q want binary-audio-data", string(buf))
	}
	time.Sleep(50 * time.Millisecond)

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.entries) != 1 {
		t.Fatalf("usage entries: got %d want 1", len(usage.entries))
	}
	if usage.entries[0].Endpoint != "audio/speech" {
		t.Errorf("endpoint: got %q want 'audio/speech'", usage.entries[0].Endpoint)
	}
}

func TestParseOpenAIRequest_Multipart(t *testing.T) {
	// Minimal multipart body with a "model" field.
	mp := []byte("--boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nopenai/whisper-1\r\n--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"audio.mp3\"\r\nContent-Type: audio/mpeg\r\n\r\n\x00\x00\x00\r\n--boundary--\r\n")
	model, err := extractModel(mp)
	if err != nil {
		t.Fatalf("parse multipart: unexpected error: %v", err)
	}
	if model != "openai/whisper-1" {
		t.Errorf("model: got %q want 'openai/whisper-1'", model)
	}
}

func TestRoutePassthrough_AudioTranscriptions_Multipart(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"text":"hello world"}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID:         "c1",
			ProviderID: "openai",
			Name:       "test",
			Format:     domain.FormatOpenAI,
			Auth:       domain.AuthBearer,
			IsActive:   true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	// Multipart body — the model field is in the form data, not JSON.
	mp := []byte("--boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nopenai/whisper-1\r\n--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.mp3\"\r\n\r\n\x00\x00\x00\r\n--boundary--\r\n")
	res, err := srv.RoutePassthrough(context.Background(), mp, extractModelMust(mp), "audio/transcriptions", "", "multipart/form-data; boundary=boundary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Contains(buf, []byte("hello world")) {
		t.Error("body should contain transcribed text")
	}
	time.Sleep(50 * time.Millisecond)

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.entries) != 1 {
		t.Fatalf("usage entries: got %d want 1", len(usage.entries))
	}
	if usage.entries[0].Endpoint != "audio/transcriptions" {
		t.Errorf("endpoint: got %q want 'audio/transcriptions'", usage.entries[0].Endpoint)
	}
}

// --- Health-tracker tests ---

// twoProviderConnRepo builds a connection repo with one active connection for
// each of two providers ("openai" and "anthropic"), both OpenAI-format so the
// passthrough mock translator works.
func twoProviderConnRepo() *mockConnectionRepo {
	return &mockConnectionRepo{
		conns: []domain.Connection{
			{
				ID:         "c-openai",
				ProviderID: "openai",
				Name:       "primary",
				Format:     domain.FormatOpenAI,
				Auth:       domain.AuthBearer,
				IsActive:   true,
			},
			{
				ID:         "c-anthropic",
				ProviderID: "anthropic",
				Name:       "primary",
				Format:     domain.FormatOpenAI,
				Auth:       domain.AuthBearer,
				IsActive:   true,
			},
		},
	}
}

// calledSnapshot returns a copy of m.called under the mutex.
func calledSnapshot(m *mockExecutor) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.called))
	copy(out, m.called)
	return out
}

// TestRouteCombo_OrderedFallback_SkipUnhealthyAndProbe verifies the full
// lifecycle: a failing model A is marked unhealthy, skipped on the next
// request while a background probe is launched; once the probe succeeds A
// is restored and the following request returns to it (ordered_fallback
// always iterates from index 0).
func TestRouteCombo_OrderedFallback_SkipUnhealthyAndProbe(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4": 500, // A is broken at first
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "mycombo",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)
	body := []byte(`{"model":"mycombo","messages":[{"role":"user","content":"hi"}]}`)

	// Request 1: A fails (500) -> marked unhealthy, B used.
	res1, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err != nil {
		t.Fatalf("req1: unexpected error: %v", err)
	}
	if res1.StatusCode != 200 {
		t.Fatalf("req1: status got %d want 200", res1.StatusCode)
	}
	res1.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "claude-3"}) {
		t.Fatalf("req1: called = %v, want [gpt-4 claude-3]", got)
	}
	if !srv.Health.IsUnhealthy("mycombo", "openai/gpt-4") {
		t.Fatalf("req1: A should be unhealthy after failing")
	}
	if srv.Health.IsUnhealthy("mycombo", "anthropic/claude-3") {
		t.Fatalf("req1: B should still be healthy")
	}

	// Now let A recover: remove it from failModels so the upcoming probe
	// succeeds.
	exec.mu.Lock()
	delete(exec.failModels, "gpt-4")
	exec.mu.Unlock()

	// Snapshot call count before req2 so we can isolate req2's calls.
	preReq2Calls := len(calledSnapshot(exec))

	// Request 2: A is unhealthy -> skipped (probe launched in background), B used.
	res2, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err != nil {
		t.Fatalf("req2: unexpected error: %v", err)
	}
	res2.Body.Close()
	// Inspect only the calls made by req2 (and any probe that has already
	// fired). Because we removed gpt-4 from failModels, A would succeed if it
	// were tried inline; if req2 used A we'd see gpt-4 first. Seeing claude-3
	// in req2's calls proves B was used, which proves A was skipped (since
	// ordered_fallback always tries A first).
	req2Calls := calledSnapshot(exec)[preReq2Calls:]
	if !contains(req2Calls, "claude-3") {
		t.Fatalf("req2: should have called claude-3 (B used because A is unhealthy), got %v", req2Calls)
	}

	// Wait for the background probe to run and restore A.
	probeDone := waitForCondition(500*time.Millisecond, func() bool {
		return !srv.Health.IsUnhealthy("mycombo", "openai/gpt-4")
	})
	if !probeDone {
		t.Fatalf("probe did not restore A within timeout")
	}
	// At this point the probe has called Execute("gpt-4") at least once.
	if !contains(calledSnapshot(exec), "gpt-4") {
		t.Fatalf("probe should have called gpt-4")
	}

	// Request 3: A is healthy again -> ordered_fallback starts from index 0,
	// so A is used.
	res3, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err != nil {
		t.Fatalf("req3: unexpected error: %v", err)
	}
	res3.Body.Close()
	calls3 := calledSnapshot(exec)
	if calls3[len(calls3)-1] != "gpt-4" {
		t.Fatalf("req3: last call should be gpt-4 (sticky), got %v", calls3)
	}
}

// TestRouteCombo_OrderedFallback_LastResort verifies that when every model in
// a combo is already unhealthy, the first pass skips them all and a last
// resort pass retries them inline so the request can still succeed (and
// recover the working model).
func TestRouteCombo_OrderedFallback_LastResort(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4": 500, // A stays broken; B will succeed
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "lrcombo",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	// Pre-seed both models as unhealthy so the first pass skips them entirely.
	srv.Health.MarkUnhealthy("lrcombo", "openai/gpt-4")
	srv.Health.MarkUnhealthy("lrcombo", "anthropic/claude-3")

	body := []byte(`{"model":"lrcombo","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err != nil {
		t.Fatalf("expected last-resort success, got error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	res.Body.Close()
	// Give probes a moment to settle.
	time.Sleep(150 * time.Millisecond)

	// B should be healthy now (last-resort succeeded for it).
	if srv.Health.IsUnhealthy("lrcombo", "anthropic/claude-3") {
		t.Fatalf("B should have been marked healthy by last-resort success")
	}
}

// TestRouteCombo_RoundRobin_SkipUnhealthy verifies that a round-robin combo
// skips an unhealthy model and serves the request from a healthy one.
func TestRouteCombo_RoundRobin_SkipUnhealthy(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "rrcombo",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "round-robin",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	// Pre-seed A unhealthy.
	srv.Health.MarkUnhealthy("rrcombo", "openai/gpt-4")

	body := []byte(`{"model":"rrcombo","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	res.Body.Close()

	// Snapshot synchronous calls before the probe runs.
	syncCalls := calledSnapshot(exec)
	if !containsOnly(t, syncCalls, "claude-3") {
		t.Fatalf("round-robin should skip unhealthy A and call only B (synchronously), got %v", syncCalls)
	}
}

// TestRouteCombo_AllUnhealthy_AllFail verifies that if every model is
// unhealthy and the last-resort pass also fails, the request fails with
// ErrAllModelsFailed.
func TestRouteCombo_AllUnhealthy_AllFail(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4":     500,
			"claude-3": 500,
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "failcombo",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	// Pre-seed both unhealthy.
	srv.Health.MarkUnhealthy("failcombo", "openai/gpt-4")
	srv.Health.MarkUnhealthy("failcombo", "anthropic/claude-3")

	body := []byte(`{"model":"failcombo","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "")
	if err == nil {
		t.Fatalf("expected ErrAllModelsFailed, got nil")
	}
}

// --- helpers ---

// extractModelMust returns the model field from body, panicking on error.
// Used in tests to avoid repeating error checks.
func extractModelMust(body []byte) string {
	m, err := extractModel(body)
	if err != nil {
		panic(err)
	}
	return m
}

// equalSeq is a shallow sequence equality check used in asserts.
func equalSeq(t *testing.T, got, want []string) bool {
	t.Helper()
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// containsOnly reports whether every entry in got is equal to `m`.
func containsOnly(t *testing.T, got []string, m string) bool {
	t.Helper()
	for _, g := range got {
		if g != m {
			return false
		}
	}
	return len(got) > 0
}

// contains reports whether `s` is present in the slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// waitForCondition polls pred every 10ms up to timeout. Returns true if pred
// ever returned true, false otherwise.
func waitForCondition(timeout time.Duration, pred func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return pred()
}

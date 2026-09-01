package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/responsecache"
)

// mockExecutor implements domain.Executor for testing. By default it returns
// status/body for every call; if failModels is populated, the model (from
// req.UpstreamModel) is looked up and failModels[model] overrides the
// default status. `called` records the sequence of UpstreamModel values per
// Execute call, in order, so tests can assert which models were attempted.
type mockExecutor struct {
	mu          sync.Mutex
	calls       int
	status      int
	body        string
	stream      bool
	headers     http.Header
	bodies      map[string]string // model -> response body (overrides body)
	failModels  map[string]int    // model -> HTTP status (overrides default)
	failConns   map[string]int    // connection ID -> HTTP status (overrides failModels)
	failFirst   map[string]int    // model -> how many initial calls return 503 before succeeding
	called      []string          // sequence of UpstreamModel per Execute call
	calledConns []string          // sequence of Connection.ID per non-probe Execute call
	sentBodies  []string          // request bodies sent to the upstream (non-probe)
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
	if m.failConns != nil && req.Connection != nil {
		if s, ok := m.failConns[req.Connection.ID]; ok {
			status = s
		}
	}
	if m.failFirst != nil {
		if n, ok := m.failFirst[model]; ok && n > 0 {
			m.failFirst[model] = n - 1
			status = http.StatusServiceUnavailable
		}
	}
	// Don't record probe calls in `called` — they're background health
	// checks, not real request routing decisions.
	if !IsProbeCall(ctx) {
		m.called = append(m.called, model)
		if req.Connection != nil {
			m.calledConns = append(m.calledConns, req.Connection.ID)
		}
		// Capture the request body for injection assertions.
		if b, rerr := io.ReadAll(req.Body); rerr == nil {
			m.sentBodies = append(m.sentBodies, string(b))
			req.Body = io.NopCloser(bytes.NewReader(b))
		}
	}
	m.mu.Unlock()
	hdr := m.headers
	if hdr == nil {
		hdr = http.Header{}
		hdr.Set("Content-Type", "application/json")
	}
	return &domain.ExecuteResult{
		StatusCode: status,
		Headers:    hdr,
		Body:       m.bodyFor(req),
		Stream:     req.Stream || m.stream,
	}, nil
}

// bodyFor returns an SSE-formatted body when the request is streaming so
// callers that parse SSE (like measureModelTPSStreaming) work in tests.
// For non-streaming, the raw body is returned as-is.
func (m *mockExecutor) bodyFor(req domain.ExecuteRequest) io.ReadCloser {
	raw := []byte(m.body)
	if m.bodies != nil {
		if b, ok := m.bodies[req.UpstreamModel]; ok {
			raw = []byte(b)
		}
	}
	if !req.Stream {
		return io.NopCloser(bytes.NewReader(raw))
	}
	// Parse the JSON body to extract content + usage, then re-wrap as
	// SSE streaming events with delta.content (the format callers expect).
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		// Can't parse — fall back to wrapping raw as a single event.
		return io.NopCloser(bytes.NewReader([]byte("data: " + m.body + "\n\ndata: [DONE]\n\n")))
	}
	var sse strings.Builder
	sse.WriteString("data: " + fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, parsed.Choices[0].Message.Content) + "\n\n")
	if parsed.Usage != nil {
		sse.WriteString("data: " + fmt.Sprintf(`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d}}`, parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens) + "\n\n")
	}
	sse.WriteString("data: [DONE]\n\n")
	return io.NopCloser(strings.NewReader(sse.String()))
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
func (r *mockComboRepo) Delete(ctx context.Context, id string) error       { return nil }

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
func (r *mockConnectionRepo) Create(ctx context.Context, c *domain.Connection) error { return nil }
func (r *mockConnectionRepo) Update(ctx context.Context, c *domain.Connection) error { return nil }
func (r *mockConnectionRepo) Delete(ctx context.Context, id string) error            { return nil }
func (r *mockConnectionRepo) Reorder(ctx context.Context, orderedIDs []string) error { return nil }
func (r *mockConnectionRepo) SetRateLimited(ctx context.Context, id string, until time.Time) error {
	return nil
}

// mockUsageRepo implements domain.UsageRepo for testing.
type mockUsageRepo struct {
	mu      sync.Mutex
	entries []domain.UsageEntry
}

func (r *mockUsageRepo) Record(ctx context.Context, e *domain.UsageEntry) error {
	r.mu.Lock()
	r.entries = append(r.entries, *e)
	r.mu.Unlock()
	return nil
}
func (r *mockUsageRepo) Stats(ctx context.Context, q domain.UsageStatsQuery) (*domain.UsageStats, error) {
	return &domain.UsageStats{}, nil
}
func (r *mockUsageRepo) History(ctx context.Context, q domain.HistoryQuery) (*domain.HistoryResult, error) {
	return &domain.HistoryResult{Data: r.entries, Total: len(r.entries)}, nil
}
func (r *mockUsageRepo) DistinctHistoryFilters(ctx context.Context, search string) (*domain.HistoryFilters, error) {
	return &domain.HistoryFilters{}, nil
}
func (r *mockUsageRepo) ModelStats(ctx context.Context) (map[string]*domain.ModelStat, error) {
	return map[string]*domain.ModelStat{}, nil
}
func (r *mockUsageRepo) ModelStatsByID(ctx context.Context) (map[string]*domain.ModelStat, error) {
	return map[string]*domain.ModelStat{}, nil
}
func (r *mockUsageRepo) SavingsStats(ctx context.Context, period string, apiKey string) (*domain.SavingsAgg, error) {
	return &domain.SavingsAgg{}, nil
}
func (r *mockUsageRepo) SumCostByApiKeyID(ctx context.Context, apiKeyID string, since time.Time) (float64, error) {
	return 0, nil
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

			IsActive: true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	modelStr, _ := extractModel(body)
	res, err := srv.RouteChat(context.Background(), body, modelStr, false, "test-key", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	// Read and close the body to trigger usage recording
	buf, _ := io.ReadAll(res.Body)
	_, _ = io.Copy(io.Discard, res.Body)
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
	if e.ApiKeyID != "test-key" {
		t.Errorf("api key id: got %q want 'test-key'", e.ApiKeyID)
	}
}

func TestRouteCombo_EmptyCompletionLength_Fallbacks(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		bodies: map[string]string{
			"gpt-4": `{"id":"1","choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":150,"completion_tokens":30,"total_tokens":180}}`,
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "emptyfb",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"emptyfb","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	buf, _ := io.ReadAll(res.Body)
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if string(buf) != `{"id":"1","choices":[{"message":{"content":"ok"}}]}` {
		t.Errorf("body = %s, want second model content", buf)
	}
	// gpt-4's blank completion counts as a soft failure; claude-3 succeeds.
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "claude-3"}) {
		t.Fatalf("called = %v, want [gpt-4 claude-3]", got)
	}
	// The blank attempt still records the tokens the upstream consumed.
	time.Sleep(50 * time.Millisecond)
	usage.mu.Lock()
	defer usage.mu.Unlock()
	failed := usage.entries[0]
	if failed.PromptTokens != 150 || failed.CompletionTokens != 30 {
		t.Errorf("failed entry tokens: prompt=%d completion=%d, want 150/30", failed.PromptTokens, failed.CompletionTokens)
	}
	if failed.Error == "" {
		t.Error("failed entry should carry the blank-completion error")
	}
}

func TestRouteCombo_AllModelsBlankCompletion_ReturnsUpstreamError(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":""},"finish_reason":"length"}]}`,
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "allblank",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"allblank","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	var ue *domain.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v; want *domain.UpstreamError", err)
	}
	if ue.Status != 0 {
		t.Fatalf("UpstreamError.Status = %d, want 0", ue.Status)
	}
	if !strings.Contains(ue.Message, "exhausted tokens") {
		t.Errorf("UpstreamError.Message = %q, want mention of exhausted tokens", ue.Message)
	}
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "claude-3"}) {
		t.Fatalf("called = %v, want [gpt-4 claude-3]", got)
	}
	time.Sleep(50 * time.Millisecond)
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

			IsActive: true,
		}},
	}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"mycombo","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	time.Sleep(50 * time.Millisecond)
}

func TestRouteSingle_ModelNotFound(t *testing.T) {
	srv := NewRouterService(&mockComboRepo{}, &mockConnectionRepo{}, &mockExecutor{}, &mockTranslator{}, &mockUsageRepo{})
	body := []byte(`{"model":"nonexistent","messages":[]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
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

			IsActive: true,
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
	_, _ = io.Copy(io.Discard, res.Body)
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

			IsActive: true,
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
	_, _ = io.Copy(io.Discard, res.Body)
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

			IsActive: true,
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
	_, _ = io.Copy(io.Discard, res.Body)
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

			IsActive: true,
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
	_, _ = io.Copy(io.Discard, res.Body)
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

				IsActive: true,
			},
			{
				ID:         "c-anthropic",
				ProviderID: "anthropic",
				Name:       "primary",

				IsActive: true,
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

// calledConnsSnapshot returns a copy of m.calledConns under the mutex.
func calledConnsSnapshot(m *mockExecutor) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.calledConns))
	copy(out, m.calledConns)
	return out
}

// fourKeyConnRepo builds a connection repo with two active connections per
// provider: the first key of each provider is the "unhealthy" one, the second
// the "healthy" one, in the ordering used by two-phase tests.
func fourKeyConnRepo() *mockConnectionRepo {
	return &mockConnectionRepo{
		conns: []domain.Connection{
			{ID: "c-openai-1", ProviderID: "openai", Name: "k1", IsActive: true},
			{ID: "c-openai-2", ProviderID: "openai", Name: "k2", IsActive: true},
			{ID: "c-anthropic-1", ProviderID: "anthropic", Name: "k1", IsActive: true},
			{ID: "c-anthropic-2", ProviderID: "anthropic", Name: "k2", IsActive: true},
		},
	}
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
			"gpt-4": 404, // A is broken at first
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
	res1, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("req1: unexpected error: %v", err)
	}
	if res1.StatusCode != 200 {
		t.Fatalf("req1: status got %d want 200", res1.StatusCode)
	}
	res1.Body.Close()
	// gpt-4 was healthy at request start, so its 404 is retried (3 attempts)
	// before falling through to claude-3.
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "gpt-4", "gpt-4", "claude-3"}) {
		t.Fatalf("req1: called = %v, want [gpt-4 gpt-4 gpt-4 claude-3]", got)
	}
	if !srv.Health.IsUnhealthy("openai/gpt-4", "c-openai") {
		t.Fatalf("req1: A should be unhealthy after failing")
	}
	if srv.Health.IsUnhealthy("anthropic/claude-3", "c-anthropic") {
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
	res2, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("req2: unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res2.Body)
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
		return !srv.Health.IsUnhealthy("openai/gpt-4", "c-openai")
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
	res3, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("req3: unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res3.Body)
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
			"gpt-4": 404, // A stays broken; B will succeed
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
	srv.Health.MarkUnhealthy("openai/gpt-4", "c-openai")
	srv.Health.MarkUnhealthy("anthropic/claude-3", "c-anthropic")

	body := []byte(`{"model":"lrcombo","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("expected last-resort success, got error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	// Give probes a moment to settle.
	time.Sleep(150 * time.Millisecond)

	// B should be healthy now (last-resort succeeded for it).
	if srv.Health.IsUnhealthy("anthropic/claude-3", "c-anthropic") {
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
	srv.Health.MarkUnhealthy("openai/gpt-4", "c-openai")

	body := []byte(`{"model":"rrcombo","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
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
			"gpt-4":    500,
			"claude-3": 404,
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
	srv.Health.MarkUnhealthy("openai/gpt-4", "c-openai")
	srv.Health.MarkUnhealthy("anthropic/claude-3", "c-anthropic")

	body := []byte(`{"model":"failcombo","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err == nil {
		t.Fatalf("expected ErrAllModelsFailed, got nil")
	}
}

// TestRouteCombo_TwoPhase_KeyOrder verifies the two-phase key routing: when
// every key fails, the healthy keys are tried first in model order and the
// keys that were already unhealthy at request start are only retried in the
// second pass, in the same model order.
func TestRouteCombo_TwoPhase_KeyOrder(t *testing.T) {
	exec := &mockExecutor{
		status: 404,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "twophase",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, fourKeyConnRepo(), exec, &mockTranslator{}, usage)

	// Pre-seed the first key of each model as unhealthy.
	srv.Health.MarkUnhealthy("openai/gpt-4", "c-openai-1")
	srv.Health.MarkUnhealthy("anthropic/claude-3", "c-anthropic-1")

	body := []byte(`{"model":"twophase","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err == nil {
		t.Fatalf("expected ErrAllModelsFailed, got nil")
	}

	// Healthy keys (phase 1) retry their 404 three times each before giving
	// up; the previously-unhealthy keys get a single last-resort attempt.
	want := []string{"c-openai-2", "c-openai-2", "c-openai-2", "c-anthropic-2", "c-anthropic-2", "c-anthropic-2", "c-openai-1", "c-anthropic-1"}
	if got := calledConnsSnapshot(exec); !equalSeq(t, got, want) {
		t.Fatalf("phase order: got %v, want %v", got, want)
	}
	wantModels := []string{"gpt-4", "gpt-4", "gpt-4", "claude-3", "claude-3", "claude-3", "gpt-4", "claude-3"}
	if got := calledSnapshot(exec); !equalSeq(t, got, wantModels) {
		t.Fatalf("phase model order: got %v, want %v", got, wantModels)
	}
}

// TestRouteCombo_TwoPhase_HealthySucceeds_SkipsUnhealthy verifies that keys
// which were unhealthy at request start are not retried when a healthy key
// succeeds — each key is tried at most once per request.
func TestRouteCombo_TwoPhase_HealthySucceeds_SkipsUnhealthy(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4": 404, // healthy key of A still fails
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "twophase2",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, fourKeyConnRepo(), exec, &mockTranslator{}, usage)

	// Pre-seed the first key of each model as unhealthy.
	srv.Health.MarkUnhealthy("openai/gpt-4", "c-openai-1")
	srv.Health.MarkUnhealthy("anthropic/claude-3", "c-anthropic-1")

	body := []byte(`{"model":"twophase2","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	// A's healthy key fails (404, retried 3× since healthy), B's healthy key
	// succeeds: the previously-unhealthy keys must NOT be retried.
	want := []string{"c-openai-2", "c-openai-2", "c-openai-2", "c-anthropic-2"}
	if got := calledConnsSnapshot(exec); !equalSeq(t, got, want) {
		t.Fatalf("healthy-only calls: got %v, want %v", got, want)
	}
}

// TestRouteCombo_Transient503_RetriesThenSucceeds verifies that a transient
// 503 from the upstream is retried on the same connection and the request
// succeeds once the upstream recovers, without falling back to another model.
func TestRouteCombo_Transient503_RetriesThenSucceeds(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failFirst: map[string]int{
			"gpt-4": 2, // first two calls return 503, third succeeds
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "retrycombo",
				Models:   []string{"openai/gpt-4"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"retrycombo","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "gpt-4", "gpt-4"}) {
		t.Fatalf("called = %v, want [gpt-4 gpt-4 gpt-4]", got)
	}
	if srv.Health.IsUnhealthy("openai/gpt-4", "c-openai") {
		t.Fatalf("model should remain healthy after transient failure + success")
	}
}

// TestRouteCombo_Transient503_RetriesExhausted_FallsBack verifies that when
// the retries are exhausted the router falls back to the next model.
func TestRouteCombo_Transient503_RetriesExhausted_FallsBack(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4": 503, // always transient-fails; B will succeed
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "retryfallback",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"retryfallback","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	// 3 attempts on gpt-4 (1 + 2 retries), then fallback to claude-3.
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "gpt-4", "gpt-4", "claude-3"}) {
		t.Fatalf("called = %v, want [gpt-4 gpt-4 gpt-4 claude-3]", got)
	}
	if !srv.Health.IsUnhealthy("openai/gpt-4", "c-openai") {
		t.Fatalf("gpt-4 should be marked unhealthy after exhausting retries")
	}
}

// TestRouteCombo_Permanent404_NoRetry verifies that permanent errors (4xx)
// skip the retry loop and fall back to the next model immediately.
// TestRouteCombo_404RetriesWhenHealthyThenFallsBack verifies that a 404 on a
// connection that was healthy at request start is retried (health, not the
// error class, drives the retry) before falling through to the next model.
func TestRouteCombo_404RetriesWhenHealthyThenFallsBack(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4": http.StatusNotFound,
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "retry404",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"retry404","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	// 404 is not a deterministic client error (only 400/422/415 are), so a
	// healthy connection retries it 3× before falling through to claude-3.
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "gpt-4", "gpt-4", "claude-3"}) {
		t.Fatalf("called = %v, want [gpt-4 gpt-4 gpt-4 claude-3]", got)
	}
}

// TestRouteSingle_RetriesWhenHealthy_AnyError verifies the retry decision is
// driven by health, not the error class: a healthy connection retries even a
// non-transient status (401 here) before falling through to the next account.
func TestRouteSingle_RetriesWhenHealthy_AnyError(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failConns: map[string]int{
			"c1": http.StatusUnauthorized,
		},
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{
			{ID: "c1", ProviderID: "openai", Name: "bad", IsActive: true},
			{ID: "c2", ProviderID: "openai", Name: "good", IsActive: true},
		},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	// c1 was healthy at request start → its 401 is retried (3 attempts)
	// despite not being a "transient" status, before c2 succeeds.
	if got := calledConnsSnapshot(exec); !equalSeq(t, got, []string{"c1", "c1", "c1", "c2"}) {
		t.Fatalf("called conns = %v, want [c1 c1 c1 c2]", got)
	}
}

// TestRouteSingle_UnhealthyNoRetry verifies the flip side: a connection that
// was already unhealthy when the request started gets a single last-resort
// attempt, even for a retryable-looking 503.
func TestRouteSingle_UnhealthyNoRetry(t *testing.T) {
	exec := &mockExecutor{
		status: http.StatusServiceUnavailable,
		body:   `{"error":{"message":"unavailable"}}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)
	srv.Health.MarkUnhealthy("openai/gpt-4", "c1")

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err == nil {
		t.Fatal("expected ErrNoConnection after last-resort attempt fails")
	}
	// Phase 2 (last resort) → single attempt, no retries despite the 503.
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4"}) {
		t.Fatalf("called = %v, want [gpt-4] (no retry when unhealthy)", got)
	}
}

// TestRouteSingle_AllFailed_ReturnsLastStatus verifies that when every
// connection fails, the client sees the last real upstream status (429 here)
// instead of a generic 503 — mirroring LiteLLM's re-raise of the last typed
// exception.
func TestRouteSingle_AllFailed_ReturnsLastStatus(t *testing.T) {
	exec := &mockExecutor{
		status: http.StatusTooManyRequests,
		body:   `{"error":{"message":"rate limited","type":"rate_limit_error"}}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	var ue *domain.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v; want *domain.UpstreamError", err)
	}
	if ue.Status != http.StatusTooManyRequests {
		t.Fatalf("UpstreamError.Status = %d, want 429", ue.Status)
	}
	if ue.Message != "rate limited" {
		t.Fatalf("UpstreamError.Message = %q, want %q", ue.Message, "rate limited")
	}
}

// TestRouteCombo_AllFailed_ReturnsLastStatus verifies that when every model in
// a combo fails, the client sees the LAST model's real status.
func TestRouteCombo_AllFailed_ReturnsLastStatus(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4":    http.StatusInternalServerError,
			"claude-3": http.StatusTooManyRequests,
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "allfail",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"allfail","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	var ue *domain.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v; want *domain.UpstreamError", err)
	}
	// claude-3 was the last model tried → its 429 wins.
	if ue.Status != http.StatusTooManyRequests {
		t.Fatalf("UpstreamError.Status = %d, want 429 (last model's status)", ue.Status)
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

// mockTPSUsageRepo returns custom ModelStatsByID for TPS tests.
type mockTPSUsageRepo struct {
	mockUsageRepo
	stats map[string]*domain.ModelStat
}

func (r *mockTPSUsageRepo) ModelStatsByID(ctx context.Context) (map[string]*domain.ModelStat, error) {
	return r.stats, nil
}

func TestRouteCombo_VelocityStrategy(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	// Model A (openai/gpt-4) TPS: 20, Model B (anthropic/claude-3) TPS: 80
	usage := &mockTPSUsageRepo{
		stats: map[string]*domain.ModelStat{
			"openai/gpt-4":       {AvgTPS: 20.0},
			"anthropic/claude-3": {AvgTPS: 80.0},
		},
	}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "velcombo",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "velocity",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)
	srv.TPS = NewTPSCache(usage, time.Minute)

	body := []byte(`{"model":"velcombo","messages":[{"role":"user","content":"fast please"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	syncCalls := calledSnapshot(exec)
	// anthropic/claude-3 (TPS 80) should be called before openai/gpt-4 (TPS 20)
	if len(syncCalls) == 0 || syncCalls[0] != "claude-3" {
		t.Fatalf("expected faster model 'claude-3' first, got calls: %v", syncCalls)
	}
}

func TestRouteCombo_IntelligenceStrategy(t *testing.T) {
	// Mock executor that responds with a number (1-based index) the classifier picks.
	// Models: 1=openai/gpt-4, 2=anthropic/claude-3
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"2"}}]}`,
	}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:              "cb1",
				Name:            "intelcombo",
				Models:          []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy:        "intelligence",
				ClassifierModel: "openai/gpt-4",
				ModelMeta: map[string]domain.ComboModelMeta{
					"openai/gpt-4":       {Description: "Robust model for complex tasks"},
					"anthropic/claude-3": {Description: "Lightweight model for simple tasks"},
				},
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, &mockUsageRepo{})

	// Test 1: Classifier returns "2" -> should route to claude-3 (index 2)
	body := []byte(`{"model":"intelcombo","messages":[{"role":"user","content":"hello"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	calls := calledSnapshot(exec)
	if len(calls) < 2 {
		t.Fatalf("expected classifier call + target call, got: %v", calls)
	}
	targetCall := calls[len(calls)-1]
	if targetCall != "claude-3" {
		t.Fatalf("expected classifier to pick 'claude-3' (choice 2), got: %s", targetCall)
	}

	// Test 2: Classifier returns "1" -> should route to gpt-4 (index 1)
	exec.body = `{"id":"1","choices":[{"message":{"content":"1"}}]} `
	res2, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res2.Body)
	res2.Body.Close()

	calls2 := calledSnapshot(exec)
	targetCall2 := calls2[len(calls2)-1]
	if targetCall2 != "gpt-4" {
		t.Fatalf("expected classifier to pick 'gpt-4' (choice 1), got: %s", targetCall2)
	}
}

// TestTPSCache_ProbePriority verifies that a fresh probe result is preferred
// over usage data, and that stale probe data falls back to usage data.
func TestTPSCache_ProbePriority(t *testing.T) {
	usage := &mockTPSUsageRepo{
		stats: map[string]*domain.ModelStat{
			"openai/gpt-4": {AvgTPS: 15.0},
		},
	}
	cache := NewTPSCache(usage, time.Minute)

	// No probe yet: should return usage data.
	if got := cache.Get("openai/gpt-4"); got != 15.0 {
		t.Fatalf("expected usage TPS 15.0, got %f", got)
	}

	// Set a fresh probe: should be preferred.
	cache.SetProbe("openai/gpt-4", 40.0)
	if got := cache.Get("openai/gpt-4"); got != 40.0 {
		t.Fatalf("expected probe TPS 40.0, got %f", got)
	}

	// NeedsProbe should be false for a model with usage data.
	if cache.NeedsProbe("openai/gpt-4") {
		t.Fatal("model with usage data should not need probing")
	}

	// A model with no data should need probing.
	if !cache.NeedsProbe("anthropic/claude-3") {
		t.Fatal("model without data should need probing")
	}
}

// TestTPSCache_NeedsProbe_Stale verifies that stale probe data triggers a
// re-probe when there is no usage data.
func TestTPSCache_NeedsProbe_Stale(t *testing.T) {
	usage := &mockTPSUsageRepo{stats: map[string]*domain.ModelStat{}}
	cache := NewTPSCache(usage, time.Minute)

	// Set a probe, then artificially age it.
	cache.SetProbe("openai/gpt-4", 30.0)
	if cache.NeedsProbe("openai/gpt-4") {
		t.Fatal("fresh probe should not need re-probing")
	}

	// Force the probe to be stale by manipulating the internal map.
	cache.mu.Lock()
	p := cache.probes["openai/gpt-4"]
	p.measuredAt = time.Now().Add(-2 * time.Hour)
	cache.probes["openai/gpt-4"] = p
	cache.mu.Unlock()

	if !cache.NeedsProbe("openai/gpt-4") {
		t.Fatal("stale probe with no usage data should need re-probing")
	}
}

// TestRouteCombo_VelocityStrategy_TriggersProbe verifies that the velocity
// strategy triggers a background TPS probe for models with no data.
func TestRouteCombo_VelocityStrategy_TriggersProbe(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":50}}`,
	}
	// No usage data for either model.
	usage := &mockTPSUsageRepo{stats: map[string]*domain.ModelStat{}}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "velprobe",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "velocity",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)
	srv.TPS = NewTPSCache(usage, time.Minute)
	srv.TPSProber = NewTPSProber(srv.TPS, srv)

	body := []byte(`{"model":"velprobe","messages":[{"role":"user","content":"test"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	// Wait for background probes to complete.
	waitForCondition(2*time.Second, func() bool {
		return srv.TPS.Get("openai/gpt-4") > 0 && srv.TPS.Get("anthropic/claude-3") > 0
	})

	if got := srv.TPS.Get("openai/gpt-4"); got <= 0 {
		t.Fatalf("expected probe to measure TPS for openai/gpt-4, got %f", got)
	}
	if got := srv.TPS.Get("anthropic/claude-3"); got <= 0 {
		t.Fatalf("expected probe to measure TPS for anthropic/claude-3, got %f", got)
	}
}

// TestReorderChosenFirst verifies that the chosen model is moved to the front
// and the remaining models keep their relative order as fallback.
func TestReorderChosenFirst(t *testing.T) {
	ordered := reorderChosenFirst(
		[]string{"a/model-a", "b/model-b", "c/model-c", "d/model-d"},
		"c/model-c",
	)
	expected := []string{"c/model-c", "a/model-a", "b/model-b", "d/model-d"}
	if !equalSeq(t, ordered, expected) {
		t.Fatalf("reorderChosenFirst: got %v, want %v", ordered, expected)
	}
}

// TestRouteCombo_NestedCombo verifies that a combo can have another combo as
// a member; the nested combo's strategy is applied transparently.
func TestRouteCombo_NestedCombo(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	// Outer combo uses ordered_fallback with nested "inner" combo + a model.
	// Inner combo uses ordered_fallback with one model. We expect the
	// nested combo to be expanded and the inner model to be called.
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"outer": {
				ID:       "outer",
				Name:     "outer",
				Models:   []string{"inner", "openai/gpt-4"},
				Strategy: "ordered_fallback",
			},
			"inner": {
				ID:       "inner",
				Name:     "inner",
				Models:   []string{"anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, &mockUsageRepo{})

	body := []byte(`{"model":"outer","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()

	calls := calledSnapshot(exec)
	// Should expand "inner" -> anthropic/claude-3 first; gpt-4 is the outer fallback.
	if len(calls) == 0 || calls[0] != "claude-3" {
		t.Fatalf("expected nested combo to resolve to claude-3 first, got %v", calls)
	}
}

// TestRouteCombo_NestedCombo_DepthLimit verifies the safety net: even if a
// cycle somehow reaches the runtime (e.g. manually edited DB), the depth
// limit prevents unbounded recursion.
func TestRouteCombo_NestedCombo_DepthLimit(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	// Both combos point only at provider/model — no real cycle, but the
	// test ensures the depth limit doesn't kill legitimate deep paths.
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"outer": {
				ID:       "outer",
				Name:     "outer",
				Models:   []string{"mid"},
				Strategy: "ordered_fallback",
			},
			"mid": {
				ID:       "mid",
				Name:     "mid",
				Models:   []string{"inner"},
				Strategy: "ordered_fallback",
			},
			"inner": {
				ID:       "inner",
				Name:     "inner",
				Models:   []string{"openai/gpt-4"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, &mockUsageRepo{})

	body := []byte(`{"model":"outer","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
}

// TestComboService_DetectCycle verifies save-time cycle detection (A→B→A).
func TestComboService_DetectCycle(t *testing.T) {
	// Existing combos: B has A.
	repo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cbA": {ID: "cbA", Name: "A", Models: []string{"openai/gpt-4"}, Strategy: "ordered_fallback"},
			"cbB": {ID: "cbB", Name: "B", Models: []string{"A"}, Strategy: "ordered_fallback"},
		},
	}
	svc := &ComboService{Repo: repo}

	// Trying to update A with B in its models should fail (A → B → A).
	a := &domain.Combo{Name: "A", Models: []string{"B"}, Strategy: "ordered_fallback"}
	err := svc.Update(context.Background(), a)
	if err == nil {
		t.Fatal("expected cycle error updating A with B as member")
	}
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

// TestComboService_DetectCycle_SelfRef catches a direct A→A reference.
func TestComboService_DetectCycle_SelfRef(t *testing.T) {
	repo := &mockComboRepo{combos: map[string]*domain.Combo{}}
	svc := &ComboService{Repo: repo}
	a := &domain.Combo{Name: "A", Models: []string{"A"}, Strategy: "ordered_fallback"}
	err := svc.Update(context.Background(), a)
	if err == nil {
		t.Fatal("expected self-reference error")
	}
}

// TestComboService_DetectCycle_Nested verifies A→B→C→A rejection.
func TestComboService_DetectCycle_Nested(t *testing.T) {
	repo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cbB": {ID: "cbB", Name: "B", Models: []string{"C"}, Strategy: "ordered_fallback"},
			"cbC": {ID: "cbC", Name: "C", Models: []string{"A"}, Strategy: "ordered_fallback"},
		},
	}
	svc := &ComboService{Repo: repo}
	a := &domain.Combo{Name: "A", Models: []string{"B"}, Strategy: "ordered_fallback"}
	err := svc.Update(context.Background(), a)
	if err == nil {
		t.Fatal("expected cycle error for A→B→C→A")
	}
}

// TestComboService_NoCycle_DeepChain verifies a long non-cyclic chain is
// accepted (e.g. A→B→C→D).
func TestComboService_NoCycle_DeepChain(t *testing.T) {
	repo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cbB": {ID: "cbB", Name: "B", Models: []string{"C"}, Strategy: "ordered_fallback"},
			"cbC": {ID: "cbC", Name: "C", Models: []string{"D"}, Strategy: "ordered_fallback"},
		},
	}
	svc := &ComboService{Repo: repo}
	a := &domain.Combo{Name: "A", Models: []string{"B"}, Strategy: "ordered_fallback"}
	err := svc.Update(context.Background(), a)
	if err != nil {
		t.Fatalf("expected no cycle, got %v", err)
	}
}

// TestRouteSingle_Transient503_RetriesThenSucceeds verifies that the
// single-model path (no combo) retries transient upstream failures on the
// same connection instead of failing the request immediately.
func TestRouteSingle_Transient503_RetriesThenSucceeds(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failFirst: map[string]int{
			"gpt-4": 2, // first two calls 503, third succeeds
		},
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4", "gpt-4", "gpt-4"}) {
		t.Fatalf("called = %v, want [gpt-4 gpt-4 gpt-4]", got)
	}
}

// TestRouteSingle_Client400_NoRetry verifies that a deterministic client
// error (400) on the single-model path is returned to the client without a
// transient retry and without burning other connections.
func TestRouteSingle_Client400_NoRetry(t *testing.T) {
	exec := &mockExecutor{
		status: http.StatusBadRequest,
		body:   `{"error":{"message":"bad request","type":"invalid_request_error"}}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	// Exactly one upstream call: no transient retry, no fallback to a
	// second connection.
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4"}) {
		t.Fatalf("called = %v, want [gpt-4] (no retry on 400)", got)
	}
}

// TestRouteCombo_Client400_NoFallback verifies that a deterministic client
// error (400) on the first combo member does NOT fall through to the next
// model — it is returned to the client immediately.
func TestRouteCombo_Client400_NoFallback(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4": http.StatusBadRequest,
		},
	}
	usage := &mockUsageRepo{}
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:       "cb1",
				Name:     "cb400",
				Models:   []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy: "ordered_fallback",
			},
		},
	}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"cb400","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4"}) {
		t.Fatalf("called = %v, want [gpt-4] (no fallback on 400)", got)
	}
}

// TestRouteSingle_SkipsRateLimitedConnection verifies that a connection still
// in its rate-limit pause (RateLimitedUntil in the future) is skipped by the
// router, and a healthy connection is preferred instead.
func TestRouteSingle_SkipsRateLimitedConnection(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{
			{ID: "c-limited", ProviderID: "openai", Name: "rl", IsActive: true, RateLimitedUntil: time.Now().Add(10 * time.Minute)},
			{ID: "c-healthy", ProviderID: "openai", Name: "ok", IsActive: true},
		},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledConnsSnapshot(exec); !equalSeq(t, got, []string{"c-healthy"}) {
		t.Fatalf("called conns = %v, want [c-healthy]", got)
	}
}

// TestRouteSingle_AllConnectionsRateLimited verifies that when every
// connection is in its rate-limit pause the request fails fast instead of
// hammering the upstream.
func TestRouteSingle_AllConnectionsRateLimited(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{
			{ID: "c1", ProviderID: "openai", Name: "rl1", IsActive: true, RateLimitedUntil: time.Now().Add(10 * time.Minute)},
			{ID: "c2", ProviderID: "openai", Name: "rl2", IsActive: true, RateLimitedUntil: time.Now().Add(10 * time.Minute)},
		},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err == nil {
		t.Fatal("expected error when all connections are rate-limited")
	}
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("expected no upstream calls, got %v", got)
	}
}

// TestRouteSingle_ExpiredRateLimit_ConnectionUsed verifies that a connection
// whose rate-limit pause has elapsed is usable again.
func TestRouteSingle_ExpiredRateLimit_ConnectionUsed(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
	}
	usage := &mockUsageRepo{}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{
			{ID: "c1", ProviderID: "openai", Name: "rl", IsActive: true, RateLimitedUntil: time.Now().Add(-time.Minute)},
		},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, usage)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledConnsSnapshot(exec); !equalSeq(t, got, []string{"c1"}) {
		t.Fatalf("called conns = %v, want [c1]", got)
	}
}

// --- Hook pipeline integration ---

// singleConnService builds a RouterService with one active openai connection.
func singleConnService(exec *mockExecutor) *RouterService {
	return NewRouterService(
		&mockComboRepo{},
		&mockConnectionRepo{conns: []domain.Connection{{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true}}},
		exec,
		&mockTranslator{},
		&mockUsageRepo{},
	)
}

// TestRouteSingle_PreCallRejects verifies the admission gate: a PreCallHook
// rejection aborts the request before any upstream call.
func TestRouteSingle_PreCallRejects(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	srv := singleConnService(exec)
	p := &HookPipeline{}
	p.Register(&stubPreCall{err: &domain.HookRejectError{Status: http.StatusForbidden, Message: "blocked"}})
	srv.Hooks = p

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	var hre *domain.HookRejectError
	if !errors.As(err, &hre) || hre.Status != http.StatusForbidden {
		t.Fatalf("err = %v; want HookRejectError 403", err)
	}
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("expected no upstream calls, got %v", got)
	}
}

// TestRouteSingle_PreCallModifiesModel verifies a PreCallHook can rewrite the
// model and the router honors the new value.
func TestRouteSingle_PreCallModifiesModel(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	srv := singleConnService(exec)
	p := &HookPipeline{}
	p.Register(hookFunc(func(_ context.Context, hc *domain.HookContext) error {
		hc.Model = "openai/gpt-4o"
		return nil
	}))
	srv.Hooks = p

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4o"}) {
		t.Fatalf("called = %v; want [gpt-4o] (model rewritten by pre-call hook)", got)
	}
}

// TestRouteSingle_PostCallModifiesBody verifies a PostCallHook can rewrite a
// non-stream response body before it reaches the client.
func TestRouteSingle_PostCallModifiesBody(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"original"}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`,
	}
	srv := singleConnService(exec)
	p := &HookPipeline{}
	p.Register(&modifyBodyPostCall{})
	srv.Hooks = p

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(res.Body)
	if !bytes.Contains(buf, []byte("modified")) {
		t.Fatalf("body = %s; want post-call modified response", buf)
	}
}

// TestRouteSingle_PostCallFailureTransformsError verifies a failure hook can
// replace the error surfaced to the client.
func TestRouteSingle_PostCallFailureTransformsError(t *testing.T) {
	// No connections -> routing fails; the failure hook replaces the error.
	srv := NewRouterService(&mockComboRepo{}, &mockConnectionRepo{}, &mockExecutor{}, &mockTranslator{}, &mockUsageRepo{})
	p := &HookPipeline{}
	p.Register(&stubPostCallFailure{err: errors.New("transformed")})
	srv.Hooks = p

	body := []byte(`{"model":"openai/gpt-4","messages":[]}`)
	_, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err == nil || err.Error() != "transformed" {
		t.Fatalf("err = %v; want transformed error", err)
	}
}

// TestRouteSingle_NoHooks_IsNilSafe verifies the zero-cost path: with Hooks nil
// the request routes normally.
func TestRouteSingle_NoHooks_IsNilSafe(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	srv := singleConnService(exec)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
}

// TestRouteChat_SkipsCacheForLongHistory verifies the conversation-history
// guard: requests over the threshold bypass the cache entirely, so the same
// request hits upstream again.
func TestRouteChat_SkipsCacheForLongHistory(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := singleConnService(exec)
	mem := responsecache.NewMemory(10, time.Minute, time.Minute)
	defer mem.Close()
	srv.Cache = NewCacheService(mem)
	srv.MaxCacheHistory = 2

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"1"},{"role":"assistant","content":"a"},{"role":"user","content":"2"}]}`)
	for i := 0; i < 2; i++ {
		res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if got := calledSnapshot(exec); len(got) != 2 {
		t.Fatalf("3-message conversation (over threshold 2) should bypass cache; upstream calls = %v", got)
	}
}

// TestRouteChat_CacheHitWithinHistory verifies the flip side: a request within
// the history threshold is cached, so the second identical request is served
// from cache without an upstream call.
func TestRouteChat_CacheHitWithinHistory(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := singleConnService(exec)
	mem := responsecache.NewMemory(10, time.Minute, time.Minute)
	defer mem.Close()
	srv.Cache = NewCacheService(mem)
	srv.MaxCacheHistory = 2

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"1"},{"role":"user","content":"2"}]}`)
	for i := 0; i < 2; i++ {
		res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if got := calledSnapshot(exec); len(got) != 1 {
		t.Fatalf("2-message conversation (within threshold) should hit cache on the 2nd request; upstream calls = %v", got)
	}
}

// TestRouteSingle_Attempts verifies a first-try success reports 1 attempt.
func TestRouteSingle_Attempts(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	srv := singleConnService(exec)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1 (first try)", res.Attempts)
	}
}

// TestRouteCombo_FallbackAttempts verifies the attempt counter reflects
// fallbacks at the connection/model level: gpt-4 fails (with internal retries)
// then claude-3 succeeds → 2 connection attempts, i.e. 1 fallback.
func TestRouteCombo_FallbackAttempts(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}]}`,
		failModels: map[string]int{
			"gpt-4": http.StatusNotFound,
		},
	}
	comboRepo := &mockComboRepo{combos: map[string]*domain.Combo{
		"cb1": {ID: "cb1", Name: "attempts", Models: []string{"openai/gpt-4", "anthropic/claude-3"}, Strategy: "ordered_fallback"},
	}}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, &mockUsageRepo{})

	body := []byte(`{"model":"attempts","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2 (gpt-4 connection + claude-3 fallback)", res.Attempts)
	}
}

// TestRouteCombo_SkipsContextTooSmall verifies the context-window filter: a
// prompt that exceeds a member's context window skips that member and goes
// straight to the next one.
func TestRouteCombo_SkipsContextTooSmall(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	comboRepo := &mockComboRepo{combos: map[string]*domain.Combo{
		"cb1": {ID: "cb1", Name: "ctxcombo", Models: []string{"openai/gpt-4", "anthropic/claude-3"}, Strategy: "ordered_fallback"},
	}}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, &mockUsageRepo{})
	srv.Pricing = &PricingCache{contexts: map[string]int{
		"openai/gpt-4":       8000,
		"anthropic/claude-3": 200000,
	}}

	// ~10000 estimated tokens (40000 chars / 4) → exceeds gpt-4's 8k window.
	body := []byte(`{"model":"ctxcombo","messages":[{"role":"user","content":"` + strings.Repeat("x", 40000) + `"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"claude-3"}) {
		t.Fatalf("called = %v, want [claude-3] (gpt-4 skipped for context window)", got)
	}
}

// TestRouteCombo_ContextUnknown_FailOpen verifies that without context data
// nothing is filtered and the configured order is tried.
func TestRouteCombo_ContextUnknown_FailOpen(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	comboRepo := &mockComboRepo{combos: map[string]*domain.Combo{
		"cb1": {ID: "cb1", Name: "noctx", Models: []string{"openai/gpt-4", "anthropic/claude-3"}, Strategy: "ordered_fallback"},
	}}
	srv := NewRouterService(comboRepo, twoProviderConnRepo(), exec, &mockTranslator{}, &mockUsageRepo{})

	body := []byte(`{"model":"noctx","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4"}) {
		t.Fatalf("called = %v, want [gpt-4] (no context data → no filter)", got)
	}
}

func TestEstimatePromptTokens(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hello world"},{"role":"assistant","content":"hi"}]}`)
	if got := estimatePromptTokens(body); got <= 0 || got > 10 {
		t.Fatalf("estimatePromptTokens = %d, want something in (0,10]", got)
	}
	if got := estimatePromptTokens([]byte(`not json`)); got <= 0 {
		t.Fatalf("estimatePromptTokens(invalid) = %d, want fallback > 0", got)
	}
}

// TestRouteChat_KeyModelForbidden verifies a key restricted to specific
// models gets 403 for a model it can't use, with no upstream call.
func TestRouteChat_KeyModelForbidden(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	srv := singleConnService(exec)
	ctx := WithAllowedModels(context.Background(), []string{"openai/gpt-4o"})

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	_, err := srv.RouteChat(ctx, body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v; want ErrForbidden", err)
	}
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("no upstream call expected for a forbidden model, got %v", got)
	}
}

// TestRouteChat_KeyModelAllowed verifies a bare allowed model name matches the
// provider-prefixed request.
func TestRouteChat_KeyModelAllowed(t *testing.T) {
	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	srv := singleConnService(exec)
	ctx := WithAllowedModels(context.Background(), []string{"gpt-4o"})

	body := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(ctx, body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if got := calledSnapshot(exec); !equalSeq(t, got, []string{"gpt-4o"}) {
		t.Fatalf("called = %v, want [gpt-4o]", got)
	}
}

func TestModelAllowed(t *testing.T) {
	srv := NewRouterService(&mockComboRepo{combos: map[string]*domain.Combo{
		"smart": {ID: "s", Name: "smart", Models: []string{"openai/gpt-4o", "anthropic/claude-3"}},
	}}, &mockConnectionRepo{}, &mockExecutor{}, &mockTranslator{}, &mockUsageRepo{})

	ctx := WithAllowedModels(context.Background(), []string{"gpt-4o"})
	if !srv.modelAllowed(ctx, "openai/gpt-4o") {
		t.Fatal("bare 'gpt-4o' should allow 'openai/gpt-4o'")
	}
	if srv.modelAllowed(ctx, "openai/gpt-4") {
		t.Fatal("gpt-4 is not in the allowed list")
	}
	if !srv.modelAllowed(ctx, "smart") {
		t.Fatal("a combo containing an allowed model should be allowed")
	}
	if !srv.modelAllowed(context.Background(), "anything") {
		t.Fatal("an empty allowed list allows everything")
	}
}

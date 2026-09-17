package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/translator"
)

type captureExecutor struct {
	lastBody []byte
	lastURL  string
}

func (c *captureExecutor) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	if req.Body != nil {
		c.lastBody, _ = io.ReadAll(req.Body)
	}
	return &domain.ExecuteResult{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))),
		Stream:     false,
	}, nil
}

type memConnRepo struct{ c domain.Connection }

func (m *memConnRepo) List(ctx context.Context) ([]domain.Connection, error) {
	return []domain.Connection{m.c}, nil
}
func (m *memConnRepo) ListByProvider(ctx context.Context, providerID string) ([]domain.Connection, error) {
	return []domain.Connection{m.c}, nil
}
func (m *memConnRepo) Get(ctx context.Context, id string) (*domain.Connection, error) {
	return &m.c, nil
}
func (m *memConnRepo) Create(ctx context.Context, c *domain.Connection) error { return nil }
func (m *memConnRepo) Update(ctx context.Context, c *domain.Connection) error { return nil }
func (m *memConnRepo) Delete(ctx context.Context, id string) error             { return nil }
func (m *memConnRepo) SetRateLimited(ctx context.Context, id string, until time.Time) error {
	return nil
}
func (m *memConnRepo) Reorder(ctx context.Context, ids []string) error { return nil }

func TestResponsesPathBody(t *testing.T) {
	// Use existing mock patterns from router_test.go if available - keep simple
	// Just test translate + injectStreamUsage path manually
	tr := translator.New()
	body := []byte(`{"model":"coding","input":[{"role":"user","content":"say hi"}],"stream":true}`)
	
	// Step 1: Responses -> OpenAI
	step1, err := tr.TranslateRequest(domain.FormatResponses, domain.FormatOpenAI, "glm-5.2", body)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("step1: %s", step1)
	
	// Step 2: injectStreamUsage
	step2 := injectStreamUsage(step1)
	t.Logf("step2: %s", step2)
	
	// Step 3: rewriteModel
	step3, err := tr.TranslateRequest(domain.FormatOpenAI, domain.FormatOpenAI, "glm-5.2", step2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("step3: %s", step3)
}

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/jhon/gorouter/internal/domain"
)

// ConnectionService is the dashboard use case for managing connections.
type ConnectionService struct {
	Repo domain.ConnectionRepo
}

func (s *ConnectionService) List(ctx context.Context) ([]domain.Connection, error) {
	return s.Repo.List(ctx)
}

func (s *ConnectionService) Create(ctx context.Context, c *domain.Connection) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now()
	}
	if c.Format == "" {
		c.Format = domain.FormatOpenAI
	}
	if c.Auth == "" {
		c.Auth = domain.AuthBearer
	}
	if c.Name == "" {
		return fmtValidation("name is required")
	}
	if c.ProviderID == "" {
		return fmtValidation("provider_id is required")
	}
	return s.Repo.Create(ctx, c)
}

func (s *ConnectionService) Update(ctx context.Context, c *domain.Connection) error {
	return s.Repo.Update(ctx, c)
}

func (s *ConnectionService) Delete(ctx context.Context, id string) error {
	return s.Repo.Delete(ctx, id)
}

func (s *ConnectionService) Reorder(ctx context.Context, ids []string) error {
	return s.Repo.Reorder(ctx, ids)
}

// ComboService is the dashboard use case for managing combos.
type ComboService struct {
	Repo   domain.ComboRepo
	Models domain.ModelRepo // for Kind validation
}

func (s *ComboService) List(ctx context.Context) ([]domain.Combo, error) {
	return s.Repo.List(ctx)
}

func (s *ComboService) Create(ctx context.Context, c *domain.Combo) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now()
	}
	if c.Name == "" {
		return fmtValidation("combo name is required")
	}
	if len(c.Models) == 0 {
		return fmtValidation("combo must have at least one model")
	}
	if err := normalizeStrategy(&c.Strategy); err != nil {
		return err
	}
	kind, err := s.resolveComboKind(ctx, c.Models)
	if err != nil {
		return err
	}
	c.Kind = kind
	return s.Repo.Create(ctx, c)
}

func (s *ComboService) Update(ctx context.Context, c *domain.Combo) error {
	if err := normalizeStrategy(&c.Strategy); err != nil {
		return err
	}
	c.UpdatedAt = time.Now()
	kind, err := s.resolveComboKind(ctx, c.Models)
	if err != nil {
		return err
	}
	c.Kind = kind
	return s.Repo.Update(ctx, c)
}

// resolveComboKind verifies that all models in the combo are the same Kind
// and returns that Kind. If a model is not in the catalog, it's treated as
// KindLLM (the default).
func (s *ComboService) resolveComboKind(ctx context.Context, models []string) (domain.ModelKind, error) {
	if s.Models == nil {
		return domain.KindLLM, nil
	}
	var firstKind domain.ModelKind
	for _, m := range models {
		entry, err := s.Models.Get(ctx, m)
		if err != nil {
			continue // model not in catalog; assume llm
		}
		k := entry.Kind
		if k == "" {
			k = domain.KindLLM
		}
		if firstKind == "" {
			firstKind = k
		} else if k != firstKind {
			return "", fmtValidation(fmt.Sprintf(
				"combo models must be same kind: %q is %s, %q is %s",
				models[0], firstKind, m, k))
		}
	}
	if firstKind == "" {
		firstKind = domain.KindLLM
	}
	return firstKind, nil
}

func normalizeStrategy(s *string) error {
	switch *s {
	case "":
		*s = "ordered_fallback"
	case "ordered_fallback", "round-robin":
	default:
		return fmtValidation(fmt.Sprintf("invalid strategy %q: must be ordered_fallback or round-robin", *s))
	}
	return nil
}

func (s *ComboService) Delete(ctx context.Context, id string) error {
	return s.Repo.Delete(ctx, id)
}

// ApiKeyService is the dashboard use case for managing client API keys.
type ApiKeyService struct {
	Repo   domain.ApiKeyRepo
	Secret string
}

func (s *ApiKeyService) List(ctx context.Context) ([]domain.ApiKey, error) {
	return s.Repo.List(ctx)
}

func (s *ApiKeyService) Create(ctx context.Context, name string, rateLimitRPM int) (*domain.ApiKey, error) {
	key, err := apikeyGenerate(s.Secret)
	if err != nil {
		return nil, err
	}
	k := &domain.ApiKey{
		ID:           uuid.NewString(),
		Key:          key,
		Name:         name,
		IsActive:     true,
		RateLimitRPM: rateLimitRPM,
	}
	if err := s.Repo.Create(ctx, k); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *ApiKeyService) Update(ctx context.Context, k *domain.ApiKey) error {
	return s.Repo.Update(ctx, k)
}

func (s *ApiKeyService) Delete(ctx context.Context, id string) error {
	return s.Repo.Delete(ctx, id)
}

// UsageService is the dashboard use case for usage analytics.
type UsageService struct {
	Repo domain.UsageRepo
}

func (s *UsageService) Stats(ctx context.Context, period string) (*domain.UsageStats, error) {
	return s.Repo.Stats(ctx, period)
}

func (s *UsageService) History(ctx context.Context, limit int) ([]domain.UsageEntry, error) {
	return s.Repo.History(ctx, limit)
}

func fmtValidation(msg string) error {
	return fmt.Errorf("%w: %s", domain.ErrValidation, msg)
}
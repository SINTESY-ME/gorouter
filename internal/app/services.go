package app

import (
	"context"
	"fmt"
	"strings"
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
	if err := s.validateCombo(c); err != nil {
		return err
	}
	if err := s.detectCycle(ctx, c.Name, c.Models); err != nil {
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
	if err := s.validateCombo(c); err != nil {
		return err
	}
	if err := s.detectCycle(ctx, c.Name, c.Models); err != nil {
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

// detectCycle rejects a combo whose member list creates a nesting cycle.
// A member without "/" is treated as a combo name; the function walks the
// dependency tree transitively (BFS) and fails if the combo being saved
// appears anywhere in the subtree — covering A→B→A, A→B→C→A, etc.
// Self-reference (A→A) is also caught.
func (s *ComboService) detectCycle(ctx context.Context, comboName string, models []string) error {
	var queue []string
	seen := map[string]bool{}
	for _, m := range models {
		if m == "" || strings.Contains(m, "/") {
			continue
		}
		if m == comboName {
			return fmtValidation(fmt.Sprintf("combo %q references itself", comboName))
		}
		if !seen[m] {
			seen[m] = true
			queue = append(queue, m)
		}
	}
	visited := map[string]bool{}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true
		nested, err := s.Repo.GetByName(ctx, name)
		if err != nil {
			continue
		}
		for _, m := range nested.Models {
			if m == "" || strings.Contains(m, "/") {
				continue
			}
			if m == comboName {
				return fmtValidation(fmt.Sprintf("combo nesting cycle detected: %s → %s → %s", comboName, name, comboName))
			}
			if !visited[m] && !seen[m] {
				seen[m] = true
				queue = append(queue, m)
			}
		}
	}
	return nil
}

func (s *ComboService) validateCombo(c *domain.Combo) error {
	if err := normalizeStrategy(&c.Strategy); err != nil {
		return err
	}
	if c.Strategy == StrategyIntelligence {
		if strings.TrimSpace(c.ClassifierModel) == "" {
			if len(c.Models) > 0 {
				c.ClassifierModel = c.Models[0]
			} else {
				return fmtValidation("classifier_model is required when strategy is intelligence")
			}
		}
		// Validate that every model in the combo has a non-empty description.
		for _, m := range c.Models {
			desc := strings.TrimSpace(c.ModelMeta[m].Description)
			if desc == "" {
				return fmtValidation(fmt.Sprintf("description is required for model %q when using intelligence strategy", m))
			}
		}
	}
	return nil
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
		*s = StrategyOrderedFallback
	case StrategyOrderedFallback, StrategyRoundRobin, StrategyVelocity, StrategyIntelligence:
	default:
		return fmtValidation(fmt.Sprintf("invalid strategy %q: must be ordered_fallback, round-robin, velocity, or intelligence", *s))
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

func (s *ApiKeyService) Create(ctx context.Context, name string, limits []domain.KeyLimit) (*domain.ApiKey, error) {
	key, err := apikeyGenerate(s.Secret)
	if err != nil {
		return nil, err
	}
	if err := normalizeKeyLimits(limits); err != nil {
		return nil, err
	}
	k := &domain.ApiKey{
		ID:       uuid.NewString(),
		Key:      key,
		Name:     name,
		IsActive: true,
		Limits:   limits,
	}
	if err := s.Repo.Create(ctx, k); err != nil {
		return nil, err
	}
	return k, nil
}

// normalizeKeyLimits validates a key's limits and backfills missing IDs so
// each limit has a stable identity for the rate-limit windows.
func normalizeKeyLimits(limits []domain.KeyLimit) error {
	if len(limits) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for i := range limits {
		l := &limits[i]
		if l.Kind != domain.KeyLimitRate && l.Kind != domain.KeyLimitBudget {
			return fmtValidation(fmt.Sprintf("invalid limit kind %q: must be rate or budget", l.Kind))
		}
		if l.Max <= 0 {
			return fmtValidation("limit max must be greater than zero")
		}
		dur, err := domain.ParseWindowDuration(l.Duration)
		if err != nil || dur <= 0 {
			return fmtValidation(fmt.Sprintf("invalid limit duration %q", l.Duration))
		}
		if l.ID == "" {
			l.ID = uuid.NewString()
		}
		if seen[l.ID] {
			return fmtValidation("duplicate limit id")
		}
		seen[l.ID] = true
	}
	return nil
}

func (s *ApiKeyService) Update(ctx context.Context, k *domain.ApiKey) error {
	if k.Limits != nil {
		if err := normalizeKeyLimits(k.Limits); err != nil {
			return err
		}
	}
	return s.Repo.Update(ctx, k)
}

func (s *ApiKeyService) Delete(ctx context.Context, id string) error {
	return s.Repo.Delete(ctx, id)
}

// UsageService is the dashboard use case for usage analytics.
type UsageService struct {
	Repo domain.UsageRepo
}

func (s *UsageService) Stats(ctx context.Context, q domain.UsageStatsQuery) (*domain.UsageStats, error) {
	return s.Repo.Stats(ctx, q)
}

func (s *UsageService) History(ctx context.Context, q domain.HistoryQuery) (*domain.HistoryResult, error) {
	return s.Repo.History(ctx, q)
}

func (s *UsageService) DistinctHistoryFilters(ctx context.Context, search string) (*domain.HistoryFilters, error) {
	return s.Repo.DistinctHistoryFilters(ctx, search)
}

func fmtValidation(msg string) error {
	return fmt.Errorf("%w: %s", domain.ErrValidation, msg)
}

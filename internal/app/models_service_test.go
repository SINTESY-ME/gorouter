package app

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// fakeModelRepo is a minimal in-memory ModelRepo for ModelsService tests.
type fakeModelRepo struct {
	entries []domain.ModelEntry
}

func (r *fakeModelRepo) List(ctx context.Context) ([]domain.ModelEntry, error) {
	return append([]domain.ModelEntry{}, r.entries...), nil
}

func (r *fakeModelRepo) ListByProvider(ctx context.Context, providerID string) ([]domain.ModelEntry, error) {
	var out []domain.ModelEntry
	for _, e := range r.entries {
		if e.ProviderID == providerID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) ListActive(ctx context.Context) ([]domain.ModelEntry, error) {
	var out []domain.ModelEntry
	for _, e := range r.entries {
		if e.IsActive {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) Get(ctx context.Context, id string) (*domain.ModelEntry, error) {
	for i := range r.entries {
		if r.entries[i].ID == id {
			return &r.entries[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeModelRepo) Upsert(ctx context.Context, m *domain.ModelEntry) error {
	for i := range r.entries {
		if r.entries[i].ID == m.ID {
			r.entries[i] = *m
			return nil
		}
	}
	r.entries = append(r.entries, *m)
	return nil
}

func (r *fakeModelRepo) UpsertBatch(ctx context.Context, entries []*domain.ModelEntry) error {
	for _, m := range entries {
		if err := r.Upsert(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (r *fakeModelRepo) Delete(ctx context.Context, id string) error {
	for i := range r.entries {
		if r.entries[i].ID == id {
			r.entries = append(r.entries[:i], r.entries[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (r *fakeModelRepo) SetActive(ctx context.Context, id string, active bool) error {
	for i := range r.entries {
		if r.entries[i].ID == id {
			r.entries[i].IsActive = active
			return nil
		}
	}
	return domain.ErrNotFound
}

func (r *fakeModelRepo) DeactivateStaleSync(ctx context.Context, providerID string, activeIDs []string) error {
	return nil
}

func (r *fakeModelRepo) ReactivateSync(ctx context.Context, providerID string, activeIDs []string) error {
	return nil
}

// fakeComboRepo is a minimal in-memory ComboRepo.
type fakeComboRepo struct {
	combos []domain.Combo
}

func (r *fakeComboRepo) List(ctx context.Context) ([]domain.Combo, error) {
	return append([]domain.Combo{}, r.combos...), nil
}

func (r *fakeComboRepo) Get(ctx context.Context, id string) (*domain.Combo, error) {
	for i := range r.combos {
		if r.combos[i].ID == id {
			return &r.combos[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeComboRepo) GetByName(ctx context.Context, name string) (*domain.Combo, error) {
	for i := range r.combos {
		if r.combos[i].Name == name {
			return &r.combos[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeComboRepo) Create(ctx context.Context, c *domain.Combo) error {
	r.combos = append(r.combos, *c)
	return nil
}

func (r *fakeComboRepo) Update(ctx context.Context, c *domain.Combo) error {
	for i := range r.combos {
		if r.combos[i].ID == c.ID {
			r.combos[i] = *c
			return nil
		}
	}
	return domain.ErrNotFound
}

func (r *fakeComboRepo) Delete(ctx context.Context, id string) error {
	for i := range r.combos {
		if r.combos[i].ID == id {
			r.combos = append(r.combos[:i], r.combos[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// selectorWithProviders builds a ConnectionSelector pre-refreshed with the
// given provider configs (no catalog → unknown providers fail open).
func selectorWithProviders(t *testing.T, cfgs []domain.ProviderConfig) *ConnectionSelector {
	t.Helper()
	sel := NewConnectionSelector(nil, nil)
	sel.cache = map[string]*domain.ProviderConfig{}
	for i := range cfgs {
		c := cfgs[i]
		sel.cache[c.ID] = &c
	}
	return sel
}

func TestModelsServiceListFiltersDisabledProviders(t *testing.T) {
	modelRepo := &fakeModelRepo{entries: []domain.ModelEntry{
		{ID: "openai/gpt-x", ProviderID: "openai", ModelID: "gpt-x", IsActive: true},
		{ID: "anthropic/claude-x", ProviderID: "anthropic", ModelID: "claude-x", IsActive: true},
		{ID: "openai/gpt-off", ProviderID: "openai", ModelID: "gpt-off", IsActive: false},
	}}
	comboRepo := &fakeComboRepo{combos: []domain.Combo{
		{ID: "c1", Name: "smart", Models: []string{"openai/gpt-x", "anthropic/claude-x"}, Strategy: StrategyOrderedFallback},
		{ID: "c2", Name: "dead-combo", Models: []string{"openai/gpt-off"}, Strategy: StrategyOrderedFallback},
	}}
	sel := selectorWithProviders(t, []domain.ProviderConfig{
		{ID: "openai", IsActive: false},
		{ID: "anthropic", IsActive: true},
	})
	svc := &ModelsService{Combos: comboRepo, Models: modelRepo, Selector: sel}

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := []string{}
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	want := []string{"anthropic/claude-x", "dead-combo", "smart"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("got %v, want %v (openai models must vanish when provider disabled; combos always listed)", ids, want)
	}
}

func TestModelsServiceListNilSelectorKeepsAllModels(t *testing.T) {
	modelRepo := &fakeModelRepo{entries: []domain.ModelEntry{
		{ID: "openai/gpt-x", ProviderID: "openai", ModelID: "gpt-x", IsActive: true},
	}}
	svc := &ModelsService{Combos: &fakeComboRepo{}, Models: modelRepo, Selector: nil}

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "openai/gpt-x" {
		t.Fatalf("nil selector must keep all models, got %+v", got)
	}
}

func TestModelsServiceListUnknownProviderFailOpen(t *testing.T) {
	modelRepo := &fakeModelRepo{entries: []domain.ModelEntry{
		{ID: "ghost/model", ProviderID: "ghost", ModelID: "model", IsActive: true},
	}}
	sel := selectorWithProviders(t, nil) // empty cache: unknown provider
	svc := &ModelsService{Combos: &fakeComboRepo{}, Models: modelRepo, Selector: sel}

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ghost/model" {
		t.Fatalf("unknown provider must fail open, got %+v", got)
	}
}

// A combos-only key must see a catalog of combos: advertising raw model ids
// it cannot call would make the list a lie (the gateway answers 403 for them).
func TestModelsServiceListCombosOnly(t *testing.T) {
	modelRepo := &fakeModelRepo{entries: []domain.ModelEntry{
		{ID: "openai/gpt-x", ProviderID: "openai", ModelID: "gpt-x", IsActive: true},
	}}
	comboRepo := &fakeComboRepo{combos: []domain.Combo{
		{ID: "c1", Name: "smart", Models: []string{"openai/gpt-x"}, Strategy: StrategyOrderedFallback},
	}}
	svc := &ModelsService{Combos: comboRepo, Models: modelRepo, Selector: nil}

	all, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("without the flag both entries must show, got %+v", all)
	}

	got, err := svc.List(WithCombosOnly(context.Background()))
	if err != nil {
		t.Fatalf("List(combos-only): %v", err)
	}
	if len(got) != 1 || got[0].ID != "smart" || got[0].OwnedBy != "combo" {
		t.Fatalf("combos-only key must see only combos, got %+v", got)
	}
}
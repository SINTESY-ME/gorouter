package app

import (
	"context"
	"errors"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// fakeApiKeyRepo is an in-memory domain.ApiKeyRepo. Only the methods a test
// needs are meaningful; the rest keep the interface satisfiable without
// dragging a database into the unit tests.
type fakeApiKeyRepo struct {
	keys map[string]*domain.ApiKey
}

func newFakeApiKeyRepo(keys ...*domain.ApiKey) *fakeApiKeyRepo {
	r := &fakeApiKeyRepo{keys: map[string]*domain.ApiKey{}}
	for _, k := range keys {
		r.keys[k.ID] = k
	}
	return r
}

func (r *fakeApiKeyRepo) List(context.Context) ([]domain.ApiKey, error) {
	out := make([]domain.ApiKey, 0, len(r.keys))
	for _, k := range r.keys {
		out = append(out, *k)
	}
	return out, nil
}

func (r *fakeApiKeyRepo) Create(_ context.Context, k *domain.ApiKey) error {
	r.keys[k.ID] = k
	return nil
}

func (r *fakeApiKeyRepo) Update(_ context.Context, k *domain.ApiKey) error {
	stored, ok := r.keys[k.ID]
	if !ok {
		return errors.New("not found")
	}
	stored.Name = k.Name
	stored.IsActive = k.IsActive
	stored.Limits = k.Limits
	stored.AllowedModels = k.AllowedModels
	return nil
}

func (r *fakeApiKeyRepo) Delete(_ context.Context, id string) error {
	delete(r.keys, id)
	return nil
}

func (r *fakeApiKeyRepo) Validate(context.Context, string) (bool, error) { return true, nil }

func (r *fakeApiKeyRepo) GetByKey(_ context.Context, key string) (*domain.ApiKey, error) {
	for _, k := range r.keys {
		if k.KeyHash == apikeyHashKey(key) {
			return k, nil
		}
	}
	return nil, nil
}

func (r *fakeApiKeyRepo) Get(_ context.Context, id string) (*domain.ApiKey, error) {
	k, ok := r.keys[id]
	if !ok {
		return nil, nil
	}
	return k, nil
}

func (r *fakeApiKeyRepo) UpdateSecret(_ context.Context, id, keyHash, keyCipher string) error {
	k, ok := r.keys[id]
	if !ok {
		return errors.New("not found")
	}
	k.KeyHash = keyHash
	k.KeyCipher = keyCipher
	return nil
}

// A key created now can be revealed later: the copy button in the dashboard
// depends on the plaintext being recoverable, which it never was before.
func TestApiKeyServiceCreateSealsForReveal(t *testing.T) {
	repo := newFakeApiKeyRepo()
	svc := &ApiKeyService{Repo: repo, Secret: "instance-secret"}
	ctx := context.Background()

	created, err := svc.Create(ctx, "opencodex", nil, []string{"openai/gpt-4o", "my-combo"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Key == "" {
		t.Fatal("create returned no plaintext")
	}
	if created.KeyCipher == "" {
		t.Fatal("create stored no sealed copy: the key would not be revealable")
	}

	revealed, err := svc.Reveal(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revealed != created.Key {
		t.Fatalf("reveal returned %q, want the created key", revealed)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Revealable {
		t.Fatalf("list did not mark the key revealable: %+v", list)
	}
}

// A key stored before the cipher existed has no plaintext to give back: the
// dashboard must be told to rotate instead of being handed a fragment.
func TestApiKeyServiceRevealRejectsLegacyKey(t *testing.T) {
	legacy := &domain.ApiKey{ID: "key-legacy", Name: "legacy", KeyHash: apikeyHashKey("sk-old"), IsActive: true}
	svc := &ApiKeyService{Repo: newFakeApiKeyRepo(legacy), Secret: "instance-secret"}

	if _, err := svc.Reveal(context.Background(), legacy.ID); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("revealing a legacy key returned %v, want a validation error", err)
	}
}

// Rotation is the escape hatch for those keys: it issues a new value, and the
// record must come back revealable with the new plaintext.
func TestApiKeyServiceRotateReplacesStoredSecret(t *testing.T) {
	legacy := &domain.ApiKey{ID: "key-legacy", Name: "legacy", KeyHash: apikeyHashKey("sk-old"), IsActive: true}
	repo := newFakeApiKeyRepo(legacy)
	svc := &ApiKeyService{Repo: repo, Secret: "instance-secret"}
	ctx := context.Background()

	rotated, err := svc.Rotate(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Key == "" || rotated.Key == "sk-old" {
		t.Fatalf("rotate returned %q, want a fresh key", rotated.Key)
	}
	if !rotated.Revealable {
		t.Fatal("rotated key is not revealable")
	}
	if got := repo.keys[legacy.ID].KeyHash; got != apikeyHashKey(rotated.Key) {
		t.Fatal("stored hash still matches the old key: the old key would keep working")
	}
	revealed, err := svc.Reveal(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revealed != rotated.Key {
		t.Fatalf("reveal after rotate returned %q, want %q", revealed, rotated.Key)
	}
}

// Editing a key must persist what the form sent: limits, allowed models and
// the model/combo access list are the whole point of the edit dialog.
func TestApiKeyServiceUpdatePersistsLimitsAndModels(t *testing.T) {
	existing := &domain.ApiKey{ID: "key-1", Name: "before", KeyHash: apikeyHashKey("sk-x"), IsActive: true}
	repo := newFakeApiKeyRepo(existing)
	svc := &ApiKeyService{Repo: repo, Secret: "instance-secret"}

	updated := &domain.ApiKey{
		ID:            "key-1",
		Name:          "after",
		IsActive:      false,
		Limits:        []domain.KeyLimit{{ID: "l1", Kind: domain.KeyLimitRate, Max: 25, Duration: "1d"}},
		AllowedModels: []string{"openai/gpt-4o", "sintesy-combo"},
	}
	if err := svc.Update(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	stored := repo.keys["key-1"]
	if stored.Name != "after" || stored.IsActive {
		t.Fatalf("name/active not persisted: %+v", stored)
	}
	if len(stored.Limits) != 1 || stored.Limits[0].Max != 25 {
		t.Fatalf("limits not persisted: %+v", stored.Limits)
	}
	if len(stored.AllowedModels) != 2 || stored.AllowedModels[1] != "sintesy-combo" {
		t.Fatalf("allowed models not persisted: %+v", stored.AllowedModels)
	}
}

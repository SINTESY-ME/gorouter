package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/apikey"
	"gorm.io/gorm"
)

// ApiKeyRepo implements domain.ApiKeyRepo via GORM. Keys are stored as
// SHA-256 hashes in the "key" column (see domain.ApiKey.KeyHash) for lookup
// and revocation, plus the plaintext sealed with AES-GCM in "key_cipher" so
// the dashboard can show the key again. The plaintext itself is never stored.
type ApiKeyRepo struct{ db *gorm.DB }

func NewApiKeyRepo(db *gorm.DB) *ApiKeyRepo { return &ApiKeyRepo{db: db} }

func (r *ApiKeyRepo) List(ctx context.Context) ([]domain.ApiKey, error) {
	var keys []domain.ApiKey
	// Members only see their own keys (no access grants for keys).
	scope := domain.UserScopeFrom(ctx)
	tx := r.db.WithContext(ctx)
	if scope != nil && scope.Role != domain.RoleAdmin {
		tx = tx.Where("created_by = ?", scope.UserID)
	}
	err := tx.Order("created_at DESC").Find(&keys).Error
	return keys, err
}

func (r *ApiKeyRepo) Create(ctx context.Context, k *domain.ApiKey) error {
	if k.KeyHash == "" {
		return fmt.Errorf("key hash is required")
	}
	err := r.db.WithContext(ctx).Create(k).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: api key collision (very unlikely); retry", domain.ErrAlreadyExists)
	}
	return err
}

// Update persists the mutable fields of a key. Select() is explicit on
// purpose: with plain struct Updates, GORM skips zero values, so clearing a
// limit set or an allowed-model list silently did nothing — and limits and
// allowed_models were missing from the update entirely.
func (r *ApiKeyRepo) Update(ctx context.Context, k *domain.ApiKey) error {
	res := r.db.WithContext(ctx).Model(&domain.ApiKey{}).Where("id = ?", k.ID).
		Select("name", "is_active", "limits", "allowed_models").
		Updates(k)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: api key not found", domain.ErrNotFound)
	}
	return nil
}

// UpdateSecret swaps a key's hash and its sealed plaintext in a single write.
func (r *ApiKeyRepo) UpdateSecret(ctx context.Context, id, keyHash, keyCipher string) error {
	res := r.db.WithContext(ctx).Model(&domain.ApiKey{}).Where("id = ?", id).
		Updates(map[string]any{"key": keyHash, "key_cipher": keyCipher})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: api key not found", domain.ErrNotFound)
	}
	return nil
}

// Get returns a key by ID, nil when it does not exist.
func (r *ApiKeyRepo) Get(ctx context.Context, id string) (*domain.ApiKey, error) {
	var k domain.ApiKey
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&k).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *ApiKeyRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&domain.ApiKey{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: api key not found", domain.ErrNotFound)
	}
	return nil
}

func (r *ApiKeyRepo) Validate(ctx context.Context, key string) (bool, error) {
	var k domain.ApiKey
	err := r.db.WithContext(ctx).Select("is_active").Where("key = ?", apikey.HashKey(key)).First(&k).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return k.IsActive, nil
}

func (r *ApiKeyRepo) GetByKey(ctx context.Context, key string) (*domain.ApiKey, error) {
	var k domain.ApiKey
	err := r.db.WithContext(ctx).Where("key = ?", apikey.HashKey(key)).First(&k).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

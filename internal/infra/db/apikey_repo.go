package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// ApiKeyRepo implements domain.ApiKeyRepo via GORM.
type ApiKeyRepo struct{ db *gorm.DB }

func NewApiKeyRepo(db *gorm.DB) *ApiKeyRepo { return &ApiKeyRepo{db: db} }

func (r *ApiKeyRepo) List(ctx context.Context) ([]domain.ApiKey, error) {
	var keys []domain.ApiKey
	err := r.db.WithContext(ctx).Order("created_at DESC").Find(&keys).Error
	return keys, err
}

func (r *ApiKeyRepo) Create(ctx context.Context, k *domain.ApiKey) error {
	err := r.db.WithContext(ctx).Create(k).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: api key collision (very unlikely); retry", domain.ErrAlreadyExists)
	}
	return err
}

func (r *ApiKeyRepo) Update(ctx context.Context, k *domain.ApiKey) error {
	res := r.db.WithContext(ctx).Model(&domain.ApiKey{}).Where("id = ?", k.ID).
		Updates(map[string]any{"name": k.Name, "is_active": k.IsActive})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: api key not found", domain.ErrNotFound)
	}
	return nil
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
	err := r.db.WithContext(ctx).Select("is_active").Where("key = ?", key).First(&k).Error
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
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&k).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}
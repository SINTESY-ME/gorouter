package db

import (
	"context"
	"errors"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// ProviderConfigRepo implements domain.ProviderConfigRepo via GORM.
type ProviderConfigRepo struct{ db *gorm.DB }

func NewProviderConfigRepo(db *gorm.DB) *ProviderConfigRepo {
	return &ProviderConfigRepo{db: db}
}

func (r *ProviderConfigRepo) List(ctx context.Context) ([]domain.ProviderConfig, error) {
	var ps []domain.ProviderConfig
	err := r.db.WithContext(ctx).Order("id").Find(&ps).Error
	return ps, err
}

func (r *ProviderConfigRepo) Get(ctx context.Context, id string) (*domain.ProviderConfig, error) {
	var p domain.ProviderConfig
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *ProviderConfigRepo) GetByProviderID(ctx context.Context, providerID string) (*domain.ProviderConfig, error) {
	var p domain.ProviderConfig
	err := r.db.WithContext(ctx).Where("id = ?", providerID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *ProviderConfigRepo) Create(ctx context.Context, p *domain.ProviderConfig) error {
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *ProviderConfigRepo) Update(ctx context.Context, p *domain.ProviderConfig) error {
	return r.db.WithContext(ctx).Save(p).Error
}

func (r *ProviderConfigRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&domain.ProviderConfig{}, "id = ?", id).Error
}
package db

import (
	"context"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// SettingRepo implements domain.SettingRepo via GORM.
type SettingRepo struct{ db *gorm.DB }

func NewSettingRepo(db *gorm.DB) *SettingRepo { return &SettingRepo{db: db} }

func (r *SettingRepo) Get(ctx context.Context, key string) (string, error) {
	var s domain.Setting
	// Find (not First) so a missing row returns no result without logging
	// "record not found" as a GORM warning on the normal first-run path.
	if err := r.db.WithContext(ctx).Where("key = ?", key).Limit(1).Find(&s).Error; err != nil {
		return "", err
	}
	return s.Value, nil
}

func (r *SettingRepo) Set(ctx context.Context, key, value string) error {
	s := domain.Setting{Key: key, Value: value, UpdatedAt: time.Now()}
	return r.db.WithContext(ctx).Save(&s).Error
}

func (r *SettingRepo) Has(ctx context.Context, key string) (bool, error) {
	v, err := r.Get(ctx, key)
	if err != nil {
		return false, err
	}
	return v != "", nil
}
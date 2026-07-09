package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// ConnectionRepo implements domain.ConnectionRepo via GORM (SQLite or
// Postgres).
type ConnectionRepo struct{ db *gorm.DB }

func NewConnectionRepo(db *gorm.DB) *ConnectionRepo { return &ConnectionRepo{db: db} }

func (r *ConnectionRepo) List(ctx context.Context) ([]domain.Connection, error) {
	var conns []domain.Connection
	err := r.db.WithContext(ctx).Order("provider_id, priority, created_at").Find(&conns).Error
	return conns, err
}

func (r *ConnectionRepo) ListByProvider(ctx context.Context, providerID string) ([]domain.Connection, error) {
	var conns []domain.Connection
	err := r.db.WithContext(ctx).Where("provider_id = ?", providerID).Order("priority, created_at").Find(&conns).Error
	return conns, err
}

func (r *ConnectionRepo) Get(ctx context.Context, id string) (*domain.Connection, error) {
	var c domain.Connection
	err := r.db.WithContext(ctx).First(&c, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ConnectionRepo) Create(ctx context.Context, c *domain.Connection) error {
	err := r.db.WithContext(ctx).Create(c).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: connection name %q already exists for this provider", domain.ErrAlreadyExists, c.Name)
	}
	return err
}

func (r *ConnectionRepo) Update(ctx context.Context, c *domain.Connection) error {
	res := r.db.WithContext(ctx).Model(&domain.Connection{}).Where("id = ?", c.ID).Updates(map[string]any{
		"name":               c.Name,
		"api_key":            c.APIKey,
		"base_url":           c.BaseURL,
		"format":             c.Format,
		"auth":               c.Auth,
		"priority":           c.Priority,
		"is_active":          c.IsActive,
		"rate_limited_until": c.RateLimitedUntil,
		"updated_at":         time.Now(),
	})
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: connection name %q already exists for this provider", domain.ErrAlreadyExists, c.Name)
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: connection not found", domain.ErrNotFound)
	}
	return nil
}

func (r *ConnectionRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&domain.Connection{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: connection not found", domain.ErrNotFound)
	}
	return nil
}

func (r *ConnectionRepo) Reorder(ctx context.Context, orderedIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for i, id := range orderedIDs {
			if err := tx.Model(&domain.Connection{}).Where("id = ?", id).
				Updates(map[string]any{"priority": i, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *ConnectionRepo) SetRateLimited(ctx context.Context, id string, until interface{}) error {
	t, _ := until.(time.Time)
	res := r.db.WithContext(ctx).Model(&domain.Connection{}).Where("id = ?", id).
		Updates(map[string]any{"rate_limited_until": t, "updated_at": time.Now()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: connection not found", domain.ErrNotFound)
	}
	return nil
}
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// ModelRepo implements domain.ModelRepo via GORM.
type ModelRepo struct{ db *gorm.DB }

func NewModelRepo(db *gorm.DB) *ModelRepo { return &ModelRepo{db: db} }

func (r *ModelRepo) List(ctx context.Context) ([]domain.ModelEntry, error) {
	var out []domain.ModelEntry
	err := r.db.WithContext(ctx).Order("provider_id, model_id").Find(&out).Error
	return out, err
}

func (r *ModelRepo) ListByProvider(ctx context.Context, providerID string) ([]domain.ModelEntry, error) {
	var out []domain.ModelEntry
	err := r.db.WithContext(ctx).Where("provider_id = ?", providerID).Order("model_id").Find(&out).Error
	return out, err
}

func (r *ModelRepo) ListActive(ctx context.Context) ([]domain.ModelEntry, error) {
	var out []domain.ModelEntry
	err := r.db.WithContext(ctx).Where("is_active = true").Order("provider_id, model_id").Find(&out).Error
	return out, err
}

func (r *ModelRepo) Get(ctx context.Context, id string) (*domain.ModelEntry, error) {
	var m domain.ModelEntry
	err := r.db.WithContext(ctx).First(&m, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *ModelRepo) Upsert(ctx context.Context, m *domain.ModelEntry) error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Save(m).Error
}

func (r *ModelRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&domain.ModelEntry{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: model not found", domain.ErrNotFound)
	}
	return nil
}

func (r *ModelRepo) SetActive(ctx context.Context, id string, active bool) error {
	res := r.db.WithContext(ctx).Model(&domain.ModelEntry{}).Where("id = ?", id).
		Updates(map[string]any{"is_active": active, "updated_at": time.Now()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: model not found", domain.ErrNotFound)
	}
	return nil
}

func (r *ModelRepo) DeactivateStaleSync(ctx context.Context, providerID string, activeIDs []string) error {
	q := r.db.WithContext(ctx).Model(&domain.ModelEntry{}).
		Where("provider_id = ? AND source = 'sync'", providerID)
	if len(activeIDs) > 0 {
		q = q.Where("id NOT IN ?", activeIDs)
	}
	return q.Updates(map[string]any{"is_active": false, "updated_at": time.Now()}).Error
}
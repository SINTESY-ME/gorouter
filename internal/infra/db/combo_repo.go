package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// ComboRepo implements domain.ComboRepo via GORM.
type ComboRepo struct{ db *gorm.DB }

func NewComboRepo(db *gorm.DB) *ComboRepo { return &ComboRepo{db: db} }

func (r *ComboRepo) List(ctx context.Context) ([]domain.Combo, error) {
	var combos []domain.Combo
	err := r.db.WithContext(ctx).Order("name").Find(&combos).Error
	return combos, err
}

func (r *ComboRepo) Get(ctx context.Context, id string) (*domain.Combo, error) {
	var c domain.Combo
	err := r.db.WithContext(ctx).First(&c, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ComboRepo) GetByName(ctx context.Context, name string) (*domain.Combo, error) {
	var c domain.Combo
	err := r.db.WithContext(ctx).First(&c, "name = ?", name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ComboRepo) Create(ctx context.Context, c *domain.Combo) error {
	err := r.db.WithContext(ctx).Create(c).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: combo name %q already exists", domain.ErrAlreadyExists, c.Name)
	}
	return err
}

func (r *ComboRepo) Update(ctx context.Context, c *domain.Combo) error {
	c.UpdatedAt = time.Now()
	res := r.db.WithContext(ctx).Model(&domain.Combo{}).Where("id = ?", c.ID).
		Select("Name", "Models", "Strategy", "UpdatedAt").
		Updates(c)
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: combo name %q already exists", domain.ErrAlreadyExists, c.Name)
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: combo not found", domain.ErrNotFound)
	}
	return nil
}

func (r *ComboRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&domain.Combo{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: combo not found", domain.ErrNotFound)
	}
	return nil
}
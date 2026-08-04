package db

import (
	"context"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// UserAccessRepo implements domain.UserAccessRepo via GORM.
type UserAccessRepo struct{ db *gorm.DB }

func NewUserAccessRepo(db *gorm.DB) *UserAccessRepo { return &UserAccessRepo{db: db} }

func (r *UserAccessRepo) List(ctx context.Context, kind domain.UserAccessKind, userID string) ([]string, error) {
	var rows []domain.UserAccess
	err := r.db.WithContext(ctx).
		Where("kind = ? AND user_id = ?", kind, userID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ResourceID)
	}
	return out, nil
}

func (r *UserAccessRepo) Set(ctx context.Context, kind domain.UserAccessKind, userID string, resourceIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("kind = ? AND user_id = ?", kind, userID).Delete(&domain.UserAccess{}).Error; err != nil {
			return err
		}
		for _, rid := range resourceIDs {
			if rid == "" {
				continue
			}
			row := domain.UserAccess{
				Kind:       kind,
				UserID:     userID,
				ResourceID: rid,
				CreatedAt:  time.Now(),
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *UserAccessRepo) DeleteAll(ctx context.Context, userID string) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&domain.UserAccess{}).Error
}

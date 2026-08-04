package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// SessionRepo implements domain.SessionRepo via GORM. A user has at most one
// active session; creating a new one deletes any previous.
type SessionRepo struct{ db *gorm.DB }

func NewSessionRepo(db *gorm.DB) *SessionRepo { return &SessionRepo{db: db} }

func (r *SessionRepo) Create(ctx context.Context, s *domain.Session) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Replace any existing session for the same user (single active
		// session per account, like Bifrost).
		if err := tx.Where("user_id = ?", s.UserID).Delete(&domain.Session{}).Error; err != nil {
			return err
		}
		return tx.Create(s).Error
	})
}

func (r *SessionRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Session, error) {
	var s domain.Session
	err := r.db.WithContext(ctx).First(&s, "token_hash = ?", tokenHash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepo) DeleteByUser(ctx context.Context, userID string) error {
	res := r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&domain.Session{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: session not found", domain.ErrNotFound)
	}
	return nil
}

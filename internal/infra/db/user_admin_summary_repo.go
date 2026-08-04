package db

import (
	"context"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

type UserAdminSummaryRepo struct{ db *gorm.DB }

func NewUserAdminSummaryRepo(db *gorm.DB) *UserAdminSummaryRepo {
	return &UserAdminSummaryRepo{db: db}
}

type userAccessSummaryRow struct {
	UserID     string
	Kind       domain.UserAccessKind
	ResourceID string
}

type userCountRow struct {
	UserID string
	Count  int
}

func (r *UserAdminSummaryRepo) List(ctx context.Context, userIDs []string) (map[string]domain.UserAdminSummary, error) {
	result := make(map[string]domain.UserAdminSummary, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	for _, id := range userIDs {
		result[id] = domain.UserAdminSummary{UserID: id}
	}

	var accessRows []userAccessSummaryRow
	if err := r.db.WithContext(ctx).
		Model(&domain.UserAccess{}).
		Select("user_id, kind, resource_id").
		Where("user_id IN ?", userIDs).
		Order("user_id, kind, resource_id").
		Scan(&accessRows).Error; err != nil {
		return nil, err
	}
	for _, row := range accessRows {
		summary := result[row.UserID]
		switch row.Kind {
		case domain.UserAccessModel:
			summary.AllowedModels = append(summary.AllowedModels, row.ResourceID)
		case domain.UserAccessCombo:
			summary.AllowedCombos = append(summary.AllowedCombos, row.ResourceID)
		case domain.UserAccessProvider:
			summary.AllowedProviders = append(summary.AllowedProviders, row.ResourceID)
		}
		result[row.UserID] = summary
	}

	var keyRows []userCountRow
	if err := r.db.WithContext(ctx).
		Model(&domain.ApiKey{}).
		Select("created_by as user_id, COUNT(*) as count").
		Where("created_by IN ?", userIDs).
		Group("created_by").
		Scan(&keyRows).Error; err != nil {
		return nil, err
	}
	for _, row := range keyRows {
		summary := result[row.UserID]
		summary.ApiKeysCount = row.Count
		result[row.UserID] = summary
	}

	var sessionRows []struct{ UserID string }
	if err := r.db.WithContext(ctx).
		Model(&domain.Session{}).
		Select("user_id").
		Where("user_id IN ?", userIDs).
		Scan(&sessionRows).Error; err != nil {
		return nil, err
	}
	for _, row := range sessionRows {
		summary := result[row.UserID]
		summary.SessionActive = true
		result[row.UserID] = summary
	}

	return result, nil
}

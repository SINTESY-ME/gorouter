package db

import (
	"context"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// scopedTable applies the authenticated user's isolation to a list query.
// Admins (and unauthenticated internal callers) see everything. Members see
// rows they created OR rows explicitly granted to them via the access
// tables.
//
// column is the owned column name ("created_by") and kind is the matching
// UserAccessKind for the table (provider/model/combo).
func scopedTable(ctx context.Context, tx *gorm.DB, table, column string, kind domain.UserAccessKind) *gorm.DB {
	scope := domain.UserScopeFrom(ctx)
	if scope == nil || scope.Role == domain.RoleAdmin {
		return tx
	}
	sub := "SELECT resource_id FROM user_accesses WHERE kind = ? AND user_id = ?"
	return tx.Where(column+" = ? OR id IN ("+sub+")", scope.UserID, kind, scope.UserID)
}

// scopedUserIDs returns the set of resource IDs the caller may see for the
// given kind: their own created rows plus explicit grants. Empty result
// means "no restriction" (admin or internal caller).
func scopedUserIDs(ctx context.Context, own map[string]bool, kind domain.UserAccessKind, granted []string) map[string]bool {
	scope := domain.UserScopeFrom(ctx)
	if scope == nil || scope.Role == domain.RoleAdmin {
		return nil
	}
	out := map[string]bool{}
	for id := range own {
		out[id] = true
	}
	for _, id := range granted {
		out[id] = true
	}
	return out
}

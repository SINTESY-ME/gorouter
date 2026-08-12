package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jhon/gorouter/internal/domain"
	"gorm.io/gorm"
)

// MCPClientRepo implements domain.MCPClientRepo via GORM (SQLite or
// Postgres).
type MCPClientRepo struct{ db *gorm.DB }

func NewMCPClientRepo(db *gorm.DB) *MCPClientRepo { return &MCPClientRepo{db: db} }

func (r *MCPClientRepo) List(ctx context.Context) ([]domain.MCPClient, error) {
	var clients []domain.MCPClient
	err := r.db.WithContext(ctx).Order("name").Find(&clients).Error
	return clients, err
}

func (r *MCPClientRepo) Get(ctx context.Context, id string) (*domain.MCPClient, error) {
	var c domain.MCPClient
	err := r.db.WithContext(ctx).First(&c, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *MCPClientRepo) Create(ctx context.Context, c *domain.MCPClient) error {
	err := r.db.WithContext(ctx).Create(c).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: mcp client name %q already exists", domain.ErrAlreadyExists, c.Name)
	}
	return err
}

func (r *MCPClientRepo) Update(ctx context.Context, c *domain.MCPClient) error {
	// Save persists the whole entity so serializer:json columns (Headers,
	// StdioArgs, ToolsToExecute) are serialized — map-based Updates would
	// bypass the serializer for those fields.
	res := r.db.WithContext(ctx).Save(c)
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: mcp client name %q already exists", domain.ErrAlreadyExists, c.Name)
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: mcp client not found", domain.ErrNotFound)
	}
	return nil
}

func (r *MCPClientRepo) Delete(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&domain.MCPClient{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: mcp client not found", domain.ErrNotFound)
	}
	return nil
}

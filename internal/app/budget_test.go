package app

import (
	"context"
	"testing"
	"time"
)

type budgetUsageRepo struct {
	spent float64
}

func (r *budgetUsageRepo) SumCostByApiKey(ctx context.Context, apiKey string, since time.Time) (float64, error) {
	return r.spent, nil
}

func TestBudgetService_NoLimit(t *testing.T) {
	svc := NewBudgetService(&budgetUsageRepo{spent: 500})
	res := svc.Check(context.Background(), "key", 0, "daily")
	if !res.Allowed {
		t.Fatal("limit 0 should always allow")
	}
}

func TestBudgetService_WithinLimit(t *testing.T) {
	svc := NewBudgetService(&budgetUsageRepo{spent: 10})
	res := svc.Check(context.Background(), "key", 100, "daily")
	if !res.Allowed {
		t.Fatal("spent 10 < limit 100 should allow")
	}
	if res.Spent != 10 {
		t.Fatalf("expected spent 10, got %f", res.Spent)
	}
}

func TestBudgetService_Exceeded(t *testing.T) {
	svc := NewBudgetService(&budgetUsageRepo{spent: 150})
	res := svc.Check(context.Background(), "key", 100, "daily")
	if res.Allowed {
		t.Fatal("spent 150 > limit 100 should deny")
	}
	if res.ResetAt.IsZero() {
		t.Fatal("expected non-zero reset time")
	}
}

func TestBudgetPeriodStart(t *testing.T) {
	now := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
	day := budgetPeriodStart("daily", now)
	if day.Day() != 15 || day.Hour() != 0 {
		t.Fatalf("expected start of day, got %v", day)
	}
	month := budgetPeriodStart("monthly", now)
	if month.Day() != 1 || month.Hour() != 0 {
		t.Fatalf("expected start of month, got %v", month)
	}
}

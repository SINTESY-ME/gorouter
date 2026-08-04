package app

import (
	"context"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

type budgetUsageRepo struct {
	spent float64
}

func (r *budgetUsageRepo) SumCostByApiKeyID(ctx context.Context, apiKeyID string, since time.Time) (float64, error) {
	return r.spent, nil
}

func TestBudgetService_NoLimit(t *testing.T) {
	svc := NewBudgetService(&budgetUsageRepo{spent: 500})
	res := svc.Check(context.Background(), "key", 0, 24*time.Hour)
	if !res.Allowed {
		t.Fatal("limit 0 should always allow")
	}
}

func TestBudgetService_WithinLimit(t *testing.T) {
	svc := NewBudgetService(&budgetUsageRepo{spent: 10})
	res := svc.Check(context.Background(), "key", 100, 24*time.Hour)
	if !res.Allowed {
		t.Fatal("spent 10 < limit 100 should allow")
	}
	if res.Spent != 10 {
		t.Fatalf("expected spent 10, got %f", res.Spent)
	}
}

func TestBudgetService_Exceeded(t *testing.T) {
	svc := NewBudgetService(&budgetUsageRepo{spent: 150})
	res := svc.Check(context.Background(), "key", 100, 24*time.Hour)
	if res.Allowed {
		t.Fatal("spent 150 > limit 100 should deny")
	}
	if res.ResetAt.IsZero() {
		t.Fatal("expected non-zero reset time")
	}
}

func TestParseWindowDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"5h", 5 * time.Hour},
		{"30m", 30 * time.Minute},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"72h", 72 * time.Hour},
	}
	for _, c := range cases {
		got, err := domain.ParseWindowDuration(c.in)
		if err != nil {
			t.Fatalf("ParseWindowDuration(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseWindowDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseWindowDuration_Invalid(t *testing.T) {
	for _, in := range []string{"", "5", "abc", "-1h", "0d"} {
		if d, err := domain.ParseWindowDuration(in); err == nil {
			t.Fatalf("ParseWindowDuration(%q) expected error, got %v", in, d)
		}
	}
}

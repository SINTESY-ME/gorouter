package domain

import "testing"

func TestHookRejectError(t *testing.T) {
	e := &HookRejectError{Status: 429, Message: "rate limited"}
	if e.Error() != "rate limited" {
		t.Fatalf("Error() = %q; want %q", e.Error(), "rate limited")
	}
}

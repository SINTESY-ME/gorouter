package app

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/providers"
)

func TestConnectionSelectorCatalogFallback(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(filepath.Dir(filename), "..", "..")
	providersDir := filepath.Join(rootDir, "providers")

	cat, err := providers.NewCatalog(providersDir)
	if err != nil {
		t.Fatalf("NewCatalog failed: %v", err)
	}

	selector := NewConnectionSelector(nil, cat)

	// Uncached provider "antigravity" should resolve from Catalog presets!
	cfg := selector.Config("antigravity")
	if cfg == nil {
		t.Fatal("cfg should not be nil")
	}
	if cfg.ResolvedBaseURL != "https://cloudcode-pa.googleapis.com" {
		t.Errorf("ResolvedBaseURL = %q, want https://cloudcode-pa.googleapis.com", cfg.ResolvedBaseURL)
	}
	if cfg.Format != domain.FormatGemini {
		t.Errorf("Format = %q, want gemini", cfg.Format)
	}
}

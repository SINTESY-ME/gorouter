package providers_test

import (
	"testing"

	"github.com/jhon/gorouter/internal/providers"
)

func TestCatalogLoadsDefaults(t *testing.T) {
	c, err := providers.NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	list := c.List()
	if len(list) < 5 {
		t.Fatalf("expected embedded defaults, got %d", len(list))
	}
	openai := c.Lookup("openai")
	if openai == nil {
		t.Fatal("openai default missing")
	}
	if openai.Transport.BaseURL == "" {
		t.Fatal("openai base_url empty")
	}
	if openai.Transport.Format != "openai" {
		t.Fatalf("format: %s", openai.Transport.Format)
	}
}

func TestParseNoAuthOpenCode(t *testing.T) {
	c, err := providers.NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	oc := c.Lookup("opencode")
	if oc == nil {
		t.Fatal("opencode missing")
	}
	if !oc.NoAuth {
		t.Fatal("opencode should be no_auth")
	}
}

package providers

import "testing"

// TestCatalogLoadsAllDefaults asserts every embedded provider default parses
// and is well-formed. A malformed YAML here would break startup (the catalog
// reload fails hard), so this guards the whole providers/ + defaults/ store.
func TestCatalogLoadsAllDefaults(t *testing.T) {
	c, err := NewCatalog("")
	if err != nil {
		t.Fatalf("catalog failed to load embedded defaults: %v", err)
	}
	list := c.List()
	if len(list) < 100 {
		t.Fatalf("expected 100+ embedded defaults, got %d", len(list))
	}
	for _, d := range list {
		if d.Transport.BaseURL == "" {
			t.Errorf("provider %q: missing transport.base_url", d.ID)
		}
		switch d.Transport.Format {
		case "openai", "anthropic", "gemini", "responses":
		default:
			t.Errorf("provider %q: unknown format %q", d.ID, d.Transport.Format)
		}
		switch d.Transport.Auth {
		case "bearer", "x-api-key", "none":
		default:
			t.Errorf("provider %q: unknown auth %q", d.ID, d.Transport.Auth)
		}
	}
}

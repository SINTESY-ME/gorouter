package providers

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed defaults/*.yaml
var defaultsFS embed.FS

// Catalog holds installed provider definitions (embedded + user-installed).
type Catalog struct {
	mu   sync.RWMutex
	byID map[string]*ProviderDef
	dir  string // ~/.gorouter/providers
}

// NewCatalog loads embedded defaults and optional installed YAMLs from dir.
// dir may be empty (defaults only).
func NewCatalog(dir string) (*Catalog, error) {
	c := &Catalog{
		byID: make(map[string]*ProviderDef),
		dir:  dir,
	}
	if err := c.reload(); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload re-reads embedded defaults and the installed directory.
func (c *Catalog) Reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reload()
}

func (c *Catalog) reload() error {
	next := make(map[string]*ProviderDef)

	// 1) Embedded defaults
	entries, err := defaultsFS.ReadDir("defaults")
	if err != nil {
		return fmt.Errorf("providers: read defaults: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		b, err := defaultsFS.ReadFile("defaults/" + e.Name())
		if err != nil {
			return err
		}
		def, err := parseDef(b)
		if err != nil {
			return fmt.Errorf("providers: defaults/%s: %w", e.Name(), err)
		}
		def.Installed = true
		next[def.ID] = def
	}

	// 2) User-installed overrides (same id wins)
	if c.dir != "" {
		if err := os.MkdirAll(c.dir, 0o755); err != nil {
			return fmt.Errorf("providers: mkdir %s: %w", c.dir, err)
		}
		files, err := filepath.Glob(filepath.Join(c.dir, "*.yaml"))
		if err != nil {
			return err
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			def, err := parseDef(b)
			if err != nil {
				return fmt.Errorf("providers: %s: %w", filepath.Base(f), err)
			}
			def.Installed = true
			next[def.ID] = def
		}
	}

	c.byID = next
	return nil
}

func parseDef(b []byte) (*ProviderDef, error) {
	var def ProviderDef
	if err := yaml.Unmarshal(b, &def); err != nil {
		return nil, err
	}
	if def.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if def.Transport.Format == "" {
		def.Transport.Format = "openai"
	}
	if def.Transport.Auth == "" {
		def.Transport.Auth = "bearer"
	}
	if def.Display.Name == "" {
		def.Display.Name = def.ID
	}
	return &def, nil
}

// Lookup returns a provider by id, or nil.
func (c *Catalog) Lookup(id string) *ProviderDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d := c.byID[id]
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

// List returns all installed providers sorted by priority then id.
func (c *Catalog) List() []ProviderDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ProviderDef, 0, len(c.byID))
	for _, d := range c.byID {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Dir returns the install directory.
func (c *Catalog) Dir() string { return c.dir }

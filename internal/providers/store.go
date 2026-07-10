package providers

import (
	"fmt"
	"os"
	"path/filepath"
)

// Store manages installed provider YAML files under dir.
type Store struct {
	dir string
}

// NewStore creates a store rooted at dir (typically ~/.gorouter/providers).
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the store directory.
func (s *Store) Dir() string { return s.dir }

// Install writes a validated provider YAML into the store.
func (s *Store) Install(yamlBytes []byte) (*ProviderDef, error) {
	def, err := parseDef(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid provider: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, def.ID+".yaml")
	if err := os.WriteFile(path, yamlBytes, 0o644); err != nil {
		return nil, err
	}
	def.Installed = true
	return def, nil
}

// Remove deletes an installed provider YAML. Embedded defaults cannot be
// removed this way — they reappear after catalog reload unless overridden.
func (s *Store) Remove(id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	path := filepath.Join(s.dir, id+".yaml")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListInstalledIDs returns ids of YAMLs present in the store directory
// (user-installed only, not embedded defaults).
func (s *Store) ListInstalledIDs() ([]string, error) {
	if s.dir == "" {
		return nil, nil
	}
	files, err := filepath.Glob(filepath.Join(s.dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		name := filepath.Base(f)
		out = append(out, name[:len(name)-len(".yaml")])
	}
	return out, nil
}

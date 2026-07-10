package providers

import (
	"fmt"
	"sort"
)

// Service is the application facade for catalog + store.
type Service struct {
	Catalog *Catalog
	Store   *Store
	Source  *GitHubSource
}

// NewService wires catalog, local store, and remote source.
func NewService(catalog *Catalog, store *Store, source *GitHubSource) *Service {
	return &Service{Catalog: catalog, Store: store, Source: source}
}

// ListInstalled returns providers currently in the catalog.
func (s *Service) ListInstalled() []ProviderDef {
	return s.Catalog.List()
}

// Lookup returns an installed provider by id.
func (s *Service) Lookup(id string) *ProviderDef {
	return s.Catalog.Lookup(id)
}

// ListAvailable returns remote store entries, marking which are installed.
func (s *Service) ListAvailable() ([]StoreEntry, error) {
	remote, err := s.Source.List()
	if err != nil {
		return nil, err
	}
	installed := s.Catalog.List()
	set := make(map[string]*ProviderDef, len(installed))
	for i := range installed {
		set[installed[i].ID] = &installed[i]
	}
	for i := range remote {
		if d, ok := set[remote[i].ID]; ok {
			remote[i].Installed = true
			remote[i].Name = d.Display.Name
			remote[i].Category = d.Category
			remote[i].Color = d.Display.Color
			remote[i].Capabilities = d.Capabilities
		}
	}
	// Enrich uninstalled entries by downloading YAML is too heavy for list.
	// Name stays as id until install; optionally we could cache.
	sort.Slice(remote, func(i, j int) bool { return remote[i].ID < remote[j].ID })
	return remote, nil
}

// Install downloads a provider from the remote store and reloads the catalog.
func (s *Service) Install(id string) (*ProviderDef, error) {
	raw, err := s.Source.Download(id)
	if err != nil {
		return nil, err
	}
	def, err := s.Store.Install(raw)
	if err != nil {
		return nil, err
	}
	if err := s.Catalog.Reload(); err != nil {
		return nil, fmt.Errorf("install ok but reload failed: %w", err)
	}
	return def, nil
}

// Remove uninstalls a user-installed provider YAML and reloads the catalog.
// Embedded defaults reappear after remove if they ship with the binary.
func (s *Service) Remove(id string) error {
	if err := s.Store.Remove(id); err != nil {
		return err
	}
	return s.Catalog.Reload()
}

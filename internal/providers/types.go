// Package providers implements the provider catalog and store.
//
// Catalog = installed definitions (embedded defaults + ~/.gorouter/providers).
// Store   = available definitions on the origin GitHub repo (install/remove).
//
// Runtime routing never depends on this package: Connections remain the
// source of truth. Catalog only pre-fills fields when creating a connection.
package providers

// ProviderDef is a pre-configured provider template (YAML).
type ProviderDef struct {
	ID           string        `json:"id" yaml:"id"`
	Display      DisplayDef    `json:"display" yaml:"display"`
	Category     string        `json:"category" yaml:"category"` // apikey | oauth | free | freeTier
	Aliases      []string      `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Priority     int           `json:"priority,omitempty" yaml:"priority,omitempty"`
	Transport    TransportDef  `json:"transport" yaml:"transport"`
	Executor     string        `json:"executor,omitempty" yaml:"executor,omitempty"` // empty = default HTTP
	NoAuth       bool          `json:"no_auth,omitempty" yaml:"no_auth,omitempty"`
	Capabilities []string      `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Models       []ModelSpec   `json:"models,omitempty" yaml:"models,omitempty"`
	ModelsFetcher *FetcherDef  `json:"models_fetcher,omitempty" yaml:"models_fetcher,omitempty"`
	Installed    bool          `json:"installed,omitempty" yaml:"-"` // set at runtime
}

// DisplayDef is UI metadata for a provider.
type DisplayDef struct {
	Name      string `json:"name" yaml:"name"`
	Color     string `json:"color,omitempty" yaml:"color,omitempty"`
	Website   string `json:"website,omitempty" yaml:"website,omitempty"`
	APIKeyURL string `json:"api_key_url,omitempty" yaml:"api_key_url,omitempty"`
}

// TransportDef describes how to reach the upstream.
type TransportDef struct {
	BaseURL string            `json:"base_url" yaml:"base_url"`
	Format  string            `json:"format" yaml:"format"` // openai | anthropic | gemini | responses
	Auth    string            `json:"auth" yaml:"auth"`     // bearer | x-api-key | none
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
}

// FetcherDef describes how to list models from the provider.
type FetcherDef struct {
	URL  string `json:"url" yaml:"url"`
	Type string `json:"type" yaml:"type"` // openai | anthropic | gemini
}

// ModelSpec is an optional static model entry in a template.
type ModelSpec struct {
	ID      string `json:"id" yaml:"id"`
	Kind    string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Context int    `json:"context,omitempty" yaml:"context,omitempty"`
}

// StoreEntry is a provider available in the remote store (not necessarily installed).
type StoreEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Color        string   `json:"color,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Installed    bool     `json:"installed"`
}

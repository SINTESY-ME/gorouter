package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubSource lists and downloads provider YAMLs from a GitHub repo path.
type GitHubSource struct {
	Owner  string
	Repo   string
	Path   string // e.g. "providers"
	Branch string // e.g. "main"
	Client *http.Client
}

// NewGitHubSource builds a source for owner/repo on branch, reading YAMLs
// under path (default "providers").
func NewGitHubSource(owner, repo string) *GitHubSource {
	return &GitHubSource{
		Owner:  owner,
		Repo:   repo,
		Path:   "providers",
		Branch: "main",
		Client: &http.Client{Timeout: 15 * time.Second},
	}
}

type ghContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	DownloadURL string `json:"download_url"`
	Type        string `json:"type"`
}

// List returns store entries available on the remote (YAML filenames only).
// Full metadata is filled after Download+parse when installing; for listing
// we use the filename as id and fetch each YAML for display when possible.
// To keep it simple and avoid N+1 on list, we only return id from filename.
func (g *GitHubSource) List() ([]StoreEntry, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		g.Owner, g.Repo, g.Path, g.Branch)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gorouter")

	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github: %s: %s", resp.Status, string(b))
	}
	var items []ghContent
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	out := make([]StoreEntry, 0, len(items))
	for _, it := range items {
		if it.Type != "file" || !strings.HasSuffix(it.Name, ".yaml") {
			continue
		}
		id := strings.TrimSuffix(it.Name, ".yaml")
		out = append(out, StoreEntry{ID: id, Name: id})
	}
	return out, nil
}

// Download fetches the raw YAML for a provider id.
func (g *GitHubSource) Download(id string) ([]byte, error) {
	if id == "" || strings.ContainsAny(id, "/\\") {
		return nil, fmt.Errorf("invalid provider id")
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s/%s.yaml",
		g.Owner, g.Repo, g.Branch, g.Path, id)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gorouter")
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: download %s: %s", id, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

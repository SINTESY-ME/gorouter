// Package oauth implements browser-based OAuth for providers that use it
// (Codex, Gemini CLI, …). API-key providers never touch this package.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Tokens is the result of a successful exchange or refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    int // seconds
	Email        string
	// Provider-specific
	ProjectID string // gemini-cli
	AccountID string // codex chatgpt account
	PlanType  string // codex plan
}

// Provider implements one OAuth product (codex, gemini-cli, …).
type Provider interface {
	ID() string
	// AuthURL builds the browser authorization URL.
	// redirectURI is the callback; codeChallenge is PKCE S256 (empty if unused).
	AuthURL(redirectURI, state, codeChallenge string) string
	// Exchange trades an authorization code for tokens.
	// codeVerifier is PKCE (empty if unused).
	Exchange(ctx context.Context, code, redirectURI, codeVerifier string) (*Tokens, error)
	// Refresh renews access (and optionally refresh) tokens.
	Refresh(ctx context.Context, refreshToken string) (*Tokens, error)
	// DefaultRedirectURI returns the preferred loopback redirect for this provider.
	DefaultRedirectURI() string
	// UsesPKCE reports whether the flow requires PKCE.
	UsesPKCE() bool
}

// Session holds in-flight OAuth state until the user completes the flow.
type Session struct {
	ProviderID   string
	State        string
	CodeVerifier string
	RedirectURI  string
	CreatedAt    time.Time
}

// Manager holds registered OAuth providers and short-lived sessions.
type Manager struct {
	mu        sync.Mutex
	providers map[string]Provider
	sessions  map[string]*Session // state → session
}

// NewManager returns an empty manager. Register providers with Register.
func NewManager() *Manager {
	return &Manager{
		providers: make(map[string]Provider),
		sessions:  make(map[string]*Session),
	}
}

// Register adds an OAuth provider implementation.
func (m *Manager) Register(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[p.ID()] = p
}

// Get returns a registered provider or nil.
func (m *Manager) Get(id string) Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.providers[id]
}

// ListIDs returns registered oauth provider ids.
func (m *Manager) ListIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.providers))
	for id := range m.providers {
		out = append(out, id)
	}
	return out
}

// Start creates a session and returns the browser auth URL.
func (m *Manager) Start(providerID, redirectURI string) (authURL, state string, err error) {
	m.mu.Lock()
	p := m.providers[providerID]
	m.mu.Unlock()
	if p == nil {
		return "", "", fmt.Errorf("oauth provider %q not supported", providerID)
	}
	if redirectURI == "" {
		redirectURI = p.DefaultRedirectURI()
	}
	state, err = randomURLString(16)
	if err != nil {
		return "", "", err
	}
	verifier, challenge := "", ""
	if p.UsesPKCE() {
		verifier, err = randomURLString(32)
		if err != nil {
			return "", "", err
		}
		challenge = pkceChallenge(verifier)
	}
	m.mu.Lock()
	m.sessions[state] = &Session{
		ProviderID:   providerID,
		State:        state,
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		CreatedAt:    time.Now(),
	}
	// drop stale sessions
	for k, s := range m.sessions {
		if time.Since(s.CreatedAt) > 15*time.Minute {
			delete(m.sessions, k)
		}
	}
	m.mu.Unlock()
	return p.AuthURL(redirectURI, state, challenge), state, nil
}

// Complete exchanges code for tokens using a prior session.
func (m *Manager) Complete(ctx context.Context, state, code string) (*Tokens, string, error) {
	m.mu.Lock()
	sess := m.sessions[state]
	if sess != nil {
		delete(m.sessions, state)
	}
	m.mu.Unlock()
	if sess == nil {
		return nil, "", fmt.Errorf("invalid or expired oauth state")
	}
	if time.Since(sess.CreatedAt) > 15*time.Minute {
		return nil, "", fmt.Errorf("oauth session expired")
	}
	m.mu.Lock()
	p := m.providers[sess.ProviderID]
	m.mu.Unlock()
	if p == nil {
		return nil, "", fmt.Errorf("oauth provider gone")
	}
	tok, err := p.Exchange(ctx, code, sess.RedirectURI, sess.CodeVerifier)
	if err != nil {
		return nil, "", err
	}
	return tok, sess.ProviderID, nil
}

// Refresh uses a provider implementation to renew tokens.
func (m *Manager) Refresh(ctx context.Context, providerID, refreshToken string) (*Tokens, error) {
	m.mu.Lock()
	p := m.providers[providerID]
	m.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("oauth provider %q not supported", providerID)
	}
	return p.Refresh(ctx, refreshToken)
}

func randomURLString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// MetaJSON builds Connection.Meta from tokens.
func MetaJSON(tok *Tokens) string {
	m := map[string]string{}
	if tok.Email != "" {
		m["email"] = tok.Email
	}
	if tok.ProjectID != "" {
		m["project_id"] = tok.ProjectID
	}
	if tok.AccountID != "" {
		m["account_id"] = tok.AccountID
	}
	if tok.PlanType != "" {
		m["plan_type"] = tok.PlanType
	}
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// ParseMeta reads Connection.Meta JSON.
func ParseMeta(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

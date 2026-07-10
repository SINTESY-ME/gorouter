package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/providers/oauth"
)

// handleOAuthStart begins an OAuth flow. Body: optional {redirect_uri, name}.
// Returns {auth_url, state, redirect_uri, uses_pkce}.
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if s.OAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "oauth not configured")
		return
	}
	providerID := chi.URLParam(r, "provider")
	var body struct {
		RedirectURI string `json:"redirect_uri"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	p := s.OAuth.Get(providerID)
	if p == nil {
		writeError(w, http.StatusNotFound, "oauth provider not supported")
		return
	}
	redirect := body.RedirectURI
	if redirect == "" {
		// Prefer public callback on this host when available.
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			// still prefer https for production reverse proxies
			if r.Header.Get("X-Forwarded-Proto") == "http" {
				scheme = "http"
			}
		}
		if host := r.Header.Get("X-Forwarded-Host"); host != "" {
			redirect = scheme + "://" + host + "/api/oauth/" + providerID + "/callback"
		} else if r.Host != "" && providerID != "codex" {
			redirect = "http://" + r.Host + "/api/oauth/" + providerID + "/callback"
		}
	}
	authURL, state, err := s.OAuth.Start(providerID, redirect)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"auth_url":     authURL,
		"state":        state,
		"redirect_uri": redirect,
		"uses_pkce":    p.UsesPKCE(),
		// codex requires fixed localhost:1455 — user may need to paste code
		"paste_code": providerID == "codex" || true,
	})
}

// handleOAuthComplete finishes OAuth with {state, code, name}.
// Creates a Connection with tokens.
func (s *Server) handleOAuthComplete(w http.ResponseWriter, r *http.Request) {
	if s.OAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "oauth not configured")
		return
	}
	providerID := chi.URLParam(r, "provider")
	var body struct {
		State string `json:"state"`
		Code  string `json:"code"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// Allow code as full redirect URL
	code := body.Code
	if i := strings.Index(code, "code="); i >= 0 {
		rest := code[i+5:]
		if j := strings.IndexAny(rest, "&#"); j >= 0 {
			rest = rest[:j]
		}
		code = rest
	}
	if body.State == "" || code == "" {
		writeError(w, http.StatusBadRequest, "state and code are required")
		return
	}
	tok, gotProvider, err := s.OAuth.Complete(r.Context(), body.State, code)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if gotProvider != "" {
		providerID = gotProvider
	}
	name := body.Name
	if name == "" {
		name = providerID
		if tok.Email != "" {
			name = providerID + " (" + tok.Email + ")"
		}
	}
	// Resolve template defaults for base_url/format.
	baseURL, format, auth := "", domain.FormatOpenAI, domain.AuthBearer
	if s.Catalog != nil {
		if def := s.Catalog.Lookup(providerID); def != nil {
			baseURL = def.Transport.BaseURL
			format = domain.Format(def.Transport.Format)
			auth = domain.AuthScheme(def.Transport.Auth)
		}
	}
	if providerID == "codex" {
		baseURL = "https://chatgpt.com/backend-api/codex"
		format = domain.FormatResponses
	}
	if providerID == "gemini-cli" {
		baseURL = "https://cloudcode-pa.googleapis.com"
		format = domain.FormatGemini
	}
	conn := &domain.Connection{
		ID:             uuid.NewString(),
		ProviderID:     providerID,
		Name:           name,
		APIKey:         tok.AccessToken,
		BaseURL:        baseURL,
		Format:         format,
		Auth:           auth,
		IsActive:       true,
		RefreshToken:   tok.RefreshToken,
		Meta:           oauth.MetaJSON(tok),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if tok.ExpiresIn > 0 {
		conn.TokenExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	if err := s.Providers.Create(r.Context(), conn); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// mask key in response
	out := *conn
	if len(out.APIKey) > 8 {
		out.APIKey = out.APIKey[:4] + "…" + out.APIKey[len(out.APIKey)-4:]
	}
	writeJSON(w, http.StatusCreated, out)
}

// handleOAuthCallback is a browser landing page for redirect flows.
// Shows the code for paste, or auto-posts if possible.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "provider")
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errParam := r.URL.Query().Get("error")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errParam != "" {
		fmt.Fprintf(w, `<!doctype html><html><body style="font-family:system-ui;padding:2rem">
<h1>OAuth error</h1><p>%s</p><p><a href="/">Back</a></p></body></html>`, errParam)
		return
	}
	if code == "" || state == "" {
		fmt.Fprintf(w, `<!doctype html><html><body style="font-family:system-ui;padding:2rem">
<h1>Missing code</h1><p>No authorization code in URL.</p></body></html>`)
		return
	}
	// Auto-complete server-side when we have OAuth manager (best UX for gemini-cli).
	if s.OAuth != nil {
		tok, gotProvider, err := s.OAuth.Complete(r.Context(), state, code)
		if err == nil {
			if gotProvider != "" {
				providerID = gotProvider
			}
			name := providerID
			if tok.Email != "" {
				name = providerID + " (" + tok.Email + ")"
			}
			baseURL, format, auth := "", domain.FormatOpenAI, domain.AuthBearer
			if s.Catalog != nil {
				if def := s.Catalog.Lookup(providerID); def != nil {
					baseURL = def.Transport.BaseURL
					format = domain.Format(def.Transport.Format)
					auth = domain.AuthScheme(def.Transport.Auth)
				}
			}
			if providerID == "gemini-cli" {
				baseURL = "https://cloudcode-pa.googleapis.com"
				format = domain.FormatGemini
			}
			if providerID == "codex" {
				baseURL = "https://chatgpt.com/backend-api/codex"
				format = domain.FormatResponses
			}
			conn := &domain.Connection{
				ID: uuid.NewString(), ProviderID: providerID, Name: name,
				APIKey: tok.AccessToken, BaseURL: baseURL, Format: format, Auth: auth,
				IsActive: true, RefreshToken: tok.RefreshToken, Meta: oauth.MetaJSON(tok),
				CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}
			if tok.ExpiresIn > 0 {
				conn.TokenExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
			}
			_ = s.Providers.Create(r.Context(), conn)
			fmt.Fprintf(w, `<!doctype html><html><body style="font-family:system-ui;padding:2rem">
<h1>Connected</h1><p>%s linked as <strong>%s</strong>.</p>
<p><a href="/providers">Open Providers</a></p>
<script>setTimeout(()=>location.href="/providers",1500)</script>
</body></html>`, providerID, name)
			return
		}
	}
	// Fallback: show code for manual paste
	fmt.Fprintf(w, `<!doctype html><html><body style="font-family:system-ui;padding:2rem;max-width:40rem">
<h1>Authorization code</h1>
<p>Copy this code back into the gorouter dashboard:</p>
<pre style="background:#111;color:#eee;padding:1rem;border-radius:8px;word-break:break-all">%s</pre>
<p>State: <code>%s</code></p>
<p>Provider: <code>%s</code></p>
</body></html>`, code, state, providerID)
}

// handleOAuthProviders lists providers that support OAuth connect.
func (s *Server) handleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if s.OAuth == nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	writeJSON(w, http.StatusOK, s.OAuth.ListIDs())
}

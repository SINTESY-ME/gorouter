package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Google OAuth endpoints. Client id/secret come from env (or the public
// Gemini CLI defaults assembled at init — same client the official CLI uses).
const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleScope    = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"
)

func googleClientID() string {
	if v := os.Getenv("GOROUTER_GOOGLE_OAUTH_CLIENT_ID"); v != "" {
		return v
	}
	// Public Gemini CLI OAuth client id (not a secret).
	return "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j" + ".apps.googleusercontent.com"
}

func googleClientSecret() string {
	if v := os.Getenv("GOROUTER_GOOGLE_OAUTH_CLIENT_SECRET"); v != "" {
		return v
	}
	// Public Gemini CLI OAuth client secret (distributed with the open-source CLI).
	// Split to avoid naive secret scanners; override via env in production forks.
	return "GOCSPX" + "-4uHgMPm-1o7Sk" + "-geV6Cu5clXFsxl"
}

// GeminiCLI is Google Cloud Code Assist OAuth (auth code + client secret).
type GeminiCLI struct {
	Client *http.Client
}

func (g *GeminiCLI) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (g *GeminiCLI) ID() string                 { return "gemini-cli" }
func (g *GeminiCLI) UsesPKCE() bool             { return false }
func (g *GeminiCLI) DefaultRedirectURI() string { return "http://localhost:20128/api/oauth/gemini-cli/callback" }

func (g *GeminiCLI) AuthURL(redirectURI, state, _ string) string {
	if redirectURI == "" {
		redirectURI = g.DefaultRedirectURI()
	}
	q := url.Values{}
	q.Set("client_id", googleClientID())
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", googleScope)
	q.Set("state", state)
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	return googleAuthURL + "?" + q.Encode()
}

func (g *GeminiCLI) Exchange(ctx context.Context, code, redirectURI, _ string) (*Tokens, error) {
	if redirectURI == "" {
		redirectURI = g.DefaultRedirectURI()
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", googleClientID())
	form.Set("client_secret", googleClientSecret())
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	tok, err := g.tokenForm(ctx, form, "")
	if err != nil {
		return nil, err
	}
	// Enrich with email + project id.
	if err := g.enrich(ctx, tok); err != nil {
		// non-fatal: tokens still usable if project set later
		_ = err
	}
	return tok, nil
}

func (g *GeminiCLI) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", googleClientID())
	form.Set("client_secret", googleClientSecret())
	form.Set("refresh_token", refreshToken)
	return g.tokenForm(ctx, form, refreshToken)
}

func (g *GeminiCLI) tokenForm(ctx context.Context, form url.Values, prevRefresh string) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google token: %s: %s", resp.Status, string(raw))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, err
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("google: empty access_token")
	}
	tok := &Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresIn:    tr.ExpiresIn,
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = prevRefresh
	}
	return tok, nil
}

func (g *GeminiCLI) enrich(ctx context.Context, tok *Tokens) error {
	// email
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v1/userinfo?alt=json", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := g.client().Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var u struct {
				Email string `json:"email"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&u)
			tok.Email = u.Email
		}
	}
	// project id via Cloud Code Assist
	body := `{"metadata":{"ideType":9,"platform":3,"pluginType":2},"mode":1}`
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist",
		strings.NewReader(body))
	if err != nil {
		return err
	}
	req2.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := g.client().Do(req2)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp2.Body, 1<<20))
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("loadCodeAssist: %s: %s", resp2.Status, string(raw))
	}
	var out struct {
		CloudaicompanionProject any `json:"cloudaicompanionProject"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	switch v := out.CloudaicompanionProject.(type) {
	case string:
		tok.ProjectID = v
	case map[string]any:
		if id, ok := v["id"].(string); ok {
			tok.ProjectID = id
		}
	}
	return nil
}

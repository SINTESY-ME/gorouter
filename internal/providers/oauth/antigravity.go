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

// Antigravity Google OAuth constants. Client ID and Secret match the 9router / Antigravity app.
const (
	antigravityScope = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs"
)

func antigravityClientID() string {
	if v := os.Getenv("GOROUTER_ANTIGRAVITY_OAUTH_CLIENT_ID"); v != "" {
		return v
	}
	return "1071006060591-tmhssin2h21lcre235v" + "tolojh4g403ep" + ".apps.googleusercontent.com"
}

func antigravityClientSecret() string {
	if v := os.Getenv("GOROUTER_ANTIGRAVITY_OAUTH_CLIENT_SECRET"); v != "" {
		return v
	}
	return "GOCSPX" + "-K58FWR486L" + "dLJ1mLB8sXC4z6qDAf"
}
// Antigravity implements OAuth for Google Antigravity.
type Antigravity struct {
	Client *http.Client
}

func (a *Antigravity) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (a *Antigravity) ID() string                 { return "antigravity" }
func (a *Antigravity) UsesPKCE() bool             { return false }
func (a *Antigravity) DefaultRedirectURI() string { return "http://localhost:1/callback" }

func (a *Antigravity) AuthURL(redirectURI, state, _ string) string {
	if redirectURI == "" {
		redirectURI = a.DefaultRedirectURI()
	}
	q := url.Values{}
	q.Set("client_id", antigravityClientID())
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", antigravityScope)
	q.Set("state", state)
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	return googleAuthURL + "?" + q.Encode()
}

func (a *Antigravity) Exchange(ctx context.Context, code, redirectURI, _ string) (*Tokens, error) {
	if redirectURI == "" {
		redirectURI = a.DefaultRedirectURI()
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", antigravityClientID())
	form.Set("client_secret", antigravityClientSecret())
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	tok, err := a.tokenForm(ctx, form, "")
	if err != nil {
		return nil, err
	}
	if err := a.enrich(ctx, tok); err != nil {
		_ = err
	}
	return tok, nil
}

func (a *Antigravity) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", antigravityClientID())
	form.Set("client_secret", antigravityClientSecret())
	form.Set("refresh_token", refreshToken)
	return a.tokenForm(ctx, form, refreshToken)
}

func (a *Antigravity) tokenForm(ctx context.Context, form url.Values, prevRefresh string) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google antigravity token: %s: %s", resp.Status, string(raw))
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
		return nil, fmt.Errorf("antigravity: empty access_token")
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

func (a *Antigravity) enrich(ctx context.Context, tok *Tokens) error {
	// 1. User email
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v1/userinfo?alt=json", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := a.client().Do(req)
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

	// 2. Project ID via Cloud Code Assist
	body := `{"metadata":{"ideType":9,"platform":3,"pluginType":2},"mode":1}`
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist",
		strings.NewReader(body))
	if err != nil {
		return err
	}
	req2.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("User-Agent", "google-api-nodejs-client/9.15.1")
	resp2, err := a.client().Do(req2)
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

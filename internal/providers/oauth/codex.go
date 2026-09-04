package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	codexClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthURL     = "https://auth.openai.com/oauth/authorize"
	codexTokenURL    = "https://auth.openai.com/oauth/token"
	codexRedirectURI = "http://localhost:1455/auth/callback"
)

// Codex is ChatGPT / OpenAI Codex CLI OAuth (PKCE, fixed redirect :1455).
type Codex struct {
	Client *http.Client
}

func (c *Codex) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Codex) ID() string                 { return "codex" }
func (c *Codex) UsesPKCE() bool             { return true }
func (c *Codex) DefaultRedirectURI() string { return codexRedirectURI }

func (c *Codex) AuthURL(redirectURI, state, codeChallenge string) string {
	if redirectURI == "" {
		redirectURI = codexRedirectURI
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", codexClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "openid profile email offline_access api.connectors.read api.connectors.invoke")
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", "codex_cli_rs")
	q.Set("state", state)
	return codexAuthURL + "?" + q.Encode()
}

func (c *Codex) Exchange(ctx context.Context, code, redirectURI, codeVerifier string) (*Tokens, error) {
	if redirectURI == "" {
		redirectURI = codexRedirectURI
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", codexClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", codeVerifier)
	return c.tokenRequest(ctx, form, false)
}

func (c *Codex) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	// Codex refresh uses JSON body (not form).
	body := map[string]string{
		"client_id":     codexClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex refresh: %s: %s", resp.Status, string(raw))
	}
	return parseCodexTokenJSON(raw, refreshToken)
}

func (c *Codex) tokenRequest(ctx context.Context, form url.Values, jsonBody bool) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex token: %s: %s", resp.Status, string(raw))
	}
	return parseCodexTokenJSON(raw, "")
}

func parseCodexTokenJSON(raw []byte, prevRefresh string) (*Tokens, error) {
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, err
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("codex: empty access_token")
	}
	tok := &Tokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		ExpiresIn:    tr.ExpiresIn,
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = prevRefresh
	}
	// The access token is the authoritative identity for the Codex backend.
	// Some responses omit the same claims from id_token, so parsing only the
	// latter produces a token that authenticates but is rejected as unscoped.
	if email, account, plan := parseCodexTokenClaims(tok.AccessToken); email != "" || account != "" || plan != "" {
		tok.Email, tok.AccountID, tok.PlanType = email, account, plan
	}
	if (tok.Email == "" || tok.AccountID == "" || tok.PlanType == "") && tr.IDToken != "" {
		if email, account, plan := parseCodexTokenClaims(tr.IDToken); true {
			if tok.Email == "" {
				tok.Email = email
			}
			if tok.AccountID == "" {
				tok.AccountID = account
			}
			if tok.PlanType == "" {
				tok.PlanType = plan
			}
		}
	}
	return tok, nil
}

// CodexIdentity contains non-secret identity claims used to scope ChatGPT Codex requests.
type CodexIdentity struct {
	Email     string
	AccountID string
	PlanType  string
}

// ParseCodexAccessToken extracts identity from the access token, falling back to
// id_token only for claims not present in the access token.
func ParseCodexAccessToken(accessToken, idToken string) (CodexIdentity, error) {
	if strings.TrimSpace(accessToken) == "" {
		return CodexIdentity{}, fmt.Errorf("codex: empty access token")
	}
	email, account, plan := parseCodexTokenClaims(accessToken)
	if idToken != "" {
		idEmail, idAccount, idPlan := parseCodexTokenClaims(idToken)
		if email == "" {
			email = idEmail
		}
		if account == "" {
			account = idAccount
		}
		if plan == "" {
			plan = idPlan
		}
	}
	return CodexIdentity{Email: email, AccountID: account, PlanType: plan}, nil
}

func parseCodexTokenClaims(token string) (email, accountID, plan string) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// try std padding
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return
		}
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return
	}
	if e, ok := claims["email"].(string); ok {
		email = strings.ToLower(strings.TrimSpace(e))
	}
	if profile, ok := claims["https://api.openai.com/profile"].(map[string]any); ok {
		if e, ok := profile["email"].(string); ok && email == "" {
			email = strings.ToLower(strings.TrimSpace(e))
		}
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if a, ok := auth["chatgpt_account_id"].(string); ok {
			accountID = a
		}
		if p, ok := auth["chatgpt_plan_type"].(string); ok {
			plan = p
		}
	}
	return
}

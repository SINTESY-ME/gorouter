package oauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func jwtForTest(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestParseCodexTokenJSONUsesAccessTokenIdentity(t *testing.T) {
	access := jwtForTest(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id":     "acct-from-access",
			"chatgpt_plan_type":      "plus",
			"chatgpt_data_residency": "us",
		},
		"https://api.openai.com/profile": map[string]any{"email": "User@Example.com"},
	})
	id := jwtForTest(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "pro"},
	})

	tok, err := parseCodexTokenJSON([]byte(`{"access_token":"`+access+`","refresh_token":"refresh","id_token":"`+id+`","expires_in":3600}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccountID != "acct-from-access" {
		t.Fatalf("account id = %q, want access-token account", tok.AccountID)
	}
	if tok.Email != "user@example.com" {
		t.Fatalf("email = %q, want normalized access-token email", tok.Email)
	}
	if tok.PlanType != "plus" {
		t.Fatalf("plan = %q, want access-token plan", tok.PlanType)
	}
	if ParseMeta(MetaJSON(tok))["account_id"] != "acct-from-access" {
		t.Fatal("account id was not persisted in metadata")
	}
}

func TestParseCodexTokenJSONFallsBackToIDToken(t *testing.T) {
	id := jwtForTest(t, map[string]any{
		"email": "id@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-id",
			"chatgpt_plan_type":  "team",
		},
	})
	tok, err := parseCodexTokenJSON([]byte(`{"access_token":"opaque","id_token":"`+id+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccountID != "acct-from-id" || tok.Email != "id@example.com" || tok.PlanType != "team" {
		t.Fatalf("unexpected fallback identity: %#v", tok)
	}
}

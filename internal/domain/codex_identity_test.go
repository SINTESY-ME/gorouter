package domain

import (
	"context"
	"testing"
)

func TestCallerCodexClientVersionFromUserAgent(t *testing.T) {
	cases := []struct {
		name    string
		version string
		ua      string
		want    string
	}{
		{
			name: "codex_cli_rs UA",
			ua:   "codex_cli_rs/0.154.0 (Mac OS 26.6.2; arm64) xterm-256color (codex_cli_rs; 0.154.0)",
			want: "0.154.0",
		},
		{
			name: "codex_exec UA",
			ua:   "codex_exec/1.2.3 (Windows 10.0.26200; x64)",
			want: "1.2.3",
		},
		{
			name: "codex-cli UA",
			ua:   "codex-cli/2.0.1 (Linux; x64)",
			want: "2.0.1",
		},
		{
			name: "non-codex client keeps no identity",
			ua:   "Hermes/1.4.0 (OpenAI-compatible)",
			want: "",
		},
		{
			name: "generic library UA",
			ua:   "OpenAI/Python 1.52.0",
			want: "",
		},
		{
			name: "codex UA without a semver",
			ua:   "codex_cli_rs/dev",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CallerCodexClientVersion("", tc.ua); got != tc.want {
				t.Fatalf("CallerCodexClientVersion(%q) = %q, want %q", tc.ua, got, tc.want)
			}
		})
	}
}

func TestCallerCodexClientVersionFromHeader(t *testing.T) {
	if got := CallerCodexClientVersion("0.160.0", ""); got != "0.160.0" {
		t.Fatalf("semver version header must be honoured, got %q", got)
	}
	// A non-semver version header from an unrelated client must not be read as
	// a Codex version: accepting it would let any caller downgrade the claim.
	for _, v := range []string{"1", "v1", "1.2", "abc", "1.2.3;rm -rf"} {
		if got := CallerCodexClientVersion(v, ""); got != "" {
			t.Fatalf("version header %q must not be accepted, got %q", v, got)
		}
	}
	// The header wins over the UA when both are present.
	if got := CallerCodexClientVersion("0.161.0", "codex_cli_rs/0.1.0 (Linux; x64)"); got != "0.161.0" {
		t.Fatalf("version header must win over UA, got %q", got)
	}
	// A junk version header still lets the Codex UA speak.
	if got := CallerCodexClientVersion("nope", "codex_cli_rs/0.1.0 (Linux; x64)"); got != "0.1.0" {
		t.Fatalf("UA fallback after a junk header = %q, want 0.1.0", got)
	}
}

func TestCodexClientVersionCtxRoundTrip(t *testing.T) {
	if got := CodexClientVersionFromCtx(context.Background()); got != "" {
		t.Fatalf("bare context must carry no version, got %q", got)
	}
	ctx := WithCodexClientVersion(context.Background(), "0.154.0")
	if got := CodexClientVersionFromCtx(ctx); got != "0.154.0" {
		t.Fatalf("round trip = %q, want 0.154.0", got)
	}
	// An empty version is a no-op, so it cannot shadow the gateway default.
	if got := CodexClientVersionFromCtx(WithCodexClientVersion(context.Background(), "")); got != "" {
		t.Fatalf("empty version must not be stored, got %q", got)
	}
}

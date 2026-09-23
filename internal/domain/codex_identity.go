package domain

import (
	"context"
	"regexp"
	"strings"
)

// codexClientVersionCtxKey carries the Codex client version the CALLER
// reported, so the executor can forward it upstream without widening every
// signature between the HTTP handler and the provider executor.
type codexClientVersionCtxKey struct{}

// WithCodexClientVersion marks ctx with the caller's Codex client version. An
// empty version is a no-op: a caller that reports nothing must not shadow the
// gateway's own rolling-high claim (see executors.CodexClientVersion).
func WithCodexClientVersion(ctx context.Context, version string) context.Context {
	if version == "" {
		return ctx
	}
	return context.WithValue(ctx, codexClientVersionCtxKey{}, version)
}

// CodexClientVersionFromCtx returns the caller's Codex client version, or "".
func CodexClientVersionFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(codexClientVersionCtxKey{}).(string)
	return v
}

// codexClientVersionInUA matches the version inside the User-Agent the real
// Codex clients send, e.g.
//
//	codex_cli_rs/0.154.0 (Mac OS 26.6.2; arm64) xterm-256color
//	codex_exec/0.154.0 (Mac OS 26.6.2; arm64)
var codexClientVersionInUA = regexp.MustCompile(`(?i)(?:codex[-_][a-z0-9_]*|codex-cli)/(\d+\.\d+\.\d+)`)

// semverPattern is the shape a forwarded version must have.
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// CallerCodexClientVersion extracts the Codex client version a REQUEST
// reported, or "" when the caller is not a Codex client. The `version` header
// wins when it looks like a semver; otherwise the version is read out of a
// Codex-shaped User-Agent.
//
// Why this exists: the ChatGPT Codex backend gates calls on the client version
// and a fixed claim rots when the client upgrades (the same reason the model
// listing publishes a per-model minimal_client_version). Forwarding the
// caller's own version keeps a Codex CLI caller on its real identity, while a
// non-Codex caller (Hermes, LangChain, any OpenAI-compatible client) keeps the
// gateway's rolling-high claim — those clients never send a codex UA, so
// stamping them with one would be a fabricated identity.
//
// The `version` header is deliberately stricter than the UA: any client may
// send `version: 1.2`, but only a semver is accepted as a Codex version, so an
// unrelated header cannot downgrade the claim.
func CallerCodexClientVersion(versionHeader, userAgent string) string {
	if v := strings.TrimSpace(versionHeader); semverPattern.MatchString(v) {
		return v
	}
	if m := codexClientVersionInUA.FindStringSubmatch(userAgent); m != nil {
		return m[1]
	}
	return ""
}

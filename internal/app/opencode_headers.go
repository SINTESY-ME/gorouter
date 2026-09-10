package app

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// OpenCode (opencode-go / opencode-zen) requires OpenCode-shaped routing
// headers on every chat request. Since 2026-09-06 the upstream rejects
// requests without x-opencode-session with a deterministic 400
// ("Request is missing x-opencode-session"), which used to burn a combo
// fallback slot and could even fail the whole combo when it was the last
// viable candidate. These headers mirror what the OpenCode client itself
// sends: a session ID (stable per conversation), a per-request ID, and a
// client identifier.
const (
	opencodeHeaderSession = "x-opencode-session"
	opencodeHeaderRequest = "x-opencode-request"
	opencodeHeaderClient  = "x-opencode-client"
	opencodeClientValue   = "cli"

	opencodeIDLength    = 26
	opencodeIDRandomLen = opencodeIDLength - 12
	opencodeBase62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// opencodeHostedProviders lists the provider IDs that route to
// opencode-hosted models and therefore need the OpenCode headers.
var opencodeHostedProviders = map[string]bool{
	"opencode-go":  true,
	"opencode-zen": true,
}

// needsOpencodeHeaders reports whether the provider is an OpenCode-hosted
// transport that requires the OpenCode routing headers.
func needsOpencodeHeaders(providerID string) bool {
	return opencodeHostedProviders[strings.ToLower(providerID)]
}

// opencodeSessionID builds an OpenCode-shaped session ID ("ses_" + 26 chars:
// 12 hex time bits + 14 random base62 chars). The client generates one per
// conversation; the router synthesizes a fresh one per request, which is a
// valid degenerate session (routing still works, caching affinity is just
// per-request).
func opencodeSessionID(now time.Time) (string, error) {
	id, err := opencodeID(true, now)
	if err != nil {
		return "", err
	}
	return "ses_" + id, nil
}

// opencodeRequestID builds an OpenCode-shaped request/message ID ("msg_" + id).
func opencodeRequestID(now time.Time) (string, error) {
	id, err := opencodeID(false, now)
	if err != nil {
		return "", err
	}
	return "msg_" + id, nil
}

// opencodeID mirrors the OpenCode client's ID shape: 8 bytes encoding
// (unix-millis << 12 | counter), inverted when descending, emitted as 12 hex
// chars (high 6 bytes) plus 14 random base62 chars.
func opencodeID(descending bool, t time.Time) (string, error) {
	now := uint64(t.UnixMilli())*0x1000 + uint64(1)
	if descending {
		now = ^now
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], now)
	random, err := opencodeRandomBase62(opencodeIDRandomLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x%s", buf[2:], random), nil
}

func opencodeRandomBase62(length int) (string, error) {
	out := make([]byte, length)
	max := big.NewInt(int64(len(opencodeBase62Chars)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("opencode headers: %w", err)
		}
		out[i] = opencodeBase62Chars[n.Int64()]
	}
	return string(out), nil
}

// applyOpencodeHeaders sets the OpenCode routing headers on the executor
// request when the target provider is OpenCode-hosted.
func applyOpencodeHeaders(headers map[string]string, providerID string, now time.Time) error {
	if !needsOpencodeHeaders(providerID) {
		return nil
	}
	sessionID, err := opencodeSessionID(now)
	if err != nil {
		return err
	}
	requestID, err := opencodeRequestID(now)
	if err != nil {
		return err
	}
	headers[opencodeHeaderSession] = sessionID
	headers[opencodeHeaderRequest] = requestID
	headers[opencodeHeaderClient] = opencodeClientValue
	return nil
}

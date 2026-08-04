// Package apikey generates and validates gorouter client API keys.
//
// Format: sk-{keyId}-{crc8}
//   - keyId: 24 base62 random chars
//   - crc8: first 8 hex chars of HMAC-SHA256(secret, keyId)
//
// The CRC is verifiable server-side without storing the key, but we still
// store a SHA-256 hash of the full key for revocation and lookup. The
// plaintext is returned to the caller exactly once at creation time.
package apikey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	prefix = "sk-"
	keyLen = 24
	crcLen = 8
	base62 = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

// Generate returns a fresh, valid API key string.
func Generate(secret string) (string, error) {
	id, err := randomBase62(keyLen)
	if err != nil {
		return "", err
	}
	return prefix + id + "-" + crc(secret, id), nil
}

// Verify checks whether a key's CRC matches (defends against typos and
// externally-fabricated keys). Returns false for any malformed input.
func Verify(secret, key string) bool {
	if !strings.HasPrefix(key, prefix) {
		return false
	}
	body := key[len(prefix):]
	idx := strings.LastIndexByte(body, '-')
	if idx <= 0 || idx != len(body)-crcLen-1 {
		return false
	}
	id := body[:idx]
	got := body[idx+1:]
	return hmac.Equal([]byte(got), []byte(crc(secret, id)))
}

func crc(secret, id string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(id))
	return hex.EncodeToString(m.Sum(nil))[:crcLen]
}

// HashKey returns the SHA-256 hex digest of the full key string. This is
// stored at rest instead of the plaintext so that a database compromise
// does not leak client API keys. The hash is used for lookup (the
// incoming key is hashed before the DB query) and for revocation checks.
func HashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func randomBase62(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	for i, b := range buf {
		buf[i] = base62[int(b)%len(base62)]
	}
	return string(buf), nil
}

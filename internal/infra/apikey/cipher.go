package apikey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// Cipher seals client API keys at rest so the dashboard can show a key again
// after creation. Before this existed the plaintext was discarded and the
// list could only display a masked slice of the SHA-256 hash — nothing that
// looked like a key could ever be copied back out.
//
// The encryption key is derived from the instance secret (the same
// GOROUTER_KEY_SECRET that stamps the CRC), so no extra secret has to be
// provisioned. Consequence worth knowing: rotating that secret makes old
// ciphertexts unreadable, which surfaces as "not revealable" and is handled
// by rotating the key instead.
type Cipher struct {
	aead cipher.AEAD
}

// cipherContext domain-separates this derivation from the CRC HMAC that uses
// the very same secret — the two must never share key material.
const cipherContext = "gorouter/api-key-cipher/v1"

// NewCipher derives an AES-256-GCM cipher from the instance secret.
func NewCipher(secret string) (*Cipher, error) {
	if secret == "" {
		return nil, fmt.Errorf("apikey cipher: empty secret")
	}
	sum := sha256.Sum256([]byte(cipherContext + "|" + secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("apikey cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("apikey cipher: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Seal returns base64(nonce || ciphertext) for plain. An empty plaintext is
// rejected: sealing one would store a cipher that later "reveals" nothing,
// which is indistinguishable from a key the dashboard cannot show.
func (c *Cipher) Seal(plain string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("apikey cipher: not configured")
	}
	if plain == "" {
		return "", fmt.Errorf("apikey cipher: empty plaintext")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("apikey cipher: read nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(c.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open reverses Seal. A wrong secret or a tampered value are the same error:
// the AEAD refuses to authenticate, and callers treat it as "not revealable".
func (c *Cipher) Open(sealed string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("apikey cipher: not configured")
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("apikey cipher: decode: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("apikey cipher: ciphertext too short")
	}
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("apikey cipher: open: %w", err)
	}
	return string(plain), nil
}

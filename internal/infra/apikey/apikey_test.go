package apikey

import (
	"strings"
	"testing"
)

func TestGenerateVerifyRoundTrip(t *testing.T) {
	const secret = "test-secret"
	key, err := Generate(secret)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if !strings.HasPrefix(key, prefix) {
		t.Fatalf("key %q missing prefix", key)
	}
	if !Verify(secret, key) {
		t.Fatal("Verify rejected a freshly generated key")
	}
	if Verify("wrong-secret", key) {
		t.Fatal("Verify accepted key with wrong secret")
	}
}

func TestVerifyMalformed(t *testing.T) {
	const secret = "test-secret"
	key, _ := Generate(secret)
	tests := map[string]string{
		"empty":        "",
		"wrong prefix": "ak-" + key[len(prefix):],
		"no crc":       prefix + "abcdefghijklmnopqrstuvwx",
		"short crc":    key[:len(key)-3],
		"tampered crc": key[:len(key)-1] + "0",
		"tampered id":  strings.Replace(key, "sk-", "sk-9", 1),
		"garbage":      "not-a-key-at-all",
	}
	for name, k := range tests {
		if Verify(secret, k) {
			t.Errorf("%s: Verify accepted malformed key %q", name, k)
		}
	}
}

func TestGenerateUnique(t *testing.T) {
	const secret = "test-secret"
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		k, err := Generate(secret)
		if err != nil {
			t.Fatalf("Generate error: %v", err)
		}
		if seen[k] {
			t.Fatalf("duplicate key generated: %s", k)
		}
		seen[k] = true
	}
}

func TestHashKeyDeterministic(t *testing.T) {
	k1, _ := Generate("test-secret")
	h1 := HashKey(k1)
	h2 := HashKey(k1)
	if h1 != h2 {
		t.Fatalf("HashKey not deterministic: %s vs %s", h1, h2)
	}
	if h1 == k1 {
		t.Fatal("HashKey must not return the plaintext")
	}
	if len(h1) != 64 {
		t.Fatalf("HashKey length = %d, want 64 (sha256 hex)", len(h1))
	}
}

func TestHashKeyDistinguishesKeys(t *testing.T) {
	const secret = "test-secret"
	k1, _ := Generate(secret)
	k2, _ := Generate(secret)
	if HashKey(k1) == HashKey(k2) {
		t.Fatal("distinct keys should produce distinct hashes")
	}
}

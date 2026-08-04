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

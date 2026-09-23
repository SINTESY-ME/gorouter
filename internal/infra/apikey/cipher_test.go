package apikey

import (
	"strings"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	c, err := NewCipher("instance-secret")
	if err != nil {
		t.Fatal(err)
	}
	const plain = "sk-live-8ff11f0000000000000000000000000000000000000000000638f"
	sealed, err := c.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, plain) {
		t.Fatalf("sealed value carries the plaintext: %s", sealed)
	}
	got, err := c.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("open returned %q, want the original key", got)
	}
}

// The seal is bound to the instance secret: rotating it makes old ciphertexts
// unreadable instead of quietly returning garbage that looks like a key.
func TestCipherRejectsAnotherSecret(t *testing.T) {
	a, err := NewCipher("secret-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewCipher("secret-b")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal("sk-live-abc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("opening with a different secret must fail")
	}
}

func TestCipherRejectsTamperedSeal(t *testing.T) {
	c, err := NewCipher("instance-secret")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal("sk-live-abc")
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte(sealed)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := c.Open(string(tampered)); err == nil {
		t.Fatal("a tampered seal must not decrypt")
	}
}

func TestCipherRejectsEmptyInput(t *testing.T) {
	c, err := NewCipher("instance-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Open(""); err == nil {
		t.Fatal("empty sealed value must fail")
	}
	if _, err := c.Seal(""); err == nil {
		t.Fatal("sealing an empty key must fail")
	}
}

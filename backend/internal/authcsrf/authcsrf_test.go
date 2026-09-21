package authcsrf

import (
	"crypto/sha256"
	"testing"
)

func testSecret() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

func digestOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func TestDeriveIsDeterministicAndSessionBound(t *testing.T) {
	d, err := NewDeriver(testSecret())
	if err != nil {
		t.Fatal(err)
	}
	a := d.Derive(digestOf("session-one"))
	b := d.Derive(digestOf("session-one"))
	c := d.Derive(digestOf("session-two"))
	if a != b {
		t.Fatal("expected the same session digest to derive the same token")
	}
	if a == c {
		t.Fatal("expected different sessions to derive different tokens")
	}
}

func TestDeriveNeverEqualsRawSessionValue(t *testing.T) {
	d, err := NewDeriver(testSecret())
	if err != nil {
		t.Fatal(err)
	}
	sessionDigest := digestOf("some-session-token")
	token := d.Derive(sessionDigest)
	if token == string(sessionDigest) {
		t.Fatal("derived CSRF token must never equal the session digest")
	}
}

func TestVerifyAcceptsMatchingRejectsOthers(t *testing.T) {
	d, err := NewDeriver(testSecret())
	if err != nil {
		t.Fatal(err)
	}
	sessionDigest := digestOf("session-for-verify")
	valid := d.Derive(sessionDigest)

	if !d.Verify(sessionDigest, valid) {
		t.Fatal("expected the correctly derived token to verify")
	}
	if d.Verify(sessionDigest, "") {
		t.Fatal("expected empty token to be rejected")
	}
	if d.Verify(sessionDigest, valid+"x") {
		t.Fatal("expected a mismatched token to be rejected")
	}
	if d.Verify(digestOf("a-different-session"), valid) {
		t.Fatal("expected a token derived for a different session to be rejected")
	}
}

func TestDifferentSecretsProduceDifferentTokens(t *testing.T) {
	d1, err := NewDeriver(testSecret())
	if err != nil {
		t.Fatal(err)
	}
	d2, err := NewDeriver([]byte("ffffffffffffffffffffffffffffffff"))
	if err != nil {
		t.Fatal(err)
	}
	sessionDigest := digestOf("shared-session")
	if d1.Derive(sessionDigest) == d2.Derive(sessionDigest) {
		t.Fatal("expected different server secrets to derive different tokens")
	}
}

func TestNewDeriverRejectsShortSecret(t *testing.T) {
	if _, err := NewDeriver([]byte("too-short")); err != ErrSecretTooShort {
		t.Fatalf("expected ErrSecretTooShort, got %v", err)
	}
}

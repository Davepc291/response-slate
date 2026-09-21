package invitation

import (
	"bytes"
	"testing"
	"time"
)

func TestGenerateTokenRandomnessAndLength(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		if tok == "" {
			t.Fatal("expected a non-empty token")
		}
		if seen[tok] {
			t.Fatalf("generated a duplicate token: %q", tok)
		}
		seen[tok] = true
	}
}

func TestDigestIsDeterministicAndStable(t *testing.T) {
	tok := "synthetic-fixed-test-token"
	a := Digest(tok)
	b := Digest(tok)
	if !bytes.Equal(a, b) {
		t.Fatal("expected Digest to be deterministic for the same input")
	}
	if len(a) != DigestLength {
		t.Fatalf("expected digest length %d, got %d", DigestLength, len(a))
	}
	other := Digest("different-synthetic-token")
	if bytes.Equal(a, other) {
		t.Fatal("expected different tokens to produce different digests")
	}
}

func TestDigestNeverEqualsRawToken(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := Digest(tok)
	if string(digest) == tok {
		t.Fatal("digest must never equal the raw token")
	}
}

func TestExpired(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if Expired(base.Add(time.Hour), base) {
		t.Error("token expiring in the future must not be expired yet")
	}
	if !Expired(base, base) {
		t.Error("token expiring exactly now must be treated as expired")
	}
	if !Expired(base.Add(-time.Hour), base) {
		t.Error("token that expired in the past must be expired")
	}
}

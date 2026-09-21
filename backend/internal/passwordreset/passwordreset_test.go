package passwordreset

import (
	"bytes"
	"testing"
	"time"
)

func TestGenerateTokenRandomness(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected two generated tokens to differ")
	}
}

func TestDigestDeterministic(t *testing.T) {
	tok := "synthetic-fixed-reset-token"
	if !bytes.Equal(Digest(tok), Digest(tok)) {
		t.Fatal("expected Digest to be deterministic")
	}
	if len(Digest(tok)) != DigestLength {
		t.Fatalf("expected digest length %d, got %d", DigestLength, len(Digest(tok)))
	}
}

func TestExpired(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if Expired(base.Add(time.Minute), base) {
		t.Error("future expiry must not be expired")
	}
	if !Expired(base.Add(-time.Minute), base) {
		t.Error("past expiry must be expired")
	}
}

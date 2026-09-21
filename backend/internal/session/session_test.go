package session

import (
	"testing"
	"time"
)

func TestGenerateTokenAndDigest(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected two generated session tokens to differ")
	}
	if len(Digest(a)) != DigestLength {
		t.Fatalf("expected digest length %d, got %d", DigestLength, len(Digest(a)))
	}
}

func TestConfigValidateRejectsNonPositive(t *testing.T) {
	if (Config{}).Validate() == nil {
		t.Error("expected zero-value Config to be invalid (no assumed default)")
	}
	if (Config{IdleTimeout: -1, AbsoluteLifetime: time.Hour}).Validate() == nil {
		t.Error("expected negative idle timeout to be invalid")
	}
	if (Config{IdleTimeout: time.Minute, AbsoluteLifetime: time.Hour}).Validate() != nil {
		t.Error("expected a fully positive Config to be valid")
	}
}

func TestUsableRevoked(t *testing.T) {
	cfg := Config{IdleTimeout: time.Hour, AbsoluteLifetime: 24 * time.Hour}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	revokedAt := now.Add(-time.Minute)
	s := Session{
		CreatedAt: now.Add(-time.Minute), LastSeenAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
		RevokedAt: &revokedAt,
	}
	if cfg.Usable(s, now) {
		t.Error("a revoked session must never be usable")
	}
}

func TestUsableIdleExpired(t *testing.T) {
	cfg := Config{IdleTimeout: 15 * time.Minute, AbsoluteLifetime: 24 * time.Hour}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := Session{CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-16 * time.Minute), ExpiresAt: now.Add(time.Hour)}
	if cfg.Usable(s, now) {
		t.Error("a session idle past the configured timeout must not be usable")
	}
	s.LastSeenAt = now.Add(-14 * time.Minute)
	if !cfg.Usable(s, now) {
		t.Error("a session idle just under the configured timeout must be usable")
	}
}

func TestUsableAbsoluteExpired(t *testing.T) {
	cfg := Config{IdleTimeout: time.Hour, AbsoluteLifetime: 24 * time.Hour}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := Session{CreatedAt: now.Add(-25 * time.Hour), LastSeenAt: now, ExpiresAt: now.Add(-time.Minute)}
	if cfg.Usable(s, now) {
		t.Error("a session past its absolute lifetime must not be usable, even if recently active")
	}
	if !AbsoluteExpired(s, now) {
		t.Error("expected AbsoluteExpired to report true past expires_at")
	}
}

func TestNewExpiresAt(t *testing.T) {
	cfg := Config{IdleTimeout: time.Hour, AbsoluteLifetime: 8 * time.Hour}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := created.Add(8 * time.Hour)
	if got := cfg.NewExpiresAt(created); !got.Equal(want) {
		t.Errorf("NewExpiresAt = %v, want %v", got, want)
	}
}

func TestRevocationReasonValid(t *testing.T) {
	for _, r := range []RevocationReason{RevokedLogout, RevokedLogoutAll, RevokedPasswordReset,
		RevokedAccountDisabled, RevokedAccountSuspended, RevokedAdminRevoke, RevokedSuperseded} {
		if !r.Valid() {
			t.Errorf("expected %q to be a valid revocation reason", r)
		}
	}
	if RevocationReason("made_up_reason").Valid() {
		t.Error("expected unknown revocation reason to be invalid (default deny)")
	}
}

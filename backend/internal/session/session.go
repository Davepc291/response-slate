// Package session implements the Section 8 (Sessions) server-side session
// domain: opaque token generation/digesting, and idle/absolute expiration
// rules evaluated against injected configuration and clock values. It has
// no HTTP handler and constructs no cookie: "Do not implement cookies yet
// unless the approved contract explicitly assigns cookie construction to
// Step 9B" (it does not — Section 8 is a proposal for a later phase).
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
)

// ID is the server-generated session identifier (a database row id, never
// the token itself).
type ID int64

// TokenLength is the raw session token size in bytes before encoding.
const TokenLength = 32

// DigestLength is the SHA-256 digest size stored in place of the raw token.
const DigestLength = sha256.Size

// GenerateToken returns a fresh, high-entropy, opaque session token. The
// caller must return this value to the trusted client exactly once (never
// logged, never stored) and persist only Digest's output.
func GenerateToken() (string, error) {
	buf := make([]byte, TokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Digest returns the SHA-256 digest of a raw session token.
func Digest(rawToken string) []byte {
	sum := sha256.Sum256([]byte(rawToken))
	return sum[:]
}

// RevocationReason is exactly one of the fixed, allow-listed reasons this
// package recognizes; it must stay in lockstep with the sessions.
// revocation_reason CHECK constraint in migration 000008.
type RevocationReason string

const (
	RevokedLogout           RevocationReason = "logout"
	RevokedLogoutAll        RevocationReason = "logout_all"
	RevokedPasswordReset    RevocationReason = "password_reset"
	RevokedAccountDisabled  RevocationReason = "account_disabled"
	RevokedAccountSuspended RevocationReason = "account_suspended"
	RevokedAdminRevoke      RevocationReason = "admin_revoke"
	RevokedSuperseded       RevocationReason = "superseded"
)

var validReasons = map[RevocationReason]bool{
	RevokedLogout: true, RevokedLogoutAll: true, RevokedPasswordReset: true,
	RevokedAccountDisabled: true, RevokedAccountSuspended: true,
	RevokedAdminRevoke: true, RevokedSuperseded: true,
}

// Valid reports whether r is one of the allow-listed reasons.
func (r RevocationReason) Valid() bool { return validReasons[r] }

// Session is the full internal session record.
type Session struct {
	ID               ID
	UserID           int64 // identity.UserID, kept as int64 to avoid an import cycle
	TokenDigest      []byte
	DeviceHint       string
	CreatedAt        time.Time
	LastSeenAt       time.Time
	ExpiresAt        time.Time // absolute lifetime ceiling
	RevokedAt        *time.Time
	RevocationReason RevocationReason
}

// Config is the caller-supplied idle/absolute timeout policy (Section 8:
// "exact values are an unresolved question"). This package assumes no
// default of its own; a caller (or a test) must construct one explicitly.
type Config struct {
	IdleTimeout      time.Duration
	AbsoluteLifetime time.Duration
}

// ErrInvalidConfig marks a Config with a non-positive duration: picking an
// implicit default here would silently choose an operational value the
// contract leaves open.
var ErrInvalidConfig = errors.New("session: idle timeout and absolute lifetime must be positive, explicitly chosen durations")

// Validate rejects a non-positive duration.
func (c Config) Validate() error {
	if c.IdleTimeout <= 0 || c.AbsoluteLifetime <= 0 {
		return ErrInvalidConfig
	}
	return nil
}

// NewExpiresAt computes the absolute expiration for a session created at
// createdAt under this Config.
func (c Config) NewExpiresAt(createdAt time.Time) time.Time {
	return createdAt.Add(c.AbsoluteLifetime)
}

// IdleExpired reports whether s has been idle longer than Config allows, as
// of now.
func (c Config) IdleExpired(s Session, now time.Time) bool {
	return now.Sub(s.LastSeenAt) >= c.IdleTimeout
}

// AbsoluteExpired reports whether s is past its absolute lifetime ceiling,
// as of now.
func AbsoluteExpired(s Session, now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// Usable reports whether s currently authorizes access: not revoked, not
// idle-expired, and not absolute-expired. This is necessary but not
// sufficient for authorizing a protected request — the owning account's
// current state (Section 1, Section 8: "Sessions for suspended, disabled,
// expired, or password-change-required accounts cannot authorize protected
// access") must always be re-checked separately and additionally, since a
// session record alone never proves the account is still eligible.
func (c Config) Usable(s Session, now time.Time) bool {
	return s.RevokedAt == nil && !c.IdleExpired(s, now) && !AbsoluteExpired(s, now)
}

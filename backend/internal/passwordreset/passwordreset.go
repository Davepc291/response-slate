// Package passwordreset implements the Section 4 (Password reset) single-use
// reset-token domain: generation, digesting, and status/expiration rules.
// It mirrors the invitation package's token model exactly, since Section 4
// explicitly follows "the same pattern as the invitation token." It has no
// persistence and no email-delivery code.
package passwordreset

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
)

// Status is exactly one of the four values this package recognizes.
type Status string

const (
	StatusPending    Status = "pending"
	StatusRedeemed   Status = "redeemed"
	StatusExpired    Status = "expired"
	StatusSuperseded Status = "superseded"
)

// TokenLength is the raw token size in bytes before encoding.
const TokenLength = 32

// DigestLength is the SHA-256 digest size stored in place of the raw token.
const DigestLength = sha256.Size

// GenerateToken returns a fresh, high-entropy, URL-safe raw token. The
// caller must return this value to the trusted recipient exactly once and
// never persist or log it; only Digest's output is ever stored.
func GenerateToken() (string, error) {
	buf := make([]byte, TokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Digest returns the SHA-256 digest of a raw token.
func Digest(rawToken string) []byte {
	sum := sha256.Sum256([]byte(rawToken))
	return sum[:]
}

// ErrNotFoundOrUsed covers "no such token," "already redeemed," and
// "superseded" identically, matching the login flow's identical treatment
// of an unknown/reused credential (Section 3, Section 4). ErrExpired is
// returned distinctly only because callers holding a reset link may need to
// prompt the user to request a new one; it must never be used to confirm
// whether an email address has an account (the request step's response is
// always the same regardless of match, per Section 3/4).
var (
	ErrNotFoundOrUsed = errors.New("passwordreset: token not found or already used")
	ErrExpired        = errors.New("passwordreset: token has expired")
)

// Expired reports whether a reset token with this expiry is past due at t.
func Expired(expiresAt, t time.Time) bool { return !t.Before(expiresAt) }

// Package invitation implements the Section 2 (First-time login) single-use
// invitation-token domain: generation, digesting, and status/expiration
// rules. It has no persistence and no email-delivery code: "This contract
// does not select a specific delivery channel or provider," and Step 9B
// does not implement one either.
package invitation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
)

// Status is exactly one of the four values this package recognizes. There
// is no "active"/"used" free-form string.
type Status string

const (
	StatusPending    Status = "pending"
	StatusRedeemed   Status = "redeemed"
	StatusExpired    Status = "expired"
	StatusSuperseded Status = "superseded"
)

// TokenLength is the raw token size in bytes before encoding: 256 bits of
// crypto/rand entropy, matching Section 2's "high-entropy, server-generated,
// random token."
const TokenLength = 32

// DigestLength is the SHA-256 digest size stored in place of the raw token.
const DigestLength = sha256.Size

// GenerateToken returns a fresh, high-entropy, URL-safe raw token. The
// caller (identitystore, via a service) must return this value to the
// trusted administrator exactly once and never persist or log it; only
// Digest's output is ever stored.
func GenerateToken() (string, error) {
	buf := make([]byte, TokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Digest returns the SHA-256 digest of a raw token, exactly what
// migration 000008's invitations.token_digest column stores.
func Digest(rawToken string) []byte {
	sum := sha256.Sum256([]byte(rawToken))
	return sum[:]
}

// Errors returned by redemption. Per Section 2's account-enumeration
// resistance rule, ErrNotFoundOrUsed covers both "no such token" and "token
// already redeemed/superseded" identically — a caller must render both
// identically to an unknown token, never distinguishing them. ErrExpired is
// the one deliberate, narrow exception (Section 3: "Expired invite"): the
// caller already holds a link naming their own pending invitation, so
// confirming expiration to that caller leaks nothing to an outside
// attacker.
var (
	ErrNotFoundOrUsed = errors.New("invitation: token not found or already used")
	ErrExpired        = errors.New("invitation: token has expired")
)

// Expired reports whether an invitation with this expiry is past due at t.
func Expired(expiresAt, t time.Time) bool { return !t.Before(expiresAt) }

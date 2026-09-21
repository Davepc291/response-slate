// Package authcsrf implements the Step 9C CSRF defense approved by
// docs/authentication-authorization-v1.md Section 8 ("state-changing
// requests require a CSRF defense appropriate to a cookie-based session").
// It is a signed double-submit design: the CSRF value is an HMAC of the
// current session's token digest under a server-held secret, so it is
// cryptographically tied to the session (never a bare random value an
// attacker could self-generate) without requiring a new database column or
// migration — the value is derived, never stored. Every comparison is
// constant-time.
package authcsrf

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
)

// MinSecretLength is the minimum accepted server secret length in bytes
// (256 bits), enforced at construction so a weak or placeholder secret
// fails startup rather than silently weakening every derived CSRF token.
const MinSecretLength = 32

// ErrSecretTooShort marks a secret shorter than MinSecretLength.
var ErrSecretTooShort = errors.New("authcsrf: secret must be at least 32 bytes")

// Deriver derives and verifies CSRF tokens bound to a session token.
type Deriver struct {
	secret []byte
}

// NewDeriver validates secret and returns a Deriver. The secret must never
// be logged; callers must source it only from process environment, never a
// default.
func NewDeriver(secret []byte) (Deriver, error) {
	if len(secret) < MinSecretLength {
		return Deriver{}, ErrSecretTooShort
	}
	cloned := make([]byte, len(secret))
	copy(cloned, secret)
	return Deriver{secret: cloned}, nil
}

// Derive returns the CSRF token for a session identified by
// sessionTokenDigest (the same SHA-256 digest already computed for session
// lookup, never the raw session token). The result is safe to place in a
// JavaScript-readable cookie: it reveals nothing about the session token
// itself (HMAC is one-way) and is useless without the server secret.
func (d Deriver) Derive(sessionTokenDigest []byte) string {
	mac := hmac.New(sha256.New, d.secret)
	mac.Write(sessionTokenDigest)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify reports whether presented is the correct CSRF token for the
// session identified by sessionTokenDigest, using a constant-time
// comparison throughout.
func (d Deriver) Verify(sessionTokenDigest []byte, presented string) bool {
	if presented == "" {
		return false
	}
	expected := d.Derive(sessionTokenDigest)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}

// Package passwordpolicy implements the Section 4 password-hashing and
// length-policy rules approved by
// docs/authentication-authorization-v1.md: a modern, length-based policy
// with no forced composition rules, Argon2id hashing via the established
// golang.org/x/crypto/argon2 package (no invented cryptography), a
// versioned PHC-style encoded hash, per-password random salts, constant-time
// verification, and rehash-on-parameter-change detection.
//
// Breached-password checking (Section 4) requires a specific, not-yet-
// selected third-party corpus/provider (Section 15, item 2 restates the
// same open delivery-provider question for a different channel). Selecting
// and integrating that provider is out of Step 9B's scope, matching the
// prohibition on selecting an external identity provider. This package
// therefore exposes BreachChecker as a provider-neutral seam and ships only
// a NoOpBreachChecker: breach checking remains an explicitly unresolved,
// blocked decision, never silently implemented against an unapproved
// third-party service.
package passwordpolicy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Policy is the Section 4 length-based requirement: a configurable minimum
// (proposed at 12) and a generous maximum (proposed at least 256) so
// passphrases are never penalized, with no forced
// uppercase/lowercase/digit/symbol composition rule.
type Policy struct {
	MinLength int
	MaxLength int
}

// DefaultPolicy matches Section 4's proposed values exactly.
func DefaultPolicy() Policy {
	return Policy{MinLength: 12, MaxLength: 256}
}

// ErrPasswordTooShort and ErrPasswordTooLong are safe to display to an
// already-authenticated user establishing or changing their own password
// (Section 4 permits a specific message at that point, distinct from the
// generic sign-in failure text). ErrPasswordInvalidEncoding rejects a
// password that is not valid UTF-8 text.
var (
	ErrPasswordTooShort        = errors.New("passwordpolicy: password is shorter than the minimum length")
	ErrPasswordTooLong         = errors.New("passwordpolicy: password exceeds the maximum length")
	ErrPasswordInvalidEncoding = errors.New("passwordpolicy: password is not valid UTF-8 text")
)

// Validate enforces only length, per Section 4's explicit rejection of
// "arbitrary forced complexity." It counts Unicode code points, not bytes,
// so multi-byte passphrases are not penalized relative to ASCII ones.
func (p Policy) Validate(password string) error {
	if !utf8.ValidString(password) {
		return ErrPasswordInvalidEncoding
	}
	n := utf8.RuneCountInString(password)
	if n < p.MinLength {
		return ErrPasswordTooShort
	}
	if n > p.MaxLength {
		return ErrPasswordTooLong
	}
	return nil
}

// BreachChecker checks a password against a breached-password corpus,
// intended to be implemented via a k-anonymity range query so the full
// password or its full hash is never sent to a third party (Section 4).
type BreachChecker interface {
	// IsBreached reports whether password appears in a known-breach corpus.
	// The password itself must never be logged or otherwise persisted by an
	// implementation of this interface.
	IsBreached(ctx context.Context, password string) (bool, error)
}

// NoOpBreachChecker always reports "not breached." It exists only so
// calling code has a concrete, safe default while breach-check provider
// selection remains an unresolved contract decision (Section 15, item 2
// analog); it is not a security claim of any kind.
type NoOpBreachChecker struct{}

func (NoOpBreachChecker) IsBreached(context.Context, string) (bool, error) { return false, nil }

// Params are the Argon2id work-factor parameters, stored alongside every
// hash (Section 4: "so the work factor can be upgraded for future logins
// without forcing an immediate mass reset"). Memory is in KiB.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams are reasonable current-generation values (64 MiB, 3
// iterations, 2 lanes), reviewable and upgradable over time per Section 4;
// they are not claimed to be a permanently correct choice.
func DefaultParams() Params {
	return Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32}
}

const argon2Version = argon2.Version

// ErrMalformedHash marks a stored hash that does not parse as this
// package's own PHC-style encoding. It is returned instead of panicking so
// a corrupted or foreign-format row fails closed, safely, as a verification
// failure rather than a crash.
var ErrMalformedHash = errors.New("passwordpolicy: malformed or unsupported password hash")

// phcPattern matches this package's exact encoding:
// $argon2id$v=<version>$m=<memory>,t=<iterations>,p=<parallelism>$<salt>$<hash>
// It must stay in lockstep with the users.password_hash CHECK constraint in
// migration 000008.
var phcPattern = regexp.MustCompile(`^\$argon2id\$v=(\d+)\$m=(\d+),t=(\d+),p=(\d+)\$([A-Za-z0-9+/]+=*)\$([A-Za-z0-9+/]+=*)$`)

// Hash derives a versioned, PHC-encoded Argon2id hash for password using a
// fresh, unique, crypto/rand-generated salt. It never reuses a salt across
// calls and never logs or returns the raw password.
func Hash(password string, params Params) (string, error) {
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("passwordpolicy: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	return encode(params, salt, key), nil
}

func encode(params Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version, params.Memory, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// decoded is a parsed PHC-encoded hash: parameters, salt, and derived key.
type decoded struct {
	params Params
	salt   []byte
	key    []byte
}

func decode(encoded string) (decoded, error) {
	m := phcPattern.FindStringSubmatch(encoded)
	if m == nil {
		return decoded{}, ErrMalformedHash
	}
	version, err1 := strconv.Atoi(m[1])
	memory, err2 := strconv.ParseUint(m[2], 10, 32)
	iterations, err3 := strconv.ParseUint(m[3], 10, 32)
	parallelism, err4 := strconv.ParseUint(m[4], 10, 8)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || version != argon2Version {
		return decoded{}, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(m[5], "="))
	if err != nil {
		return decoded{}, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(m[6], "="))
	if err != nil {
		return decoded{}, ErrMalformedHash
	}
	if len(salt) == 0 || len(key) == 0 {
		return decoded{}, ErrMalformedHash
	}
	return decoded{
		params: Params{
			Memory:      uint32(memory),
			Iterations:  uint32(iterations),
			Parallelism: uint8(parallelism),
			SaltLength:  uint32(len(salt)),
			KeyLength:   uint32(len(key)),
		},
		salt: salt,
		key:  key,
	}, nil
}

// Verify reports whether password matches encoded, using a constant-time
// comparison of the derived keys. A malformed hash fails closed (false,
// ErrMalformedHash) rather than panicking; the caller must treat that
// identically to "wrong password" for any user-facing response, per
// Section 3's account-enumeration resistance (no distinguishing detail).
func Verify(encoded, password string) (bool, error) {
	d, err := decode(encoded)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), d.salt, d.params.Iterations, d.params.Memory, d.params.Parallelism, uint32(len(d.key)))
	return subtle.ConstantTimeCompare(candidate, d.key) == 1, nil
}

// NeedsRehash reports whether encoded was produced with parameters weaker
// than current, so a successful login can trigger a transparent rehash
// without forcing an immediate mass reset (Section 4). A malformed hash
// always needs rehashing (fails safe toward re-establishing a good hash),
// but the caller must still have verified the password first.
func NeedsRehash(encoded string, current Params) bool {
	d, err := decode(encoded)
	if err != nil {
		return true
	}
	return d.params.Memory < current.Memory ||
		d.params.Iterations < current.Iterations ||
		d.params.Parallelism < current.Parallelism ||
		d.params.KeyLength < current.KeyLength
}

// Package authconfig is the Step 9C explicit, validated configuration
// surface for the authentication HTTP API, mirroring this repository's
// existing per-subsystem Options+Validate convention (recordings,
// audioanalysis, transcription, operations). Every duration, secret, and
// origin the approved contract leaves as a caller-supplied, unresolved
// policy value (docs/authentication-authorization-v1.md Section 15) must be
// explicitly configured when authentication is enabled: this package
// chooses no implicit default for any of them, and Validate fails startup
// rather than silently falling back to an insecure or arbitrary value.
package authconfig

import (
	"errors"
	"net"
	"net/url"
	"time"

	"greenwich-fire-responder/backend/internal/authcsrf"
	"greenwich-fire-responder/backend/internal/authratelimit"
)

// RateLimit is one bounded threshold (see authratelimit.Options, which this
// mirrors exactly so config loading stays independent of that package's
// import).
type RateLimit struct {
	MaxAttempts int
	Window      time.Duration
}

func (r RateLimit) Validate() error {
	if r.MaxAttempts <= 0 || r.Window <= 0 {
		return errors.New("authconfig: rate limit max attempts and window must be positive")
	}
	return nil
}

func (r RateLimit) ToLimiterOptions() authratelimit.Options {
	return authratelimit.Options{MaxAttempts: r.MaxAttempts, Window: r.Window}
}

// Options is the full, explicit authentication configuration.
type Options struct {
	// Enabled gates whether the authentication HTTP API is registered at
	// all. When false, every other field is ignored and unvalidated: an
	// existing deployment that has not opted in is completely unaffected.
	Enabled bool

	// SessionIdleTimeout and SessionMaxLifetime are Section 8's idle and
	// absolute session timeouts (Section 15, item 6: exact values left
	// unresolved by the contract; this deployment's operator must choose
	// them explicitly).
	SessionIdleTimeout time.Duration
	SessionMaxLifetime time.Duration

	// PasswordResetTTL is Section 4's reset-token expiration window.
	PasswordResetTTL time.Duration

	// InvitationTTL is Section 2's invitation-token expiration window,
	// applied by Step 9E's administrator user-management service to
	// initial issuance, resend, and administrator reset alike. Sourced from
	// the contract's own reserved GFR_AUTH_INVITATION_TTL variable
	// (Section 11.5); this package picks no implicit default.
	InvitationTTL time.Duration

	// CSRFSecret derives the Step 9C signed double-submit CSRF token (see
	// authcsrf). It is sourced from the contract's own reserved
	// GFR_AUTH_SESSION_SECRET variable, repurposed: Step 9B's session
	// design is a fully opaque, database-verified token requiring no
	// signing key of its own, so the one server secret Section 11.5
	// reserves for "session-signing/encryption" is used here instead, for
	// the one place this phase actually needs a server-held secret. See
	// this package's godoc and the Step 9C implementation report for the
	// full rationale.
	CSRFSecret []byte

	// AllowedOrigins is the exact set of Origin header values a
	// state-changing authenticated request may present (Section 8: CSRF
	// defense "appropriate to a cookie-based session"). Not named by the
	// approved contract; a narrow, necessary Step 9C addition.
	AllowedOrigins []string

	// TrustedProxies is the set of proxy addresses/CIDRs permitted to
	// supply a trustworthy X-Forwarded-For header (see
	// backend/internal/clientip). Empty (the default) means no proxy is
	// trusted and X-Forwarded-For is always ignored.
	TrustedProxies []*net.IPNet

	LoginRateLimitPerAccount       RateLimit
	LoginRateLimitPerIP            RateLimit
	InvitationRateLimit            RateLimit
	PasswordResetRequestRateLimit  RateLimit
	PasswordResetCompleteRateLimit RateLimit

	// BreachCheckEnabled mirrors the contract's GFR_AUTH_BREACH_CHECK_ENABLED
	// gate. Step 9B ships only passwordpolicy.NoOpBreachChecker: no breach
	// corpus provider is approved or implemented. Setting this true fails
	// validation rather than silently claiming a security control that
	// does not exist.
	BreachCheckEnabled bool
}

var (
	ErrInvalidSessionTimeouts = errors.New("authconfig: session idle timeout and max lifetime must be positive, with idle timeout not exceeding max lifetime")
	ErrInvalidResetTTL        = errors.New("authconfig: password reset TTL must be positive")
	ErrInvalidInvitationTTL   = errors.New("authconfig: invitation TTL must be positive")
	ErrInvalidOrigins         = errors.New("authconfig: at least one allowed origin (scheme://host[:port], no path) is required")
	ErrBreachCheckUnavailable = errors.New("authconfig: breach-password checking is enabled but no provider is implemented (Step 9B ships only a no-op checker); leave it disabled")
)

// Validate enforces every explicit-configuration requirement. It is a
// no-op when Enabled is false.
func (o Options) Validate() error {
	if !o.Enabled {
		return nil
	}
	if o.SessionIdleTimeout <= 0 || o.SessionMaxLifetime <= 0 || o.SessionIdleTimeout > o.SessionMaxLifetime {
		return ErrInvalidSessionTimeouts
	}
	if o.PasswordResetTTL <= 0 {
		return ErrInvalidResetTTL
	}
	if o.InvitationTTL <= 0 {
		return ErrInvalidInvitationTTL
	}
	if len(o.CSRFSecret) < authcsrf.MinSecretLength {
		return authcsrf.ErrSecretTooShort
	}
	if len(o.AllowedOrigins) == 0 {
		return ErrInvalidOrigins
	}
	for _, origin := range o.AllowedOrigins {
		if !validOrigin(origin) {
			return ErrInvalidOrigins
		}
	}
	for _, rl := range []RateLimit{
		o.LoginRateLimitPerAccount, o.LoginRateLimitPerIP, o.InvitationRateLimit,
		o.PasswordResetRequestRateLimit, o.PasswordResetCompleteRateLimit,
	} {
		if err := rl.Validate(); err != nil {
			return err
		}
	}
	if o.BreachCheckEnabled {
		return ErrBreachCheckUnavailable
	}
	return nil
}

// validOrigin requires an absolute scheme://host[:port] value with no
// path, query, or fragment, matching what a browser's Origin header
// actually looks like.
func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

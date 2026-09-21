package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"greenwich-fire-responder/backend/internal/authconfig"
	"greenwich-fire-responder/backend/internal/clientip"
)

// loadAuth reads the Step 9C GFR_AUTH_* environment variables into an
// authconfig.Options, then validates it. Every duration, secret, and
// origin the approved contract leaves unresolved (Section 15) must be
// explicitly set when GFR_AUTH_ENABLED is true: there is no fallback
// default for any of them, matching this file's own errInvalid convention
// (never wrap or echo an environment value, per config.go's existing rule).
//
// GFR_AUTH_SESSION_SECRET is read here as the CSRF-token derivation secret,
// not a session-signing key: Step 9B's session design is a fully opaque,
// database-verified token requiring no signing key of its own, so the one
// server secret docs/authentication-authorization-v1.md Section 11.5
// reserves under this name is repurposed for the one place Step 9C
// actually needs a server-held secret. See authconfig.Options.CSRFSecret's
// doc comment for the same note.
func loadAuth(o *authconfig.Options) error {
	invalid := errors.New("invalid authentication configuration")

	if v := strings.TrimSpace(os.Getenv("GFR_AUTH_ENABLED")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return invalid
		}
		o.Enabled = b
	}

	for name, target := range map[string]*time.Duration{
		"GFR_AUTH_SESSION_IDLE_TIMEOUT": &o.SessionIdleTimeout,
		"GFR_AUTH_SESSION_MAX_LIFETIME": &o.SessionMaxLifetime,
		"GFR_AUTH_PASSWORD_RESET_TTL":   &o.PasswordResetTTL,
	} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return invalid
			}
			*target = d
		}
	}

	if v := os.Getenv("GFR_AUTH_SESSION_SECRET"); v != "" {
		o.CSRFSecret = []byte(v)
	}

	if v := strings.TrimSpace(os.Getenv("GFR_AUTH_ALLOWED_ORIGINS")); v != "" {
		var origins []string
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				origins = append(origins, part)
			}
		}
		o.AllowedOrigins = origins
	}

	if v := strings.TrimSpace(os.Getenv("GFR_AUTH_TRUSTED_PROXIES")); v != "" {
		trusted, err := clientip.ParseTrustedProxies(v)
		if err != nil {
			return invalid
		}
		o.TrustedProxies = trusted
	}

	for name, target := range map[string]*authconfig.RateLimit{
		"GFR_AUTH_RATE_LIMIT_PER_ACCOUNT":             &o.LoginRateLimitPerAccount,
		"GFR_AUTH_RATE_LIMIT_PER_IP":                  &o.LoginRateLimitPerIP,
		"GFR_AUTH_RATE_LIMIT_INVITATION":              &o.InvitationRateLimit,
		"GFR_AUTH_RATE_LIMIT_PASSWORD_RESET":          &o.PasswordResetRequestRateLimit,
		"GFR_AUTH_RATE_LIMIT_PASSWORD_RESET_COMPLETE": &o.PasswordResetCompleteRateLimit,
	} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			rl, err := parseRateLimit(v)
			if err != nil {
				return invalid
			}
			*target = rl
		}
	}
	// GFR_AUTH_RATE_LIMIT_PASSWORD_RESET applies to the request stage; the
	// completion stage defaults to the same configured value unless
	// GFR_AUTH_RATE_LIMIT_PASSWORD_RESET_COMPLETE overrides it, so a single
	// variable is enough for the common case.
	if strings.TrimSpace(os.Getenv("GFR_AUTH_RATE_LIMIT_PASSWORD_RESET_COMPLETE")) == "" &&
		strings.TrimSpace(os.Getenv("GFR_AUTH_RATE_LIMIT_PASSWORD_RESET")) != "" {
		o.PasswordResetCompleteRateLimit = o.PasswordResetRequestRateLimit
	}

	if v := strings.TrimSpace(os.Getenv("GFR_AUTH_BREACH_CHECK_ENABLED")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return invalid
		}
		o.BreachCheckEnabled = b
	}

	if err := o.Validate(); err != nil {
		return err
	}
	return nil
}

// parseRateLimit accepts "<max-attempts>/<window-duration>", for example
// "5/15m".
func parseRateLimit(raw string) (authconfig.RateLimit, error) {
	idx := strings.IndexByte(raw, '/')
	if idx <= 0 || idx == len(raw)-1 {
		return authconfig.RateLimit{}, errBadRateLimitFormat
	}
	max, err := strconv.Atoi(strings.TrimSpace(raw[:idx]))
	if err != nil {
		return authconfig.RateLimit{}, errBadRateLimitFormat
	}
	window, err := time.ParseDuration(strings.TrimSpace(raw[idx+1:]))
	if err != nil {
		return authconfig.RateLimit{}, errBadRateLimitFormat
	}
	return authconfig.RateLimit{MaxAttempts: max, Window: window}, nil
}

var errBadRateLimitFormat = errors.New("invalid rate limit format, expected <max-attempts>/<window-duration>")

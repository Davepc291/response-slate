package authconfig

import (
	"testing"
	"time"
)

func validOptions() Options {
	return Options{
		Enabled:                        true,
		SessionIdleTimeout:             15 * time.Minute,
		SessionMaxLifetime:             12 * time.Hour,
		PasswordResetTTL:               time.Hour,
		CSRFSecret:                     []byte("01234567890123456789012345678901"),
		AllowedOrigins:                 []string{"https://app.example.test"},
		LoginRateLimitPerAccount:       RateLimit{MaxAttempts: 5, Window: 15 * time.Minute},
		LoginRateLimitPerIP:            RateLimit{MaxAttempts: 20, Window: 15 * time.Minute},
		InvitationRateLimit:            RateLimit{MaxAttempts: 10, Window: time.Hour},
		PasswordResetRequestRateLimit:  RateLimit{MaxAttempts: 5, Window: time.Hour},
		PasswordResetCompleteRateLimit: RateLimit{MaxAttempts: 5, Window: time.Hour},
	}
}

func TestValidateAcceptsWellFormedOptions(t *testing.T) {
	if err := validOptions().Validate(); err != nil {
		t.Fatalf("expected valid options to pass, got %v", err)
	}
}

func TestValidateNoOpWhenDisabled(t *testing.T) {
	if err := (Options{Enabled: false}).Validate(); err != nil {
		t.Fatalf("expected disabled options to always validate, got %v", err)
	}
}

func TestValidateRejectsIdleExceedingAbsolute(t *testing.T) {
	o := validOptions()
	o.SessionIdleTimeout = 13 * time.Hour
	if err := o.Validate(); err != ErrInvalidSessionTimeouts {
		t.Fatalf("expected ErrInvalidSessionTimeouts, got %v", err)
	}
}

func TestValidateRejectsShortCSRFSecret(t *testing.T) {
	o := validOptions()
	o.CSRFSecret = []byte("too-short")
	if err := o.Validate(); err == nil {
		t.Fatal("expected an error for a too-short CSRF secret")
	}
}

func TestValidateRejectsEmptyOrigins(t *testing.T) {
	o := validOptions()
	o.AllowedOrigins = nil
	if err := o.Validate(); err != ErrInvalidOrigins {
		t.Fatalf("expected ErrInvalidOrigins, got %v", err)
	}
}

func TestValidateRejectsMalformedOrigin(t *testing.T) {
	o := validOptions()
	o.AllowedOrigins = []string{"https://app.example.test/some/path"}
	if err := o.Validate(); err != ErrInvalidOrigins {
		t.Fatalf("expected ErrInvalidOrigins for an origin with a path, got %v", err)
	}
}

func TestValidateRejectsBadRateLimits(t *testing.T) {
	o := validOptions()
	o.LoginRateLimitPerIP = RateLimit{}
	if err := o.Validate(); err == nil {
		t.Fatal("expected an error for a zero-value rate limit")
	}
}

func TestValidateRejectsBreachCheckEnabled(t *testing.T) {
	o := validOptions()
	o.BreachCheckEnabled = true
	if err := o.Validate(); err != ErrBreachCheckUnavailable {
		t.Fatalf("expected ErrBreachCheckUnavailable, got %v", err)
	}
}

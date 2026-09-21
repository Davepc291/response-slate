package config_test

import (
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/config"
)

func clearAuthEnvExceptTests(t *testing.T) {
	t.Helper()
	clearRecordingEnv(t)
	t.Setenv("GFR_DATABASE_REQUIRED", "false")
}

func validAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GFR_AUTH_ENABLED", "true")
	t.Setenv("GFR_DATABASE_URL", "postgres://example.invalid/dev")
	t.Setenv("GFR_AUTH_SESSION_IDLE_TIMEOUT", "15m")
	t.Setenv("GFR_AUTH_SESSION_MAX_LIFETIME", "12h")
	t.Setenv("GFR_AUTH_PASSWORD_RESET_TTL", "1h")
	t.Setenv("GFR_AUTH_INVITATION_TTL", "24h")
	t.Setenv("GFR_AUTH_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("GFR_AUTH_ALLOWED_ORIGINS", "https://app.example.test")
	t.Setenv("GFR_AUTH_RATE_LIMIT_PER_ACCOUNT", "5/15m")
	t.Setenv("GFR_AUTH_RATE_LIMIT_PER_IP", "20/15m")
	t.Setenv("GFR_AUTH_RATE_LIMIT_INVITATION", "10/1h")
	t.Setenv("GFR_AUTH_RATE_LIMIT_PASSWORD_RESET", "5/1h")
}

func TestAuthDisabledByDefault(t *testing.T) {
	clearAuthEnvExceptTests(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Enabled {
		t.Fatal("expected authentication to be disabled by default")
	}
}

func TestAuthEnabledRequiresExplicitConfiguration(t *testing.T) {
	clearAuthEnvExceptTests(t)
	t.Setenv("GFR_AUTH_ENABLED", "true")
	t.Setenv("GFR_DATABASE_URL", "postgres://example.invalid/dev")
	// Every other required GFR_AUTH_* value is deliberately left unset.
	if _, err := config.Load(); err == nil {
		t.Fatal("expected Load to fail when auth is enabled without its required configuration")
	}
}

func TestAuthEnabledRequiresDatabaseURL(t *testing.T) {
	clearAuthEnvExceptTests(t)
	validAuthEnv(t)
	t.Setenv("GFR_DATABASE_URL", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected Load to fail when auth is enabled without a database URL")
	}
}

func TestAuthFullyConfiguredLoads(t *testing.T) {
	clearAuthEnvExceptTests(t)
	validAuthEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected valid auth configuration to load, got %v", err)
	}
	if !cfg.Auth.Enabled || cfg.Auth.SessionIdleTimeout != 15*time.Minute || cfg.Auth.SessionMaxLifetime != 12*time.Hour {
		t.Fatalf("unexpected auth config: %+v", cfg.Auth)
	}
	if len(cfg.Auth.AllowedOrigins) != 1 || cfg.Auth.AllowedOrigins[0] != "https://app.example.test" {
		t.Fatalf("unexpected allowed origins: %+v", cfg.Auth.AllowedOrigins)
	}
}

func TestAuthRejectsMalformedRateLimit(t *testing.T) {
	clearAuthEnvExceptTests(t)
	validAuthEnv(t)
	t.Setenv("GFR_AUTH_RATE_LIMIT_PER_IP", "not-a-rate-limit")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected an error for a malformed rate limit")
	}
}

func TestAuthRejectsBreachCheckEnabled(t *testing.T) {
	clearAuthEnvExceptTests(t)
	validAuthEnv(t)
	t.Setenv("GFR_AUTH_BREACH_CHECK_ENABLED", "true")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected an error since no breach-check provider is implemented")
	}
}

func TestAuthRejectsInvalidTrustedProxy(t *testing.T) {
	clearAuthEnvExceptTests(t)
	validAuthEnv(t)
	t.Setenv("GFR_AUTH_TRUSTED_PROXIES", "not-a-cidr-or-ip")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected an error for an invalid trusted proxy entry")
	}
}

func TestAuthNeverLogsSecretsInErrors(t *testing.T) {
	clearAuthEnvExceptTests(t)
	t.Setenv("GFR_AUTH_ENABLED", "true")
	t.Setenv("GFR_DATABASE_URL", "postgres://user:supersecretpassword@example.invalid/dev")
	t.Setenv("GFR_AUTH_SESSION_SECRET", "top-secret-csrf-derivation-material-value")
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an error since other required auth values are still unset")
	}
	if containsSubstring(err.Error(), "supersecretpassword") || containsSubstring(err.Error(), "top-secret-csrf-derivation-material-value") {
		t.Fatalf("error message leaked a secret: %v", err)
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

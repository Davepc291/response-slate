package main

// Explicitly opted-in verification only, mirroring live_test.go's
// convention: skipped unless GFR_IDENTITY_LIVE_TEST=true and
// GFR_DATABASE_URL name a local PostgreSQL instance with migration 000008
// already applied. This test creates no user, no invitation, and no
// credential of any kind — it only confirms the authentication HTTP API is
// reachable end-to-end once wired, and that health/ready remain correct
// alongside it.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/config"
)

func TestAuthEnabledEndToEndWiring(t *testing.T) {
	if os.Getenv("GFR_IDENTITY_LIVE_TEST") != "true" {
		t.Skip("explicit local database opt-in required (set GFR_IDENTITY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	dbURL := os.Getenv("GFR_DATABASE_URL")
	if dbURL == "" {
		t.Fatal("GFR_DATABASE_URL is required for this opt-in test")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	cfg := config.Config{
		HTTPAddr:         addr,
		DatabaseURL:      dbURL,
		DatabaseRequired: true,
	}
	cfg.Auth.Enabled = true
	cfg.Auth.SessionIdleTimeout = 15 * time.Minute
	cfg.Auth.SessionMaxLifetime = 12 * time.Hour
	cfg.Auth.PasswordResetTTL = time.Hour
	cfg.Auth.CSRFSecret = []byte("01234567890123456789012345678901")
	cfg.Auth.AllowedOrigins = []string{"https://app.example.test"}
	cfg.Auth.LoginRateLimitPerAccount.MaxAttempts, cfg.Auth.LoginRateLimitPerAccount.Window = 5, 15*time.Minute
	cfg.Auth.LoginRateLimitPerIP.MaxAttempts, cfg.Auth.LoginRateLimitPerIP.Window = 20, 15*time.Minute
	cfg.Auth.InvitationRateLimit.MaxAttempts, cfg.Auth.InvitationRateLimit.Window = 10, time.Hour
	cfg.Auth.PasswordResetRequestRateLimit.MaxAttempts, cfg.Auth.PasswordResetRequestRateLimit.Window = 5, time.Hour
	cfg.Auth.PasswordResetCompleteRateLimit.MaxAttempts, cfg.Auth.PasswordResetCompleteRateLimit.Window = 5, time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithConfig(ctx, cfg, nil, slog.New(slog.NewJSONHandler(logDiscard{}, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("server did not shut down in time")
		}
	})

	client := &http.Client{Timeout: 5 * time.Second}
	waitFor := func(path string) *http.Response {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get("http://" + addr + path)
			if err == nil {
				return resp
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", path)
		return nil
	}

	ready := waitFor("/api/ready")
	defer ready.Body.Close()
	if ready.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/ready 200 with a working database, got %d", ready.StatusCode)
	}

	health := waitFor("/api/health")
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/health 200, got %d", health.StatusCode)
	}

	// A well-formed but non-matching login request must be reachable and
	// return the generic, enumeration-resistant failure — never 404 (route
	// missing) and never a raw database/internal error.
	loginReq, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/auth/login",
		strings.NewReader(`{"email":"no-such-synthetic-account@example.test","password":"whatever-value"}`))
	if err != nil {
		t.Fatal(err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "https://app.example.test")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a non-matching login, got %d", loginResp.StatusCode)
	}

	// This test creates no rows, so there is nothing to clean up in the
	// database afterward.
}

type logDiscard struct{}

func (logDiscard) Write(p []byte) (int, error) { return len(p), nil }

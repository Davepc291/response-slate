package main

// Deterministic, no-database wiring coverage: confirms /api/health and
// /api/ready behave exactly as before Step 9C when authentication is
// disabled (the default), and that no authentication route exists at all
// in that state — the opposite, auth-enabled case is covered by the
// explicit local-database opt-in test in auth_live_test.go, since
// buildAuthHandlers requires a real PostgreSQL connection.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/config"
)

func TestHealthAndReadyPreservedWithAuthDisabled(t *testing.T) {
	cfg := config.Config{HTTPAddr: "127.0.0.1:0"}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	cfg.HTTPAddr = addr

	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runWithConfig(ctx, cfg, nil, slog.New(slog.NewJSONHandler(&logs, nil))) }()
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
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get("http://" + addr + path)
			if err == nil {
				return resp
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", path)
		return nil
	}

	health := waitFor("/api/health")
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/health 200, got %d", health.StatusCode)
	}
	body, _ := io.ReadAll(health.Body)
	var healthResp struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal(body, &healthResp); err != nil || healthResp.Status != "ok" {
		t.Fatalf("unexpected health body: %s", body)
	}

	ready := waitFor("/api/ready")
	defer ready.Body.Close()
	// No database configured: ready reports not_ready/unavailable, exactly
	// as before Step 9C.
	if ready.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected /api/ready 503 with no database, got %d", ready.StatusCode)
	}

	// No authentication route exists when auth is disabled.
	loginResp, err := client.Post("http://"+addr+"/api/auth/login", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /api/auth/login to be absent (404) when auth is disabled, got %d", loginResp.StatusCode)
	}
}

func TestRunWithConfigFailsFastOnInvalidAuthConfig(t *testing.T) {
	cfg := config.Config{HTTPAddr: "127.0.0.1:0", DatabaseURL: "postgres://example.invalid/dev"}
	cfg.Auth.Enabled = true // every other required Auth field left zero-valued/invalid
	err := runWithConfig(context.Background(), cfg, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected an error for invalid auth configuration rather than an insecure default")
	}
}

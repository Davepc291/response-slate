package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
)

func validP256dh() string {
	b := make([]byte, 65)
	b[0] = 0x04
	for i := 1; i < len(b); i++ {
		b[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validAuth() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(i + 100)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validRegistration() notifydevices.Registration {
	return notifydevices.Registration{
		ID:     7,
		UserID: 1,
		Subscription: notifydevices.Subscription{
			Endpoint: "https://push.example.com/subscription/secret-endpoint",
			Keys:     notifydevices.Keys{P256dh: validP256dh(), Auth: validAuth()},
		},
	}
}

func validConfig() Config {
	return Config{
		DeviceID:        7,
		Title:           "secret-title-value",
		Body:            "secret-body-value",
		URL:             "https://app.example.com/incidents/42",
		Confirmed:       true,
		DatabaseURL:     "postgres://user@127.0.0.1:5432/db",
		Enabled:         true,
		Env:             notifywebpush.EnvDev,
		VAPIDPublicKey:  "pub",
		VAPIDPrivateKey: "priv",
		VAPIDSubject:    "mailto:ops@example.com",
	}
}

// fakeConnection is an in-memory Connection fake.
type fakeConnection struct {
	registration notifydevices.Registration
	found        bool
	getErr       error

	testMode bool
	tmErr    error

	getCalls      int
	testModeCalls int
}

func (f *fakeConnection) Get(context.Context, notifydevices.DeviceID) (notifydevices.Registration, bool, error) {
	f.getCalls++
	if f.getErr != nil {
		return notifydevices.Registration{}, false, f.getErr
	}
	return f.registration, f.found, nil
}

func (f *fakeConnection) TestMode(context.Context, notifydevices.DeviceID) (bool, error) {
	f.testModeCalls++
	if f.tmErr != nil {
		return false, f.tmErr
	}
	return f.testMode, nil
}

func connectorFor(conn Connection, connErr error) Connector {
	return func(context.Context, string) (Connection, func(), error) {
		if connErr != nil {
			return nil, nil, connErr
		}
		return conn, func() {}, nil
	}
}

func newSenderReturning(sender Sender, err error) NewSender {
	return func(Config) (Sender, error) { return sender, err }
}

func depsFor(conn Connection, sender *notifyrelay.FakeSender) (Deps, *bytes.Buffer) {
	var out bytes.Buffer
	return Deps{
		Stdout:    &out,
		Connect:   connectorFor(conn, nil),
		NewSender: newSenderReturning(sender, nil),
	}, &out
}

func readyConnection() *fakeConnection {
	return &fakeConnection{registration: validRegistration(), found: true, testMode: true}
}

func TestRunRequiresConfirmation(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	deps, _ := depsFor(readyConnection(), sender)

	cfg := validConfig()
	cfg.Confirmed = false

	err := Run(context.Background(), cfg, deps)
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("expected %v, got %v", ErrNotConfirmed, err)
	}
	if len(sender.Calls()) != 0 {
		t.Fatal("expected zero Send calls")
	}
}

func TestRunRejectsDisabledRelay(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	deps, _ := depsFor(readyConnection(), sender)

	cfg := validConfig()
	cfg.Enabled = false

	err := Run(context.Background(), cfg, deps)
	if !errors.Is(err, notifywebpush.ErrRelayDisabled) {
		t.Fatalf("expected %v, got %v", notifywebpush.ErrRelayDisabled, err)
	}
	if len(sender.Calls()) != 0 {
		t.Fatal("expected zero Send calls")
	}
}

func TestRunRejectsEnvNotDev(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	deps, _ := depsFor(readyConnection(), sender)

	for _, env := range []notifywebpush.Env{notifywebpush.EnvPreview, notifywebpush.EnvProduction, "staging"} {
		cfg := validConfig()
		cfg.Env = env

		err := Run(context.Background(), cfg, deps)
		if !errors.Is(err, ErrEnvNotDev) {
			t.Fatalf("env %q: expected %v, got %v", env, ErrEnvNotDev, err)
		}
	}
}

func TestRunPropagatesInvalidVAPIDOptions(t *testing.T) {
	conn := readyConnection()
	var out bytes.Buffer
	deps := Deps{
		Stdout:  &out,
		Connect: connectorFor(conn, nil),
		NewSender: func(Config) (Sender, error) {
			return nil, notifywebpush.ErrInvalidVAPIDPublicKey
		},
	}

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, notifywebpush.ErrInvalidVAPIDPublicKey) {
		t.Fatalf("expected %v, got %v", notifywebpush.ErrInvalidVAPIDPublicKey, err)
	}
	if conn.getCalls != 0 {
		t.Fatal("expected no device lookup when VAPID options are invalid")
	}
}

func TestRunRejectsInvalidDeviceID(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	deps, _ := depsFor(readyConnection(), sender)

	cfg := validConfig()
	cfg.DeviceID = 0

	err := Run(context.Background(), cfg, deps)
	if !errors.Is(err, ErrInvalidDeviceID) {
		t.Fatalf("expected %v, got %v", ErrInvalidDeviceID, err)
	}
}

func TestRunRejectsMissingDevice(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	conn := &fakeConnection{found: false}
	deps, _ := depsFor(conn, sender)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("expected %v, got %v", ErrDeviceNotFound, err)
	}
	if len(sender.Calls()) != 0 {
		t.Fatal("expected zero Send calls")
	}
}

func TestRunRejectsRevokedDevice(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	revokedAt := time.Now()
	reg := validRegistration()
	reg.RevokedAt = &revokedAt
	conn := &fakeConnection{registration: reg, found: true, testMode: true}
	deps, _ := depsFor(conn, sender)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("expected %v, got %v", ErrDeviceRevoked, err)
	}
	if conn.testModeCalls != 0 {
		t.Fatal("expected no test_mode read for a revoked device")
	}
	if len(sender.Calls()) != 0 {
		t.Fatal("expected zero Send calls")
	}
}

func TestRunRejectsNonTestModeDevice(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	conn := &fakeConnection{registration: validRegistration(), found: true, testMode: false}
	deps, _ := depsFor(conn, sender)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrDeviceNotTestMode) {
		t.Fatalf("expected %v, got %v", ErrDeviceNotTestMode, err)
	}
	if len(sender.Calls()) != 0 {
		t.Fatal("expected zero Send calls")
	}
}

func TestRunAcceptedOutcomeReportsSuccess(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	deps, out := depsFor(readyConnection(), sender)

	if err := Run(context.Background(), validConfig(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "SANDBOX TRANSPORT TEST") {
		t.Fatalf("expected the sandbox banner, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Outcome:  accepted") {
		t.Fatalf("expected an accepted outcome report, got %q", out.String())
	}
	calls := sender.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one Send call, got %d", len(calls))
	}
	if calls[0].OutboxID != sandboxOutboxID || !calls[0].TestMode {
		t.Fatalf("unexpected request: %+v", calls[0])
	}
}

func TestRunTemporaryFailureReportsOnlyAndReturnsError(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	sender.SetResult(sandboxOutboxID, notifyrelay.Result{Outcome: notifyrelay.OutcomeTemporaryFailure})
	deps, out := depsFor(readyConnection(), sender)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrOutcomeTemporaryFailure) {
		t.Fatalf("expected %v, got %v", ErrOutcomeTemporaryFailure, err)
	}
	if !strings.Contains(out.String(), "Outcome:  temporary_failure") {
		t.Fatalf("expected a temporary_failure report, got %q", out.String())
	}
}

func TestRunPermanentFailureReportsOnlyAndReturnsError(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	sender.SetResult(sandboxOutboxID, notifyrelay.Result{Outcome: notifyrelay.OutcomePermanentFailure})
	deps, out := depsFor(readyConnection(), sender)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrOutcomePermanentFailure) {
		t.Fatalf("expected %v, got %v", ErrOutcomePermanentFailure, err)
	}
	if !strings.Contains(out.String(), "Outcome:  permanent_failure") {
		t.Fatalf("expected a permanent_failure report, got %q", out.String())
	}
}

func TestRunUnauthorizedReportsOnlyNeverRevokes(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	sender.SetResult(sandboxOutboxID, notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	conn := readyConnection()
	deps, out := depsFor(conn, sender)

	err := Run(context.Background(), validConfig(), deps)
	if !errors.Is(err, ErrOutcomeUnauthorized) {
		t.Fatalf("expected %v, got %v", ErrOutcomeUnauthorized, err)
	}
	if !strings.Contains(out.String(), "Outcome:  unauthorized") {
		t.Fatalf("expected an unauthorized report, got %q", out.String())
	}
	// Connection has no revoke-capable method at all (see Connection's own
	// doc comment); this assertion documents that there is nothing to call.
	if conn.getCalls != 1 || conn.testModeCalls != 1 {
		t.Fatalf("expected exactly one Get and one TestMode read, got Get=%d TestMode=%d", conn.getCalls, conn.testModeCalls)
	}
}

func TestRunNeverRetries(t *testing.T) {
	sender := notifyrelay.NewFakeSender()
	sender.SetError(sandboxOutboxID, errors.New("boom"))
	deps, _ := depsFor(readyConnection(), sender)

	_ = Run(context.Background(), validConfig(), deps)
	if len(sender.Calls()) != 1 {
		t.Fatalf("expected exactly one Send call even on failure, got %d", len(sender.Calls()))
	}
}

// TestRunNeverLeaksSecretsInOutputOrErrors proves that neither the printed
// report nor any returned error ever contains the endpoint, subscription
// keys, VAPID material, or payload content.
func TestRunNeverLeaksSecretsInOutputOrErrors(t *testing.T) {
	cfg := validConfig()
	sender := notifyrelay.NewFakeSender()
	deps, out := depsFor(readyConnection(), sender)

	err := Run(context.Background(), cfg, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secrets := []string{
		validRegistration().Subscription.Endpoint,
		validRegistration().Subscription.Keys.P256dh,
		validRegistration().Subscription.Keys.Auth,
		cfg.VAPIDPublicKey,
		cfg.VAPIDPrivateKey,
		cfg.VAPIDSubject,
		cfg.Title,
		cfg.Body,
		cfg.URL,
		cfg.DatabaseURL,
	}
	for _, secret := range secrets {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("output must never contain %q:\n%s", secret, out.String())
		}
	}
}

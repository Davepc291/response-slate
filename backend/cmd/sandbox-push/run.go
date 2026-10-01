// Command sandbox-push is a standalone, local-development-only tool that
// sends exactly one manual Web Push notification to one explicitly
// test_mode=true device, for Step 8D-B Part 13's controlled sandbox push
// path. It performs exactly one send attempt, never a retry, and never
// mutates notification_devices, notification_outbox, or
// notification_deliveries: its only database access is read-only (looking
// up the one target device). It bypasses the outbox, the delivery-attempt
// planner, preference evaluation, and delivery-audit recording entirely --
// it is a direct transport smoke test, not a simulation of real alert
// delivery, and every line of its own console output says so.
//
// This tool is never imported by, registered with, or reachable from
// backend/cmd/api. It reads its own narrow set of GFR_NOTIFY_* environment
// variables directly (mirroring bootstrap-admin's own identical convention
// for GFR_DATABASE_URL) and never touches backend/internal/config.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
)

// sandboxOutboxID is a fixed, positive placeholder notifyrelay.OutboxID.
// This tool bypasses the real outbox entirely (see this file's own doc
// comment), so no real outbox row or identity exists for this send; this
// constant exists only to satisfy notifyrelay.Request's own
// OutboxID > 0 structural requirement.
const sandboxOutboxID notifyrelay.OutboxID = 1

// Config is the parsed, validated input for one sandbox-push run.
type Config struct {
	DeviceID  int64
	Title     string
	Body      string
	URL       string
	Confirmed bool

	DatabaseURL string

	Enabled         bool
	Env             notifywebpush.Env
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string
}

// Connection is the minimal, read-only database surface Run needs: Get
// (reused directly from notifydevicestore.Postgres, unmodified) for the
// registration/subscription data, and the narrow supplementary TestMode
// read this tool owns locally rather than extending notifydevicestore for
// a tool-only need. Nothing in this interface, or anywhere else in this
// file, can issue an INSERT, UPDATE, or DELETE.
type Connection interface {
	Get(ctx context.Context, id notifydevices.DeviceID) (notifydevices.Registration, bool, error)
	TestMode(ctx context.Context, id notifydevices.DeviceID) (bool, error)
}

// Connector opens (and returns a cleanup func for) the read-only database
// connection Run operates against, mirroring bootstrap-admin's own
// Connector shape exactly.
type Connector func(ctx context.Context, databaseURL string) (Connection, func(), error)

// Sender is the minimal notifyrelay.Sender surface Run needs, so tests use
// notifyrelay.FakeSender instead of a real notifywebpush.Sender -- this
// tool's own unit tests never perform real HTTP/network I/O.
type Sender interface {
	Send(ctx context.Context, req notifyrelay.Request) (notifyrelay.Result, error)
}

// NewSender constructs the real Sender for a validated Config. Injected
// (rather than calling notifywebpush.New directly in Run) purely so tests
// can substitute a notifyrelay.FakeSender without ever constructing a real
// notifywebpush.Sender or touching any network capability.
type NewSender func(cfg Config) (Sender, error)

// Deps carries every side-effecting dependency Run needs.
type Deps struct {
	Stdout    io.Writer
	Connect   Connector
	NewSender NewSender
}

var (
	// ErrNotConfirmed marks a run that omitted the required -confirm flag.
	ErrNotConfirmed = errors.New("sandbox-push: refusing to run without -confirm")
	// ErrInvalidDeviceID marks a non-positive -device-id.
	ErrInvalidDeviceID = errors.New("sandbox-push: device id must be a positive integer")
	// ErrDatabaseURLEmpty marks a run with no GFR_DATABASE_URL set.
	ErrDatabaseURLEmpty = errors.New("sandbox-push: GFR_DATABASE_URL is not set")
	// ErrEnvNotDev marks a run whose GFR_NOTIFY_ENV is not exactly "dev".
	// This is deliberately stricter than notifywebpush.Env's own
	// three-value validity (which also allows "preview"): this tool is
	// dev-sandbox-only by name and purpose, and refuses "preview" too.
	ErrEnvNotDev = errors.New("sandbox-push: GFR_NOTIFY_ENV must be exactly \"dev\" for this tool")
	// ErrDeviceNotFound marks a -device-id with no matching
	// notification_devices row.
	ErrDeviceNotFound = errors.New("sandbox-push: device not found")
	// ErrDeviceRevoked marks a device whose registration is no longer
	// active (RevokedAt is set).
	ErrDeviceRevoked = errors.New("sandbox-push: device is revoked")
	// ErrDeviceNotTestMode marks a device whose test_mode column is false.
	// This tool refuses to send to any device that is not explicitly
	// flagged test_mode=true, regardless of how it was registered.
	ErrDeviceNotTestMode = errors.New("sandbox-push: device is not marked test_mode")
	// ErrDatabaseUnavailable marks a read failure. Never wraps a raw driver
	// error.
	ErrDatabaseUnavailable = errors.New("sandbox-push: database unavailable")

	// ErrOutcomeTemporaryFailure, ErrOutcomePermanentFailure, and
	// ErrOutcomeUnauthorized mark a send that completed (no network/config
	// error) but did not result in notifyrelay.OutcomeAccepted. Run always
	// prints the full report to Deps.Stdout before returning one of these;
	// they exist only so main.go's ordinary "if err != nil, exit nonzero"
	// handling reports a nonzero exit code for a non-accepted outcome,
	// exactly as an accepted outcome's nil error reports success.
	ErrOutcomeTemporaryFailure = errors.New("sandbox-push: outcome was temporary_failure")
	ErrOutcomePermanentFailure = errors.New("sandbox-push: outcome was permanent_failure")
	ErrOutcomeUnauthorized     = errors.New("sandbox-push: outcome was unauthorized")
)

// Run performs the mandatory pre-send checks in the locked order --
// confirmation, relay enabled, env exactly dev, valid VAPID/options, device
// id positive, device exists, device active, device test_mode, request
// validates -- then calls Sender.Send exactly once, with no retry. Every
// database access it performs is a read (Get, TestMode); it never inserts,
// updates, deletes, revokes a device, or writes anything to the outbox or
// delivery-audit tables. Every line printed to deps.Stdout is prefixed by a
// "SANDBOX TRANSPORT TEST -- NOT A REAL ALERT" banner and never contains the
// endpoint, subscription keys, VAPID keys, VAPID subject, payload title/
// body/url, or a database connection string.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	if !cfg.Confirmed {
		return ErrNotConfirmed
	}
	if !cfg.Enabled {
		return notifywebpush.ErrRelayDisabled
	}
	if cfg.Env != notifywebpush.EnvDev {
		return ErrEnvNotDev
	}

	sender, err := deps.NewSender(cfg)
	if err != nil {
		return err
	}

	if cfg.DeviceID <= 0 {
		return ErrInvalidDeviceID
	}
	if cfg.DatabaseURL == "" {
		return ErrDatabaseURLEmpty
	}

	conn, cleanup, err := deps.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("sandbox-push: connect to database: %w", err)
	}
	defer cleanup()

	deviceID := notifydevices.DeviceID(cfg.DeviceID)
	registration, ok, err := conn.Get(ctx, deviceID)
	if err != nil {
		return ErrDatabaseUnavailable
	}
	if !ok {
		return ErrDeviceNotFound
	}
	if !registration.Active() {
		return ErrDeviceRevoked
	}

	testMode, err := conn.TestMode(ctx, deviceID)
	if err != nil {
		return ErrDatabaseUnavailable
	}
	if !testMode {
		return ErrDeviceNotTestMode
	}

	req := notifyrelay.Request{
		OutboxID: sandboxOutboxID,
		Endpoint: registration.Subscription.Endpoint,
		P256dh:   registration.Subscription.Keys.P256dh,
		Auth:     registration.Subscription.Keys.Auth,
		Payload:  notifyrelay.Payload{Title: cfg.Title, Body: cfg.Body, URL: cfg.URL},
		TestMode: true,
	}
	if err := req.Validate(); err != nil {
		return err
	}

	result, err := sender.Send(ctx, req)
	if err != nil {
		return err
	}

	printBanner(deps.Stdout)
	fmt.Fprintf(deps.Stdout, "  DeviceID: %d\n", cfg.DeviceID)
	fmt.Fprintf(deps.Stdout, "  Outcome:  %s\n", result.Outcome)
	if result.Code != nil {
		fmt.Fprintf(deps.Stdout, "  Code:     %s\n", *result.Code)
	}

	switch result.Outcome {
	case notifyrelay.OutcomeAccepted:
		return nil
	case notifyrelay.OutcomeTemporaryFailure:
		return ErrOutcomeTemporaryFailure
	case notifyrelay.OutcomePermanentFailure:
		return ErrOutcomePermanentFailure
	case notifyrelay.OutcomeUnauthorized:
		// Report only: this tool never revokes a device, regardless of
		// outcome (see this file's own doc comment and Connection's own
		// read-only method set, which has no revoke capability at all).
		return ErrOutcomeUnauthorized
	default:
		return fmt.Errorf("sandbox-push: unrecognized outcome %q", result.Outcome)
	}
}

func printBanner(w io.Writer) {
	fmt.Fprintln(w, "=== SANDBOX TRANSPORT TEST -- NOT A REAL ALERT ===")
}

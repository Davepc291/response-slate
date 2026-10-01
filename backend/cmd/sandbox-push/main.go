package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifydevicestore"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
)

const connectTimeout = 5 * time.Second

const usageText = `sandbox-push sends exactly one manual Web Push notification to one
explicitly test_mode=true device, for Step 8D-B Part 13's controlled
sandbox push path.

THIS IS A SANDBOX TRANSPORT TEST, NOT A REAL ALERT. LOCAL DEVELOPMENT ONLY.
This tool:
  - performs exactly one send attempt, with no retry;
  - its only database access is read-only (it never inserts, updates,
    deletes, or revokes a device, and never writes to the outbox or
    delivery-audit tables);
  - bypasses the outbox, delivery-attempt planner, preference evaluation,
    and delivery-audit recording entirely;
  - refuses to run unless the relay is explicitly enabled and
    GFR_NOTIFY_ENV is exactly "dev" (never "preview" or "production");
  - refuses to send to a device that is not found, revoked, or not marked
    test_mode=true;
  - never reports an "unauthorized" result by revoking the device -- it
    only reports the outcome;
  - never prints the device endpoint, subscription keys, VAPID keys, VAPID
    subject, payload content, or a database connection string.

Prerequisites:
  - GFR_DATABASE_URL set, pointing at a loopback PostgreSQL instance.
  - GFR_NOTIFY_RELAY_ENABLED=true, GFR_NOTIFY_ENV=dev,
    GFR_NOTIFY_VAPID_PUBLIC_KEY, GFR_NOTIFY_VAPID_PRIVATE_KEY, and
    GFR_NOTIFY_VAPID_SUBJECT all set (see vapid-keygen for generating a
    dev-only key pair).
  - A device already imported by sandbox-device, with test_mode=true.

Usage:
  sandbox-push -device-id <id> -title "..." [-body "..."] [-url "..."] -confirm

Flags:
`

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "sandbox-push:", err)
		os.Exit(2)
	}

	deps := Deps{
		Stdout:    os.Stdout,
		Connect:   realConnector,
		NewSender: realNewSender,
	}

	if err := Run(context.Background(), cfg, deps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseFlags parses args into a Config. Every GFR_NOTIFY_*/GFR_DATABASE_URL
// value is read from the process environment (via getenv, injected for
// tests), matching bootstrap-admin's own identical convention exactly; this
// tool never touches backend/internal/config.
func parseFlags(args []string, getenv func(string) string, usageOut io.Writer) (Config, error) {
	fs := flag.NewFlagSet("sandbox-push", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	fs.Usage = func() {
		fmt.Fprint(usageOut, usageText)
		fs.PrintDefaults()
	}

	deviceID := fs.Int64("device-id", 0, "Target device id; must already exist, be active, and be test_mode=true (required).")
	title := fs.String("title", "", "Notification title (required).")
	body := fs.String("body", "", "Notification body (optional).")
	url := fs.String("url", "", "Notification click-through URL (optional).")
	confirm := fs.Bool("confirm", false, "Required. Confirms you intend to send one real sandbox Web Push notification.")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	enabled, _ := strconv.ParseBool(getenv("GFR_NOTIFY_RELAY_ENABLED"))

	return Config{
		DeviceID:  *deviceID,
		Title:     *title,
		Body:      *body,
		URL:       *url,
		Confirmed: *confirm,

		DatabaseURL: getenv("GFR_DATABASE_URL"),

		Enabled:         enabled,
		Env:             notifywebpush.Env(getenv("GFR_NOTIFY_ENV")),
		VAPIDPublicKey:  getenv("GFR_NOTIFY_VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey: getenv("GFR_NOTIFY_VAPID_PRIVATE_KEY"),
		VAPIDSubject:    getenv("GFR_NOTIFY_VAPID_SUBJECT"),
	}, nil
}

// postgresConnection adapts *notifydevicestore.Postgres (reused entirely
// unmodified) into this tool's own read-only Connection interface, adding
// only the one narrow, local, read-only TestMode query -- never extending
// notifydevicestore itself.
type postgresConnection struct{ store *notifydevicestore.Postgres }

func (c postgresConnection) Get(ctx context.Context, id notifydevices.DeviceID) (notifydevices.Registration, bool, error) {
	return c.store.Get(ctx, id)
}

func (c postgresConnection) TestMode(ctx context.Context, id notifydevices.DeviceID) (bool, error) {
	var testMode bool
	err := c.store.DB.QueryRow(ctx, `SELECT test_mode FROM notification_devices WHERE id = $1`, int64(id)).Scan(&testMode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, ErrDatabaseUnavailable
	}
	return testMode, nil
}

// realConnector opens a loopback-only connection via notifydevicestore.Open,
// which independently enforces its own host allowlist.
func realConnector(ctx context.Context, databaseURL string) (Connection, func(), error) {
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	store, cleanup, err := notifydevicestore.Open(connectCtx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	return postgresConnection{store: store}, cleanup, nil
}

// realNewSender builds a real notifywebpush.Sender from cfg, using fixed
// TTL/urgency/send-timeout defaults per Step 8D-B Part 12's own locked
// design -- this tool reads no GFR_NOTIFY_TTL/URGENCY/TIMEOUT variable, and
// none exists.
func realNewSender(cfg Config) (Sender, error) {
	options := notifywebpush.Options{
		Enabled:         cfg.Enabled,
		Env:             cfg.Env,
		VAPIDPublicKey:  cfg.VAPIDPublicKey,
		VAPIDPrivateKey: cfg.VAPIDPrivateKey,
		VAPIDSubject:    cfg.VAPIDSubject,
		TTL:             notifywebpush.DefaultTTL,
		Urgency:         notifywebpush.UrgencyNormal,
		SendTimeout:     notifywebpush.DefaultSendTimeout,
	}
	return notifywebpush.New(options, nil)
}

var _ notifyrelay.Sender = (*notifywebpush.Sender)(nil)

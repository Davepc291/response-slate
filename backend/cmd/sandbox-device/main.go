package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"greenwich-fire-responder/backend/internal/notifydevicestore"
)

const connectTimeout = 5 * time.Second

const usageText = `sandbox-device imports exactly one already-created browser
PushSubscription as a single test_mode=true notification_devices row, for
Step 8D-B Part 13's controlled sandbox push path.

LOCAL DEVELOPMENT ONLY. This tool:
  - refuses every database host except 127.0.0.1, localhost, and ::1;
  - never creates a user -- -user-id must already exist;
  - hardcodes test_mode = true in its one INSERT statement; there is no
    flag or option anywhere in this tool capable of creating a
    non-test-mode registration;
  - never sends anything (it does not import notifyrelay or notifywebpush);
  - never accepts the subscription endpoint/p256dh/auth as command-line
    flags, so they never land in shell history;
  - never prints the endpoint, key material, database URL, or the full
    subscription JSON it imported.

Prerequisites:
  - GFR_DATABASE_URL set in the process environment, pointing at a loopback
    PostgreSQL instance.
  - An already-existing user id (for example, the account created by
    bootstrap-admin).
  - A browser PushSubscription, as JSON: {"endpoint":"...","keys":
    {"p256dh":"...","auth":"..."}}, piped via stdin or saved to a file and
    passed with -file. Prefer a location outside this repository (for
    example your OS temp directory); if saved inside the repository, use a
    path already covered by .gitignore (for example under tmp/). Delete the
    file manually once the import succeeds -- this tool does not delete it
    for you.

Usage:
  sandbox-device -user-id <id> -confirm < subscription.json
  sandbox-device -user-id <id> -confirm -file subscription.json

Flags:
`

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "sandbox-device:", err)
		os.Exit(2)
	}

	deps := Deps{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Connect:  realConnector,
		OpenFile: os.ReadFile,
	}

	if err := Run(context.Background(), cfg, deps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseFlags parses args into a Config. GFR_DATABASE_URL is read from the
// process environment (via getenv, injected for tests), matching
// bootstrap-admin's own identical convention exactly.
func parseFlags(args []string, getenv func(string) string, usageOut io.Writer) (Config, error) {
	fs := flag.NewFlagSet("sandbox-device", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	fs.Usage = func() {
		fmt.Fprint(usageOut, usageText)
		fs.PrintDefaults()
	}

	userID := fs.Int64("user-id", 0, "Existing user id to own the new device registration (required).")
	file := fs.String("file", "", "Path to a file containing the subscription JSON. Omit to read from stdin.")
	confirm := fs.Bool("confirm", false, "Required. Confirms you intend to create a test_mode=true device registration against a loopback-only PostgreSQL database.")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	return Config{
		DatabaseURL: getenv("GFR_DATABASE_URL"),
		UserID:      *userID,
		File:        *file,
		Confirmed:   *confirm,
	}, nil
}

// realConnector opens a loopback-only connection via notifydevicestore.Open,
// which independently re-enforces the same host allowlist as
// requireLoopbackDatabase. It never returns a raw driver/connection-string
// error.
func realConnector(ctx context.Context, databaseURL string) (dbConn, func(), error) {
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	store, cleanup, err := notifydevicestore.Open(connectCtx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	return store.DB, cleanup, nil
}

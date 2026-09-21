package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"

	"greenwich-fire-responder/backend/internal/identitystore"
)

const connectTimeout = 5 * time.Second

const usageText = `bootstrap-admin creates exactly one active system_administrator account,
directly in the local development database, so a developer can sign in to
the Step 9C/9D/9E authentication surface for the first time.

LOCAL DEVELOPMENT ONLY. This tool:
  - refuses every database host except 127.0.0.1, localhost, and ::1;
  - refuses to run unless the users table is completely empty;
  - never accepts a password as a command-line argument or environment
    variable — it is always collected interactively with hidden input and
    confirmed a second time;
  - never prints or logs a password, password hash, token, or database
    credential.

Prerequisites:
  - Migration 000008 already applied to the target database
    (see database/README.md).
  - GFR_DATABASE_URL set in the process environment, pointing at a loopback
    PostgreSQL instance (the API itself never loads .env automatically).

Usage:
  bootstrap-admin -email <address> -display-name <name> -confirm

Flags:
`

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "bootstrap-admin:", err)
		os.Exit(2)
	}

	deps := Deps{
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		ReadPassword: realReadPassword(os.Stdin, os.Stdout),
		Connect:      realConnector,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := Run(ctx, cfg, deps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseFlags parses args into a Config. GFR_DATABASE_URL is read from the
// process environment (via getenv, injected for tests), never from a flag —
// matching this repository's existing GFR_* convention that the API and its
// tools read configuration from the process environment, not a file or an
// argument.
func parseFlags(args []string, getenv func(string) string, usageOut io.Writer) (Config, error) {
	fs := flag.NewFlagSet("bootstrap-admin", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	fs.Usage = func() {
		fmt.Fprint(usageOut, usageText)
		fs.PrintDefaults()
	}

	email := fs.String("email", "", "Normalized email address for the first system administrator (required).")
	displayName := fs.String("display-name", "", "Display name for the first system administrator (required).")
	confirm := fs.Bool("confirm", false, "Required. Confirms you intend to create the first local system administrator account against a loopback-only PostgreSQL database.")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	return Config{
		DatabaseURL: getenv("GFR_DATABASE_URL"),
		Email:       *email,
		DisplayName: *displayName,
		Confirmed:   *confirm,
	}, nil
}

// realConnector opens a loopback-only connection via identitystore.Open,
// which independently re-enforces the same host allowlist as
// requireLoopbackDatabase. It never returns a raw driver/connection-string
// error: identitystore.Open already reduces every failure to its own
// sentinel errors, so a credential embedded in databaseURL is never echoed.
func realConnector(ctx context.Context, databaseURL string) (dbConn, func(), error) {
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	store, cleanup, err := identitystore.Open(connectCtx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	return store.DB, cleanup, nil
}

// realReadPassword prompts on stdout and reads one line of hidden input
// from stdin using golang.org/x/term, which disables local echo at the
// terminal-driver level (not merely by omitting characters from output).
// It refuses to fall back to visible input when stdin is not a terminal
// (for example, when piped or redirected), because that would silently
// defeat the hidden-input requirement.
func realReadPassword(stdin *os.File, stdout io.Writer) PasswordReader {
	return func(prompt string) (string, error) {
		fmt.Fprint(stdout, prompt)
		if !term.IsTerminal(int(stdin.Fd())) {
			return "", errors.New("stdin is not a terminal; run this tool interactively so the password can be entered with hidden input")
		}
		raw, err := term.ReadPassword(int(stdin.Fd()))
		fmt.Fprintln(stdout)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
}

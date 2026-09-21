// Command bootstrap-admin is a standalone, local-development-only tool that
// creates exactly one active system_administrator account directly against
// a loopback-only PostgreSQL database, so a developer can sign in to the
// Step 9C/9D/9E authentication surface for the first time.
//
// It exists only because migration 000008 deliberately inserts zero rows
// (see database/migrations/000008_identity_foundation.sql) and every
// /api/admin/users* route requires an already-authenticated administrator
// (see backend/internal/authhttp/adminusers.go and
// backend/internal/adminservice) — there is otherwise no path to the first
// administrator account at all. This tool is never imported by, registered
// with, or reachable from backend/cmd/api: it adds no HTTP endpoint and
// changes no authorization rule. It is a one-time, out-of-band local
// provisioning step, not a production deployment mechanism.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
)

// Config is the parsed, validated input for one bootstrap-admin run. It
// deliberately has no password field: a password is never accepted as a
// command-line argument or environment variable, only collected
// interactively inside Run via Deps.ReadPassword.
type Config struct {
	DatabaseURL string
	Email       string
	DisplayName string
	Confirmed   bool
}

// dbConn is the minimal database surface Run needs. It is satisfied by
// *identitystore.Postgres's own DB field in production (see realConnector in
// main.go) and by a fake in tests, so Run's validation and control-flow
// logic can be fully unit-tested without a running PostgreSQL instance.
type dbConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Connector opens (and returns a cleanup func for) the database connection
// Run operates against. The production implementation
// (main.go's realConnector) wraps identitystore.Open, which independently
// re-enforces the identical loopback-only rule as requireLoopbackDatabase
// below at actual connection time; Run itself never dials a socket.
type Connector func(ctx context.Context, databaseURL string) (dbConn, func(), error)

// PasswordReader collects one line of hidden terminal input for prompt. A
// production implementation must never echo the typed characters and must
// never return a value any caller subsequently logs.
type PasswordReader func(prompt string) (string, error)

// Deps carries every side-effecting dependency Run needs, so tests can
// substitute fakes for the terminal and the database instead of requiring a
// real TTY or a real PostgreSQL instance.
type Deps struct {
	Stdout       io.Writer
	Stderr       io.Writer
	ReadPassword PasswordReader
	Connect      Connector
}

var (
	// ErrNotConfirmed marks a run that omitted the required -confirm flag.
	ErrNotConfirmed = errors.New("bootstrap-admin: refusing to run without -confirm")
	// ErrDatabaseURLEmpty marks a run with no GFR_DATABASE_URL set.
	ErrDatabaseURLEmpty = errors.New("bootstrap-admin: GFR_DATABASE_URL is not set")
	// ErrUsersNotEmpty marks a run against a database that already has at
	// least one account: this tool only ever bootstraps the very first one.
	ErrUsersNotEmpty = errors.New("bootstrap-admin: the users table is not empty; this tool only bootstraps the very first administrator")
	// ErrPasswordMismatch marks a run where the confirmation entry did not
	// match the first password entry.
	ErrPasswordMismatch = errors.New("bootstrap-admin: passwords do not match")
)

// requireLoopbackDatabase parses raw and rejects every host except
// 127.0.0.1, localhost, and ::1. This is a fast, network-free,
// defense-in-depth check performed before Deps.Connect is ever called;
// identitystore.Open enforces the identical rule again, independently, at
// actual connection time (belt-and-suspenders, not a single point of
// failure for a mistake this tool must never make).
func requireLoopbackDatabase(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return ErrDatabaseURLEmpty
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("bootstrap-admin: GFR_DATABASE_URL is not a valid connection URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return errors.New("bootstrap-admin: GFR_DATABASE_URL must use the postgres:// or postgresql:// scheme")
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return fmt.Errorf("bootstrap-admin: refusing non-loopback database host %q: this tool only operates against 127.0.0.1, localhost, or ::1", u.Hostname())
	}
}

// countUsers returns the number of existing rows in users. It never returns
// or logs any column value, only a count.
func countUsers(ctx context.Context, conn dbConn) (int, error) {
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("bootstrap-admin: count existing users: %w", err)
	}
	return n, nil
}

// insertSystemAdministrator inserts exactly one active system_administrator
// row with the given normalized email, display name, and PHC-encoded
// password hash. scope and created_by are left NULL: Section 1 of the
// approved contract treats NULL scope as "no scope boundary assigned...
// used by system_administrator, whose authority is deployment-wide," and
// created_by NULL matches the existing Step 9B test fixture's own note that
// "the very first administrator is provisioned once, out of band."
func insertSystemAdministrator(ctx context.Context, conn dbConn, email, displayName, passwordHash string) (int64, error) {
	var id int64
	err := conn.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, $2, 'system_administrator', 'active', $3, now()) RETURNING id`,
		email, displayName, passwordHash).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("bootstrap-admin: insert administrator: %w", err)
	}
	return id, nil
}

// Run performs one full bootstrap: validate inputs and the required
// confirmation flag, connect to a loopback-only database, refuse unless the
// users table is empty, collect and confirm a password via hidden terminal
// input, hash it with the existing passwordpolicy package, insert exactly
// one active system_administrator row, and print a success message that
// contains no password, hash, token, or database credential. Every
// rejection happens before the password prompt where possible, so a
// developer is never asked to type a password only to learn afterward that
// a precondition failed.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	if !cfg.Confirmed {
		return ErrNotConfirmed
	}

	email, err := identity.NormalizeEmail(cfg.Email)
	if err != nil {
		return fmt.Errorf("bootstrap-admin: invalid email: %w", err)
	}
	if err := identity.ValidateDisplayName(cfg.DisplayName); err != nil {
		return fmt.Errorf("bootstrap-admin: invalid display name: %w", err)
	}
	if err := requireLoopbackDatabase(cfg.DatabaseURL); err != nil {
		return err
	}

	conn, cleanup, err := deps.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("bootstrap-admin: connect to database: %w", err)
	}
	defer cleanup()

	existing, err := countUsers(ctx, conn)
	if err != nil {
		return err
	}
	if existing != 0 {
		return ErrUsersNotEmpty
	}

	password, err := deps.ReadPassword("New system administrator password: ")
	if err != nil {
		return fmt.Errorf("bootstrap-admin: read password: %w", err)
	}
	confirmPassword, err := deps.ReadPassword("Confirm password: ")
	if err != nil {
		return fmt.Errorf("bootstrap-admin: read password confirmation: %w", err)
	}
	if password != confirmPassword {
		return ErrPasswordMismatch
	}

	policy := passwordpolicy.DefaultPolicy()
	if err := policy.Validate(password); err != nil {
		return fmt.Errorf("bootstrap-admin: %w", err)
	}

	hash, err := passwordpolicy.Hash(password, passwordpolicy.DefaultParams())
	if err != nil {
		return errors.New("bootstrap-admin: hash password")
	}
	// Best-effort: Go strings are immutable and may already have been
	// copied by the runtime, so this does not guarantee erasure. It is not
	// relied upon as the control that keeps the password out of output —
	// that guarantee is that neither variable is ever passed to Stdout,
	// Stderr, or an error value anywhere in this function.
	password, confirmPassword = "", ""

	id, err := insertSystemAdministrator(ctx, conn, email, cfg.DisplayName, hash)
	if err != nil {
		return err
	}
	hash = ""

	fmt.Fprintln(deps.Stdout, "Bootstrap complete: system administrator account created.")
	fmt.Fprintf(deps.Stdout, "  ID:           %d\n", id)
	fmt.Fprintf(deps.Stdout, "  Email:        %s\n", email)
	fmt.Fprintf(deps.Stdout, "  Display name: %s\n", cfg.DisplayName)
	fmt.Fprintln(deps.Stdout, "  Role:         system_administrator")
	fmt.Fprintln(deps.Stdout, "  Status:       active")
	fmt.Fprintln(deps.Stdout, "The password you entered is not stored anywhere by this tool and cannot be shown again; record it using your normal local process.")
	return nil
}

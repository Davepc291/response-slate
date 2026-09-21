// Package identitystore is the PostgreSQL persistence layer for the Step 9B
// identity/authorization foundation (migration 000008): users, invitations,
// password_resets, and sessions. These four tables are handled together in
// one package because several required operations (invitation redemption,
// account-state changes, and session revocation) must be transactional
// across them (for example, disabling a user must revoke its sessions in
// the same transaction as the state change). It is never registered by the
// live server; a caller (a test, or a future authorized integration) must
// construct and invoke it explicitly. No HTTP handler, cookie, or route
// guard exists in this package.
package identitystore

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrInput marks a caller/validation error (malformed email, unknown
	// role, oversized display name, and so on). Safe to display; never
	// wraps a raw database error or echoes a credential.
	ErrInput = errors.New("identitystore: invalid input")
	// ErrNotFound marks a lookup that matched no row.
	ErrNotFound = errors.New("identitystore: not found")
	// ErrConflict marks a uniqueness violation (most commonly a duplicate
	// normalized_email).
	ErrConflict = errors.New("identitystore: conflict")
	// ErrInvalidTransition marks a state change that is not permitted from
	// the account's/token's current state. It never reveals which specific
	// current state blocked the change, matching the identity package's own
	// generic ErrInvalidTransition.
	ErrInvalidTransition = errors.New("identitystore: state transition not permitted")
	// ErrTokenInvalid covers "no such token" and "already used" identically
	// (Section 2/4 account-enumeration resistance): a caller must never
	// distinguish these to an end user.
	ErrTokenInvalid = errors.New("identitystore: token not found or already used")
	// ErrTokenExpired is the one deliberate, narrow exception (Section 3:
	// "Expired invite"): safe to reveal only because the caller already
	// holds a link naming their own pending token.
	ErrTokenExpired = errors.New("identitystore: token has expired")
	// ErrAccountNotActive marks an attempt to create a session, or perform
	// any action requiring CanAuthenticate, for an account that is not
	// currently active.
	ErrAccountNotActive = errors.New("identitystore: account is not active")
	// ErrUnavailable marks a database failure. Callers must fail closed.
	ErrUnavailable = errors.New("identitystore: database unavailable")
)

// Conn is the minimal pgx surface shared by *pgxpool.Pool and pgx.Tx.
type Conn interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Pool additionally supports starting a transaction.
type Pool interface {
	Conn
	Begin(context.Context) (pgx.Tx, error)
}

// Postgres is the PostgreSQL-backed store for users, invitations,
// password_resets, and sessions.
type Postgres struct{ DB Pool }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing alertstore/transcriptreview hardened Open convention: this
// package is dormant until a future authorized integration explicitly wires
// it up, and must never be pointed at a remote or production database by
// accident.
func Open(ctx context.Context, connection string) (*Postgres, func(), error) {
	u, err := url.Parse(connection)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		(u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return nil, nil, ErrUnavailable
	}
	cfg, err := pgxpool.ParseConfig(connection)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	if cfg.ConnConfig.Host != u.Hostname() || len(cfg.ConnConfig.Fallbacks) > 0 {
		if cfg.ConnConfig.Host != u.Hostname() {
			return nil, nil, ErrUnavailable
		}
		cfg.ConnConfig.Fallbacks = nil
	}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	if pool.Ping(ctx) != nil {
		pool.Close()
		return nil, nil, ErrUnavailable
	}
	return &Postgres{pool}, pool.Close, nil
}

// safeDB never returns a raw driver error: a connection string, table name,
// or constraint detail could otherwise leak across the package boundary.
func safeDB(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505": // unique_violation
			return ErrConflict
		case "23514", "23503", "22001": // check_violation, foreign_key_violation, string_data_right_truncation
			return ErrInput
		}
	}
	return ErrUnavailable
}

// rollback is a best-effort cleanup helper for the common
// "defer rollback unless committed" pattern used throughout this package.
func rollback(tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

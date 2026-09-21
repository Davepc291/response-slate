// Package identityauditstore is the PostgreSQL persistence layer for the
// identityaudit domain (migration 000008's identity_audit_log table). It
// exposes an insert-only Store: there is no update or delete capability
// through this package, matching identity_audit_log's ENABLE ALWAYS
// append-only trigger. It is never registered by the live server; a caller
// (a test, or a future authorized integration) must construct and invoke it
// explicitly.
package identityauditstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identityaudit"
)

var (
	// ErrInput marks a caller/validation error: an unknown event type, a
	// forbidden or non-allow-listed metadata key, or a malformed reason
	// code. It is safe to display; it never wraps a raw database error.
	ErrInput = errors.New("identityauditstore: invalid audit event")
	// ErrUnavailable marks a database failure. Callers must fail closed:
	// never claim an audit event was recorded when it was not.
	ErrUnavailable = errors.New("identityauditstore: database unavailable")
)

// Querier is the minimal pgx surface this package needs.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store records identity/authorization audit events. There is
// deliberately no Update, Delete, or corresponding SQL statement anywhere
// in this package.
type Store interface {
	Record(ctx context.Context, ev identityaudit.Event) (int64, error)
}

// Postgres is the PostgreSQL-backed Store.
type Postgres struct{ DB Querier }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing alertstore/transcriptreview hardened Open convention.
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
	cfg.MaxConns = 1
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

// safeDB never returns a raw driver error, which could otherwise leak a
// connection string or constraint detail across the package boundary.
func safeDB(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23514", "23503", "22001":
			return ErrInput
		}
	}
	return ErrUnavailable
}

// Record validates ev (defense in depth: identityaudit.New should already
// have validated it, but this package trusts no caller) and inserts exactly
// one row. It never accepts a password, raw token, cookie value, MFA
// secret, full notification payload, or unnecessary protected call content,
// because identityaudit.ValidateMetadata rejects any key not on that event
// type's explicit allow-list before this function ever builds a statement.
func (p *Postgres) Record(ctx context.Context, ev identityaudit.Event) (int64, error) {
	if p == nil || p.DB == nil {
		return 0, ErrUnavailable
	}
	if _, ok := allowedEventType(ev.Type); !ok {
		return 0, ErrInput
	}
	if err := identityaudit.ValidateMetadata(ev.Type, ev.Metadata); err != nil {
		return 0, ErrInput
	}
	if ev.AccountID == 0 && ev.Type != identityaudit.LoginFailure && ev.Type != identityaudit.InvitationRedemptionFailed {
		return 0, ErrInput
	}
	if ev.CreatedAt.IsZero() {
		return 0, ErrInput
	}

	metadataJSON, err := json.Marshal(ev.Metadata)
	if err != nil {
		return 0, ErrInput
	}

	var accountID, actorID *int64
	if ev.AccountID != 0 {
		v := int64(ev.AccountID)
		accountID = &v
	}
	if ev.ActorID != 0 {
		v := int64(ev.ActorID)
		actorID = &v
	}
	var reason *string
	if ev.Reason != "" {
		reason = &ev.Reason
	}

	var id int64
	row := p.DB.QueryRow(ctx, `INSERT INTO identity_audit_log (event_type, account_id, actor_id, reason, metadata, created_at)
        VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		string(ev.Type), accountID, actorID, reason, metadataJSON, ev.CreatedAt)
	if err := row.Scan(&id); err != nil {
		return 0, safeDB(err)
	}
	return id, nil
}

// allowedEventType is a small local guard so this package does not need to
// export identityaudit's internal allow-list map.
func allowedEventType(t identityaudit.EventType) (identityaudit.EventType, bool) {
	switch t {
	case identityaudit.LoginSuccess, identityaudit.LoginFailure, identityaudit.InvitationCreated,
		identityaudit.InvitationRedeemed, identityaudit.InvitationRedemptionFailed, identityaudit.InvitationExpired,
		identityaudit.PasswordResetRequested, identityaudit.PasswordResetCompleted,
		identityaudit.MFAEnrollment, identityaudit.MFARecovery, identityaudit.RoleOrScopeChange,
		identityaudit.AccountStateChange, identityaudit.ProtectedCallAccess, identityaudit.NotificationDeviceEvent,
		identityaudit.SessionCreated, identityaudit.SessionRevoked, identityaudit.AdministrativeAction:
		return t, true
	default:
		return "", false
	}
}

// Package notifydeliverystore is the PostgreSQL persistence layer for the
// Step 8D-B Part 7 notifydelivery domain model (migration
// database/migrations/000010_notification_relay_foundation.sql,
// notification_deliveries table). It is kept deliberately separate from
// backend/internal/notifydelivery itself: that package's own
// depcheck_test.go forbids importing a database driver at all, so any
// Postgres code must live here instead, mirroring the existing
// notifydevices/notifydevicestore, notifyconsent/notifyconsentstore,
// notifyprefs/notifyprefsstore, and notifyoutbox/notifyoutboxstore
// package-pair convention.
//
// This package is never registered by the live server (backend/cmd/api); a
// caller (a test, or a future authorized integration sitting behind Step 9
// authentication) must construct and invoke it explicitly. It has no HTTP
// handler, no route, no provider SDK, no notification-sending code, no
// worker/claim/lease logic, and no eligibility-evaluation logic.
//
// Only Record is implemented (Step 8D-B Part 7). This package never
// exposes an Update, Delete, Get, or List method, and never issues any SQL
// statement other than one INSERT ... RETURNING: notification_deliveries
// is hard immutable, append-only audit history (migration 000010's
// notification_deliveries_immutable trigger), and this store's own API
// surface reflects that as directly as possible, mirroring
// identityauditstore's own "there is deliberately no Update, Delete, or
// corresponding SQL statement anywhere in this package" precedent exactly.
package notifydeliverystore

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/notifydelivery"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

// Sentinel errors. There is deliberately no ErrForbidden: this table's one
// foreign key does not encode an ownership/authorization boundary the way
// notifyconsentstore.Record's or notifyoutboxstore.Enqueue's composite
// device/user FKs do -- a violation here only ever means the caller
// supplied an (outbox_id, event_id, device_id) triple that does not match
// a real, internally consistent notification_outbox row, which is a plain
// input-shaped mistake, not a cross-user access attempt. There is also
// deliberately no ErrConflict and no ErrNotFound: this package has no
// state machine (every call is an independent insert of a new historical
// fact, never a transition) and no lookup-by-id method at all.
var (
	// ErrInput marks a caller/validation error caught before any database
	// access (Delivery.Validate()), or a database-side check/format
	// violation that is not specifically the composite FK case -- which
	// also stays ErrInput (see this var block's own doc comment above).
	ErrInput = errors.New("notifydeliverystore: invalid input")
	// ErrUnavailable marks a database failure. Callers must fail closed:
	// never claim a delivery record was recorded when it was not.
	ErrUnavailable = errors.New("notifydeliverystore: database unavailable")
)

// Querier is the minimal pgx surface Record needs: a single atomic
// INSERT ... RETURNING statement, so nothing beyond QueryRow is required --
// exactly mirroring notifyconsentstore/notifyprefsstore's own minimal
// Querier.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Postgres is the PostgreSQL-backed store for notification_deliveries.
// Construct with Open, or by setting DB directly in a test.
type Postgres struct{ DB Querier }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing notifyoutboxstore/notifyprefsstore/notifyconsentstore
// hardened Open convention exactly: this package is dormant until a future
// authorized integration explicitly wires it up, and must never be pointed
// at a remote or production database by accident.
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

// safeDB never returns a raw driver error: a connection string, table name,
// or constraint detail could otherwise leak across the package boundary.
// This stays deliberately generic, exactly mirroring the sibling stores'
// own safeDB: it never special-cases 23503 (foreign_key_violation) into
// anything but the already-correct generic ErrInput -- there is no
// ownership meaning to carve out here (see this package's own var block
// doc comment for why ErrForbidden has no role in this table at all).
//
// Note: migration 000010's notification_deliveries_immutable trigger
// raises ERRCODE 55000 (object_not_in_prerequisite_state), not one of the
// codes classified below -- an UPDATE/DELETE/TRUNCATE attempt would
// therefore fail closed as the generic ErrUnavailable through this
// function. This is never actually reached through this package's own API,
// since Record never issues any statement but a single INSERT.
func safeDB(err error) error {
	if err == nil {
		return nil
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23514", "23503", "23505", "22001", "22023":
			return ErrInput
		}
	}
	return ErrUnavailable
}

// Record validates d and inserts exactly one new, immutable delivery-audit
// row (Section 17: "one row per relay delivery attempt"). It never updates
// or deletes an existing row -- there is no such method anywhere in this
// package.
//
// This is a single INSERT ... RETURNING statement. d.ID and d.CreatedAt are
// never used as input: id is always server-generated (GENERATED ALWAYS AS
// IDENTITY), and created_at is always the column's own DEFAULT now() --
// this package never calls time.Now() itself and never accepts a caller-
// supplied clock value for this table, since there is no other timestamp
// here needing coordination and no update path to make deterministic
// testing of a "now" argument meaningful. Both are read back from the
// statement's own RETURNING clause into the returned Delivery.
//
// d.AttemptNumber is used exactly as supplied: this package never derives
// it from, and never reads or modifies, notification_outbox.attempt_count
// (see notifydelivery's own doc comment for why that correspondence is
// deliberately left to a future, separately authorized component).
//
// This statement targets the migration's own composite foreign key,
// FOREIGN KEY (outbox_id, event_id, device_id) REFERENCES
// notification_outbox (id, event_id, device_id): that FK is the sole
// mechanism that enforces d's triple is internally consistent with a real
// outbox row. No redundant Go-side pre-read of notification_outbox is
// performed before this INSERT -- the FK check is already atomic and
// race-free, and a separate read-then-insert would only add a TOCTOU
// window with no additional safety. Its violation is Postgres error code
// 23503 (foreign_key_violation), which safeDB deliberately leaves generic
// (see safeDB's own doc comment): unlike notifyconsentstore.Record's or
// notifyoutboxstore.Enqueue's own composite FKs, this one does not encode
// an ownership boundary, so its violation is mapped to the plain,
// already-correct ErrInput, never ErrForbidden.
func (p *Postgres) Record(ctx context.Context, d notifydelivery.Delivery) (notifydelivery.Delivery, error) {
	if p == nil || p.DB == nil {
		return notifydelivery.Delivery{}, ErrUnavailable
	}
	if err := d.Validate(); err != nil {
		return notifydelivery.Delivery{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `INSERT INTO notification_deliveries (outbox_id, event_id, device_id, outcome, attempt_number, error_code)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, outbox_id, event_id, device_id, outcome, attempt_number, error_code, created_at`,
		int64(d.OutboxID), d.EventID, int64(d.DeviceID), string(d.Outcome), d.AttemptNumber, d.ErrorCode)

	recorded, err := scanDelivery(row)
	if err != nil {
		return notifydelivery.Delivery{}, safeDB(err)
	}
	return recorded, nil
}

// scanDelivery reads exactly the column list Record's RETURNING clause
// produces: id, outbox_id, event_id, device_id, outcome, attempt_number,
// error_code, created_at.
func scanDelivery(row pgx.Row) (notifydelivery.Delivery, error) {
	var (
		id, outboxID, deviceID int64
		eventID, outcome       string
		attemptNumber          int
		errorCode              *string
		createdAt              time.Time
	)
	if err := row.Scan(&id, &outboxID, &eventID, &deviceID, &outcome, &attemptNumber, &errorCode, &createdAt); err != nil {
		return notifydelivery.Delivery{}, err
	}
	return notifydelivery.Delivery{
		ID:            notifydelivery.DeliveryID(id),
		OutboxID:      notifyoutbox.OutboxID(outboxID),
		EventID:       eventID,
		DeviceID:      notifydevices.DeviceID(deviceID),
		Outcome:       notifydelivery.Outcome(outcome),
		AttemptNumber: attemptNumber,
		ErrorCode:     errorCode,
		CreatedAt:     createdAt,
	}, nil
}

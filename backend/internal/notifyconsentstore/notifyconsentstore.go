// Package notifyconsentstore is the PostgreSQL persistence layer for the
// Step 8D-B Part 4 notifyconsent domain model (migration
// database/migrations/000010_notification_relay_foundation.sql,
// notification_consents table). It is kept deliberately separate from
// backend/internal/notifyconsent itself: that package's own depcheck_test.go
// forbids importing a database driver at all, so any Postgres code must live
// here instead, mirroring the existing notifydevices/notifydevicestore
// package-pair convention.
//
// This package is never registered by the live server (backend/cmd/api); a
// caller (a test, or a future authorized integration sitting behind Step 9
// authentication) must construct and invoke it explicitly. It has no HTTP
// handler, no route, no provider SDK, no notification-sending code, and
// reads no GFR_NOTIFY_* environment variable.
//
// Only Record and Current are implemented (Step 8D-B Part 4). History --
// returning the full append-only sequence of consent events for a device or
// user -- is deliberately not implemented yet; that is a separate, future
// substep.
package notifyconsentstore

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyconsent"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

// Sentinel errors. There is no ErrNotFound: Record either succeeds or fails
// closed with ErrInput/ErrForbidden/ErrUnavailable, and Current expresses
// "no consent history" as its own (false, false, nil) return rather than a
// sentinel error (see Current's doc comment). None of these ever echoes a
// device's subscription endpoint, key material, or another user's identity.
var (
	// ErrInput marks a caller/validation error (non-positive user or device
	// id, zero time, invalid event) caught before any database access, or a
	// database-side check/format violation that is not the specific
	// ownership FK case Record itself translates to ErrForbidden. Safe to
	// display; never wraps a raw database error.
	ErrInput = errors.New("notifyconsentstore: invalid input")
	// ErrForbidden marks a Record call whose (deviceID, userID) pair does
	// not match any row in notification_devices -- i.e. deviceID does not
	// belong to userID (or does not exist at all). See Record's own doc
	// comment for exactly how this is derived from the database response.
	ErrForbidden = errors.New("notifyconsentstore: device does not belong to user")
	// ErrUnavailable marks a database failure. Callers must fail closed.
	ErrUnavailable = errors.New("notifyconsentstore: database unavailable")
)

// Querier is the minimal pgx surface Record/Current need: both are single
// atomic statements (one INSERT ... RETURNING, one SELECT ... LIMIT 1), so
// nothing beyond QueryRow is required for either -- unlike notifydevicestore,
// neither method here ever needs Query (a variable-length result set) or
// Begin (a multi-statement transaction), since consent is append-only and
// never reads-then-conditionally-writes the way Replace/Revoke do.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Postgres is the PostgreSQL-backed store for notification_consents.
// Construct with Open, or by setting DB directly in a test.
type Postgres struct{ DB Querier }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing notifydevicestore/alertstore/identitystore hardened Open
// convention exactly: this package is dormant until a future authorized
// integration explicitly wires it up, and must never be pointed at a remote
// or production database by accident.
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
// This stays deliberately generic, exactly mirroring notifydevicestore's own
// safeDB: it never special-cases 23503 (foreign_key_violation) into
// ErrForbidden here. The one place a composite-FK violation on
// notification_consents means "this device does not belong to this user" is
// Record itself, which derives that meaning from the shape of its own
// query (see Record's doc comment) before safeDB is ever consulted, not from
// a generic error-code classification that every other caller would also
// trip over.
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

// scanConsent reads exactly the column list Record's RETURNING clause
// produces: id, user_id, device_id, event, created_at.
func scanConsent(row pgx.Row) (notifyconsent.Consent, error) {
	var (
		id, userID, deviceID int64
		event                string
		createdAt            time.Time
	)
	if err := row.Scan(&id, &userID, &deviceID, &event, &createdAt); err != nil {
		return notifyconsent.Consent{}, err
	}
	return notifyconsent.Consent{
		ID:        id,
		UserID:    identity.UserID(userID),
		DeviceID:  notifydevices.DeviceID(deviceID),
		Event:     notifyconsent.Event(event),
		CreatedAt: createdAt,
	}, nil
}

// Record appends one new consent/revocation event for deviceID (Section 8:
// "an explicit, timestamped consent event tied to the authenticated user and
// the specific device registration"). now is the caller-supplied clock
// value; this package never calls time.Now() itself.
//
// notification_consents is append-only (its migration-level immutability
// trigger rejects any UPDATE/DELETE/TRUNCATE outright), and this method
// never deduplicates: recording the same event twice in a row (e.g.
// granted, then granted again) is not an error and is not collapsed --
// every call that passes validation and the ownership check produces
// exactly one new row. Deriving a single current consent state from this
// append-only sequence is Current's job, not this method's.
//
// event must be notifyconsent.EventGranted or notifyconsent.EventRevoked
// (Event.Valid()); anything else fails closed with ErrInput before any
// database access, matching the migration's own CHECK (event IN ('granted',
// 'revoked')) as a second, defense-in-depth line -- a caller can never reach
// the database with an invalid event value in the first place.
//
// This is a single INSERT ... RETURNING statement targeting the migration's
// own composite foreign key, FOREIGN KEY (device_id, user_id) REFERENCES
// notification_devices (id, user_id): that FK is the sole mechanism that
// enforces deviceID actually belongs to userID, not any application-level
// lookup. Its violation is Postgres error code 23503
// (foreign_key_violation), which safeDB deliberately leaves generic (see
// safeDB's own doc comment) -- so it is translated to the specific
// ErrForbidden right here, in the one place its meaning is unambiguous: this
// query has only one FK, so a 23503 here can only mean "(deviceID, userID)
// does not match any notification_devices row," never anything else.
//
// The composite FK does not require the referenced notification_devices row
// to currently be active (it has no revoked_at IS NULL condition): consent
// can legitimately be recorded against an already-revoked device -- for
// example, a revocation event racing after, or arriving to record, a device
// the user already revoked through notifydevicestore.Revoke. This is a
// deliberate property of the existing schema, not something this method
// adds or should ever "fix" by checking device activity itself.
func (p *Postgres) Record(ctx context.Context, now time.Time, userID identity.UserID, deviceID notifydevices.DeviceID, event notifyconsent.Event) (notifyconsent.Consent, error) {
	if p == nil || p.DB == nil {
		return notifyconsent.Consent{}, ErrUnavailable
	}
	if userID <= 0 || deviceID <= 0 || now.IsZero() || !event.Valid() {
		return notifyconsent.Consent{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `INSERT INTO notification_consents (user_id, device_id, event, created_at)
        VALUES ($1, $2, $3, $4)
        RETURNING id, user_id, device_id, event, created_at`,
		int64(userID), int64(deviceID), string(event), now)

	c, err := scanConsent(row)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23503" {
			return notifyconsent.Consent{}, ErrForbidden
		}
		return notifyconsent.Consent{}, safeDB(err)
	}
	return c, nil
}

// Current derives the current consent state for deviceID from the latest
// recorded event -- notification_consents has no mutable "current state"
// column of its own, only the append-only event log Record writes to, so
// this is always a derivation, never a direct read of stored state.
//
// granted is true exactly when the single most recent event (ORDER BY
// created_at DESC, id DESC, matching the same most-recent-first,
// id-as-tiebreaker convention notifydevicestore.ListActive already uses for
// deterministic ordering even when two rows share an identical created_at
// timestamp) is notifyconsent.EventGranted; it is false for
// notifyconsent.EventRevoked.
//
// ok reports whether any consent history exists for deviceID at all: a
// device with no recorded consent event -- never granted, never revoked --
// returns (false, false, nil), deliberately distinct from an explicit
// revocation's (false, true, nil). A caller that only checks the first
// return value already fails closed correctly either way (both cases read
// as "not granted"); ok exists for a caller that needs to distinguish
// "explicitly revoked" from "no decision has ever been recorded."
//
// This intentionally does not require deviceID to currently be active in
// notification_devices: Record permits consent to be recorded against an
// already-revoked device (see Record's own doc comment), so Current must be
// equally willing to report that recorded state back, not silently hide it
// behind a device-activity filter this method never had reason to add.
func (p *Postgres) Current(ctx context.Context, deviceID notifydevices.DeviceID) (bool, bool, error) {
	if p == nil || p.DB == nil {
		return false, false, ErrUnavailable
	}
	if deviceID <= 0 {
		return false, false, ErrInput
	}

	row := p.DB.QueryRow(ctx, `SELECT event FROM notification_consents WHERE device_id = $1
        ORDER BY created_at DESC, id DESC LIMIT 1`, int64(deviceID))

	var eventStr string
	if err := row.Scan(&eventStr); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, nil
		}
		return false, false, safeDB(err)
	}
	return notifyconsent.Event(eventStr) == notifyconsent.EventGranted, true, nil
}

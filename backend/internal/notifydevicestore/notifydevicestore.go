// Package notifydevicestore is the PostgreSQL persistence layer for the
// Step 8D-B notifydevices domain model (migration
// database/migrations/000010_notification_relay_foundation.sql,
// notification_devices table). It is kept deliberately separate from
// backend/internal/notifydevices itself: that package's own depcheck_test.go
// forbids importing a database driver at all, so any Postgres code must live
// here instead, mirroring the existing identity/identitystore and
// alerts/alertstore package-pair convention throughout this repository.
//
// This package is never registered by the live server (backend/cmd/api); a
// caller (a test, or a future authorized integration sitting behind Step 9
// authentication) must construct and invoke it explicitly. It has no HTTP
// handler, no route, no provider SDK, no notification-sending code, and
// reads no GFR_NOTIFY_* environment variable.
//
// Only Register and Get are implemented so far (Step 8D-B Part 3, slices 1
// and 2A). Replace, Revoke, and ListActive are deliberately not implemented
// yet.
package notifydevicestore

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

// Sentinel errors. Only the ones Register actually needs exist yet
// (mirroring alertstore's own minimalism: it defines no ErrNotFound/
// ErrConflict either, since its one method never needs them). None of these,
// nor any error this package returns, ever echoes a subscription endpoint,
// key material, or another user's identity.
var (
	// ErrInput marks a caller/validation error (invalid user id, malformed
	// subscription, invalid platform hint). Safe to display; never wraps a
	// raw database error.
	ErrInput = errors.New("notifydevicestore: invalid input")
	// ErrForbidden marks an attempt to register a subscription endpoint that
	// a different user already actively holds (Section 6: "rejected...as a
	// possible cross-user registration attempt"). See Register's own doc
	// comment for exactly how this is derived from the database response.
	ErrForbidden = errors.New("notifydevicestore: registration belongs to a different user")
	// ErrUnavailable marks a database failure. Callers must fail closed.
	ErrUnavailable = errors.New("notifydevicestore: database unavailable")
)

// errCorruptSubscription marks a scanned row whose stored p256dh/auth no
// longer decodes to a structurally valid Web Push subscription (Section 4.1
// key sizes) -- something the schema's own bytea-length CHECK constraints
// should make unreachable through this package's own write path, but a read
// must never silently hand back data it cannot itself vouch for. This is
// deliberately unexported and never returned to a caller directly: every
// caller (Register, Get) fails closed with the generic ErrUnavailable
// instead, exactly as safeDB already does for every other database
// anomaly -- a raw corruption detail is not something to expose either.
var errCorruptSubscription = errors.New("notifydevicestore: stored subscription key material failed re-validation")

// Querier is the minimal pgx surface this package needs so far: Register is
// a single atomic statement, so nothing beyond QueryRow is required. A real
// *pgxpool.Pool satisfies it; tests use a fake.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Postgres is the PostgreSQL-backed store for notification_devices.
// Construct with Open, or by setting DB directly in a test.
type Postgres struct{ DB Querier }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing alertstore/identitystore/transcriptreview hardened Open
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
// This stays deliberately generic, exactly mirroring alertstore's own
// safeDB: it never special-cases 23505 (unique_violation) into ErrForbidden
// here. The one place a unique-index conflict on notification_devices'
// active-endpoint index means "a different user already holds this
// endpoint" is Register itself, which derives that meaning from the shape
// of its own query (see Register's doc comment) before safeDB is ever
// consulted, not from a generic error-code classification that every other
// caller would also trip over.
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

// decodeSubscriptionKey decodes a base64url string, accepting both the
// unpadded encoding every mainstream browser's PushSubscription.toJSON()
// produces and the padded variant, exactly mirroring
// notifydevices.decodeB64URL's own tolerance (duplicated here as a small,
// self-contained helper rather than exported from notifydevices, so that
// package's own dependency surface stays exactly what its depcheck_test.go
// already verifies -- mirroring identitystore/mfa.go's precedent of
// duplicating a small constant/rule locally rather than coupling a
// lower-level store package to a sibling package's internals).
func decodeSubscriptionKey(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// maxPlatformLen/platformPattern duplicate notifydevices' own unexported
// validatePlatform rule for the same reason decodeSubscriptionKey duplicates
// its decode logic: this package must validate before ever reaching SQL, and
// notifydevices exports no validation function of its own to call.
const maxPlatformLen = 64

var platformPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

func validatePlatform(platform string) error {
	if platform == "" {
		return nil
	}
	if len(platform) > maxPlatformLen || !platformPattern.MatchString(platform) {
		return ErrInput
	}
	return nil
}

// Register validates sub and platform using notifydevices' own exported
// domain rules (Subscription.Validate), then creates or idempotently
// updates the caller's registration for that subscription in a single
// atomic statement (Section 6: "Re-registering the same endpoint for the
// same user updates the existing row (idempotent)"). now is the
// caller-supplied clock value; this package never calls time.Now() itself.
//
// The statement is an INSERT ... ON CONFLICT (endpoint) WHERE revoked_at IS
// NULL DO UPDATE ... WHERE notification_devices.user_id = EXCLUDED.user_id,
// targeting exactly the migration's own partial unique index
// (notification_devices_endpoint_active_idx). This lets the database itself
// resolve the three cases in one round trip, atomically, with no
// application-level transaction or row lock required:
//
//   - No active row holds this endpoint: the INSERT proceeds normally,
//     creating a fresh active registration (this is also how a previously
//     revoked/superseded endpoint becomes registrable again: the partial
//     index does not see a revoked row at all, so there is no conflict).
//   - An active row for this endpoint is owned by the same user: the
//     conflict fires, its WHERE guard matches, and the existing row is
//     updated in place (its DeviceID, and CreatedAt, are preserved; only
//     Subscription/Platform/UpdatedAt change).
//   - An active row for this endpoint is owned by a different user: the
//     conflict fires, but the WHERE guard does not match, so Postgres does
//     nothing for that row and the statement's RETURNING clause yields zero
//     rows. That is the one and only way this exact query can return zero
//     rows, so a resulting pgx.ErrNoRows is translated to ErrForbidden right
//     here -- not by safeDB, and not as a general "not found" meaning, which
//     would never make sense for an INSERT.
//
// The database's own partial unique index remains the final concurrency
// backstop regardless: two concurrent Register calls for the same new
// endpoint can never both succeed in creating two active rows, whether or
// not this package's own logic is ever wrong.
func (p *Postgres) Register(ctx context.Context, now time.Time, userID identity.UserID, sub notifydevices.Subscription, platform string) (notifydevices.Registration, error) {
	if p == nil || p.DB == nil {
		return notifydevices.Registration{}, ErrUnavailable
	}
	if userID <= 0 || now.IsZero() {
		return notifydevices.Registration{}, ErrInput
	}
	if err := sub.Validate(); err != nil {
		return notifydevices.Registration{}, ErrInput
	}
	if err := validatePlatform(platform); err != nil {
		return notifydevices.Registration{}, ErrInput
	}

	p256dh, err := decodeSubscriptionKey(sub.Keys.P256dh)
	if err != nil {
		return notifydevices.Registration{}, ErrInput
	}
	auth, err := decodeSubscriptionKey(sub.Keys.Auth)
	if err != nil {
		return notifydevices.Registration{}, ErrInput
	}

	var platformArg *string
	if platform != "" {
		platformArg = &platform
	}

	row := p.DB.QueryRow(ctx, `INSERT INTO notification_devices (user_id, endpoint, p256dh, auth, platform, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $6)
        ON CONFLICT (endpoint) WHERE revoked_at IS NULL DO UPDATE SET
            p256dh = EXCLUDED.p256dh,
            auth = EXCLUDED.auth,
            platform = EXCLUDED.platform,
            updated_at = EXCLUDED.updated_at
        WHERE notification_devices.user_id = EXCLUDED.user_id
        RETURNING id, user_id, endpoint, p256dh, auth, platform, created_at, updated_at, revoked_at, superseded_by`,
		int64(userID), sub.Endpoint, p256dh, auth, platformArg, now)

	reg, err := scanRegistration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifydevices.Registration{}, ErrForbidden
		}
		if errors.Is(err, errCorruptSubscription) {
			return notifydevices.Registration{}, ErrUnavailable
		}
		return notifydevices.Registration{}, safeDB(err)
	}
	return reg, nil
}

// Get returns the registration for id regardless of its active or revoked
// state: Get is an id lookup/history operation, never an active-only
// filter (that distinction belongs to a future ListActive). ok is false
// with a nil error exactly when no row exists for id -- mirroring
// notifydevices.Store.Get's own (Registration, bool) shape, extended with
// an error return since this database-backed variant can also fail on
// unavailability, which the in-memory store never can.
func (p *Postgres) Get(ctx context.Context, id notifydevices.DeviceID) (notifydevices.Registration, bool, error) {
	if p == nil || p.DB == nil {
		return notifydevices.Registration{}, false, ErrUnavailable
	}
	if id <= 0 {
		return notifydevices.Registration{}, false, ErrInput
	}

	row := p.DB.QueryRow(ctx, `SELECT id, user_id, endpoint, p256dh, auth, platform, created_at, updated_at, revoked_at, superseded_by
        FROM notification_devices WHERE id = $1`, int64(id))

	reg, err := scanRegistration(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifydevices.Registration{}, false, nil
		}
		if errors.Is(err, errCorruptSubscription) {
			return notifydevices.Registration{}, false, ErrUnavailable
		}
		return notifydevices.Registration{}, false, safeDB(err)
	}
	return reg, true, nil
}

// scanRegistration reads exactly the column list both Register's RETURNING
// clause and Get's SELECT produce, re-encoding the stored bytea p256dh/auth
// values back to base64url text so the result is a normal,
// immediately-usable notifydevices.Registration/Subscription value.
// Re-validating that reconstructed Subscription (rather than trusting the
// bytea CHECK constraints alone) is deliberate defense in depth: a write
// through this package can never produce an invalid one, but a read must
// never silently hand back key material it cannot itself vouch for,
// regardless of how it got into the row. Shared by every caller that reads
// a notification_devices row back (Register today; Replace/Revoke/
// ListActive will reuse it too), so this guarantee is uniform rather than
// re-implemented per method.
func scanRegistration(row pgx.Row) (notifydevices.Registration, error) {
	var (
		id, userID           int64
		endpoint             string
		p256dh, auth         []byte
		platform             *string
		createdAt, updatedAt time.Time
		revokedAt            *time.Time
		supersededBy         *int64
	)
	if err := row.Scan(&id, &userID, &endpoint, &p256dh, &auth, &platform, &createdAt, &updatedAt, &revokedAt, &supersededBy); err != nil {
		return notifydevices.Registration{}, err
	}
	reg := notifydevices.Registration{
		ID:     notifydevices.DeviceID(id),
		UserID: identity.UserID(userID),
		Subscription: notifydevices.Subscription{
			Endpoint: endpoint,
			Keys: notifydevices.Keys{
				P256dh: base64.RawURLEncoding.EncodeToString(p256dh),
				Auth:   base64.RawURLEncoding.EncodeToString(auth),
			},
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		RevokedAt: revokedAt,
	}
	if platform != nil {
		reg.Platform = *platform
	}
	if supersededBy != nil {
		reg.SupersededBy = notifydevices.DeviceID(*supersededBy)
	}
	if err := reg.Subscription.Validate(); err != nil {
		return notifydevices.Registration{}, errCorruptSubscription
	}
	return reg, nil
}

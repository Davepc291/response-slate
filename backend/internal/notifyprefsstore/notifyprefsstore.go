// Package notifyprefsstore is the PostgreSQL persistence layer for the Step
// 8D-B Part 5 notifyprefs domain model (migration
// database/migrations/000010_notification_relay_foundation.sql,
// alert_preferences table). It is kept deliberately separate from
// backend/internal/notifyprefs itself: that package's own depcheck_test.go
// forbids importing a database driver at all, so any Postgres code must live
// here instead, mirroring the existing notifydevices/notifydevicestore and
// notifyconsent/notifyconsentstore package-pair convention.
//
// This package is never registered by the live server (backend/cmd/api); a
// caller (a test, or a future authorized integration sitting behind Step 9
// authentication) must construct and invoke it explicitly. It has no HTTP
// handler, no route, no provider SDK, no notification-sending code, no
// preference-evaluation/quiet-hours-suppression logic (that belongs to the
// separately named, not-yet-built notifypreferences package -- see
// notifyprefs' own doc comment for why this package is deliberately not
// named that), and reads no GFR_NOTIFY_* environment variable.
//
// Only Get and Upsert are implemented (Step 8D-B Part 5), matching
// alert_preferences' own shape exactly: one mutable current row per user,
// no history, full-replace semantics on every write.
package notifyprefsstore

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyprefs"
)

// Sentinel errors. There is deliberately no ErrForbidden and no ErrNotFound:
// alert_preferences is keyed directly on user_id (the primary key IS the
// owner), so unlike notifydevicestore/notifyconsentstore there is no
// separate owned-resource id to check ownership against -- a caller only
// ever operates on exactly one row, its own. "No row yet" is expressed by
// Get's own (Preferences, bool, error) shape, not a sentinel error (see
// Get's doc comment). None of these ever echoes a caller-supplied value.
var (
	// ErrInput marks a caller/validation error caught before any database
	// access, or a database-side check/foreign-key/format violation. A
	// foreign-key violation here only ever means "user_id does not
	// reference a real users row" -- there is no ownership meaning to
	// translate locally the way notifyconsentstore.Record must for its own
	// (device_id, user_id) composite FK, so safeDB's already-generic 23503
	// classification is exactly correct here with no local override.
	ErrInput = errors.New("notifyprefsstore: invalid input")
	// ErrUnavailable marks a database failure. Callers must fail closed.
	ErrUnavailable = errors.New("notifyprefsstore: database unavailable")
)

// Querier is the minimal pgx surface Get/Upsert need: both are single
// atomic statements (one SELECT, one INSERT ... ON CONFLICT ... RETURNING),
// so nothing beyond QueryRow is required for either -- exactly mirroring
// notifyconsentstore's own minimal Querier.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Postgres is the PostgreSQL-backed store for alert_preferences. Construct
// with Open, or by setting DB directly in a test.
type Postgres struct{ DB Querier }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing notifyconsentstore/notifydevicestore/identitystore hardened
// Open convention exactly: this package is dormant until a future
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
// own safeDB: a 23503 (foreign_key_violation) here always means "user_id
// does not reference a real users row," which is already exactly what
// ErrInput means -- there is no ownership meaning to carve out locally the
// way notifyconsentstore.Record must for its own composite FK.
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

// toPgTime converts notifyprefs' own domain representation of a quiet-hours
// clock offset (a *time.Duration since midnight, verified empirically
// against this package's own local rollback-only development database to
// be the correct domain-side shape) into the pgx wire type for a plain
// PostgreSQL `time` column. A direct time.Duration query argument was
// verified NOT to work (pgx attempts to send it as text, which Postgres
// rejects with "invalid input syntax for type time"); pgtype.Time's
// Microseconds-since-midnight field is the form that round-trips correctly.
func toPgTime(d *time.Duration) pgtype.Time {
	if d == nil {
		return pgtype.Time{}
	}
	return pgtype.Time{Microseconds: int64(*d / time.Microsecond), Valid: true}
}

// fromPgTime is toPgTime's inverse, used when scanning a row back.
func fromPgTime(t pgtype.Time) *time.Duration {
	if !t.Valid {
		return nil
	}
	d := time.Duration(t.Microseconds) * time.Microsecond
	return &d
}

// emptyIfNil returns ids unchanged, or a non-nil empty slice if ids is nil.
// notification_consents' sibling tables never distinguish a nil Go slice
// from an empty one, but this package's own columns default to
// '{}'::text[], never NULL (both NOT NULL), so a Preferences value built
// with a nil ToneSetIDs/KeywordListIDs/Channels must still write the
// column's own empty-array default, not attempt to write a SQL NULL a NOT
// NULL column would reject.
func emptyIfNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// scanPreferences reads exactly the column list both Get's SELECT and
// Upsert's RETURNING clause produce.
func scanPreferences(row pgx.Row) (notifyprefs.Preferences, error) {
	var (
		userID                     int64
		enabled                    bool
		channelStrs                []string
		toneSetIDs, keywordListIDs []string
		minConfidence              *float64
		quietStart, quietEnd       pgtype.Time
		quietTZ                    *string
		createdAt, updatedAt       time.Time
	)
	if err := row.Scan(&userID, &enabled, &channelStrs, &toneSetIDs, &keywordListIDs, &minConfidence,
		&quietStart, &quietEnd, &quietTZ, &createdAt, &updatedAt); err != nil {
		return notifyprefs.Preferences{}, err
	}
	channels := make([]notifyprefs.Channel, len(channelStrs))
	for i, c := range channelStrs {
		channels[i] = notifyprefs.Channel(c)
	}
	return notifyprefs.Preferences{
		UserID:             identity.UserID(userID),
		Enabled:            enabled,
		Channels:           channels,
		ToneSetIDs:         toneSetIDs,
		KeywordListIDs:     keywordListIDs,
		MinConfidence:      minConfidence,
		QuietHoursStart:    fromPgTime(quietStart),
		QuietHoursEnd:      fromPgTime(quietEnd),
		QuietHoursTimezone: quietTZ,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	}, nil
}

// Get returns the current preferences row for userID. ok is false with a
// nil error exactly when no row exists yet for userID -- this is not an
// error condition: a user who has never configured preferences simply has
// no row (Section 7). The returned zero-value Preferences (Enabled: false,
// empty Channels/ToneSetIDs/KeywordListIDs, nil MinConfidence/quiet-hours
// fields) happens to already equal the migration's own column defaults, so
// a caller that only checks Enabled fails closed identically whether the
// row is missing or explicitly disabled -- missing preferences must never
// be interpreted as "deliver by default." ok exists so a caller that needs
// to distinguish "never configured" from "explicitly configured, all
// defaults" (for example, a preferences UI) still can.
func (p *Postgres) Get(ctx context.Context, userID identity.UserID) (notifyprefs.Preferences, bool, error) {
	if p == nil || p.DB == nil {
		return notifyprefs.Preferences{}, false, ErrUnavailable
	}
	if userID <= 0 {
		return notifyprefs.Preferences{}, false, ErrInput
	}

	row := p.DB.QueryRow(ctx, `SELECT user_id, enabled, channels, tone_set_ids, keyword_list_ids, min_confidence,
        quiet_hours_start, quiet_hours_end, quiet_hours_timezone, created_at, updated_at
        FROM alert_preferences WHERE user_id = $1`, int64(userID))

	prefs, err := scanPreferences(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyprefs.Preferences{}, false, nil
		}
		return notifyprefs.Preferences{}, false, safeDB(err)
	}
	return prefs, true, nil
}

// Upsert creates or fully replaces the single current preferences row for
// prefs.UserID (Section 7; migration 000010's Decision 3: "one mutable
// current-settings row per user... no preference-history versioning"). now
// is the caller-supplied clock value; this package never calls time.Now()
// itself.
//
// This is a single INSERT ... ON CONFLICT (user_id) DO UPDATE ... RETURNING
// statement, exactly mirroring notifydevicestore.Register's own ON CONFLICT
// shape: Postgres resolves "does a row already exist for this user" and
// "create vs. replace" atomically in one round trip, with no
// application-level transaction or row lock required. Every configurable
// field (enabled, channels, tone-set/keyword-list ids, min confidence,
// quiet-hours window and time zone) is unconditionally overwritten on every
// call -- this is full-replace, whole-resource semantics (matching the
// amendment's own proposed PUT route for this resource), never a
// field-level partial patch.
//
// created_at is deliberately never included in the DO UPDATE SET list, so
// an existing row's original created_at survives every subsequent Upsert
// unchanged; it is set once, from now, only on the INSERT branch (a brand
// new row). updated_at is likewise never included in the DO UPDATE SET
// list: migration 000010's own alert_preferences_updated_at trigger
// (BEFORE UPDATE, shared with notification_devices' identical trigger)
// unconditionally overwrites updated_at with clock_timestamp() on every
// UPDATE -- including the DO UPDATE branch of this very statement -- so
// this method does not attempt to fight that trigger by supplying its own
// value there; now is used for updated_at only on the INSERT branch, where
// no trigger fires and no other source of truth exists yet.
//
// prefs is validated with its own Preferences.Validate() before any
// database access, so no malformed value ever reaches SQL. The only
// database-detectable failure this statement can produce is a foreign-key
// violation on user_id (23503, meaning prefs.UserID does not reference a
// real users row) -- classified as the already-correct, already-generic
// ErrInput by safeDB, with no local reinterpretation (see safeDB's own
// doc comment for why no ErrForbidden-shaped translation belongs here at
// all).
func (p *Postgres) Upsert(ctx context.Context, now time.Time, prefs notifyprefs.Preferences) (notifyprefs.Preferences, error) {
	if p == nil || p.DB == nil {
		return notifyprefs.Preferences{}, ErrUnavailable
	}
	if now.IsZero() {
		return notifyprefs.Preferences{}, ErrInput
	}
	if err := prefs.Validate(); err != nil {
		return notifyprefs.Preferences{}, ErrInput
	}

	// make(..., len(...)) always yields a non-nil slice even when
	// prefs.Channels is nil/empty, so channels itself never needs
	// emptyIfNil the way ToneSetIDs/KeywordListIDs (passed through
	// directly from the caller-supplied prefs) still might.
	channels := make([]string, len(prefs.Channels))
	for i, c := range prefs.Channels {
		channels[i] = string(c)
	}

	row := p.DB.QueryRow(ctx, `INSERT INTO alert_preferences
            (user_id, enabled, channels, tone_set_ids, keyword_list_ids, min_confidence,
             quiet_hours_start, quiet_hours_end, quiet_hours_timezone, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
        ON CONFLICT (user_id) DO UPDATE SET
            enabled = EXCLUDED.enabled,
            channels = EXCLUDED.channels,
            tone_set_ids = EXCLUDED.tone_set_ids,
            keyword_list_ids = EXCLUDED.keyword_list_ids,
            min_confidence = EXCLUDED.min_confidence,
            quiet_hours_start = EXCLUDED.quiet_hours_start,
            quiet_hours_end = EXCLUDED.quiet_hours_end,
            quiet_hours_timezone = EXCLUDED.quiet_hours_timezone
        RETURNING user_id, enabled, channels, tone_set_ids, keyword_list_ids, min_confidence,
            quiet_hours_start, quiet_hours_end, quiet_hours_timezone, created_at, updated_at`,
		int64(prefs.UserID), prefs.Enabled, channels, emptyIfNil(prefs.ToneSetIDs), emptyIfNil(prefs.KeywordListIDs),
		prefs.MinConfidence, toPgTime(prefs.QuietHoursStart), toPgTime(prefs.QuietHoursEnd), prefs.QuietHoursTimezone, now)

	updated, err := scanPreferences(row)
	if err != nil {
		return notifyprefs.Preferences{}, safeDB(err)
	}
	return updated, nil
}

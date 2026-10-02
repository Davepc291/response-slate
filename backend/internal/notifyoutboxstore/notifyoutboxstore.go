// Package notifyoutboxstore is the PostgreSQL persistence layer for the
// Step 8D-B Part 6 notifyoutbox domain model (migration
// database/migrations/000010_notification_relay_foundation.sql,
// notification_outbox table, extended by migration
// 000011_notification_outbox_claim_fencing.sql's claim_token column). It is
// kept deliberately separate from backend/internal/notifyoutbox itself:
// that package's own depcheck_test.go forbids importing a database driver
// at all, so any Postgres code must live here instead, mirroring the
// existing notifydevices/notifydevicestore, notifyconsent/
// notifyconsentstore, and notifyprefs/notifyprefsstore package-pair
// convention.
//
// This package is never registered by the live server (backend/cmd/api); a
// caller (a test, or a future authorized integration sitting behind Step 9
// authentication) must construct and invoke it explicitly. It has no HTTP
// handler, no route, no provider SDK, no notification-sending code, no
// worker loop, no eligibility-evaluation logic (device-active,
// consent-current, preferences-enabled, channel/tone/keyword match,
// min-confidence, quiet-hours, or account-status re-checks all belong to
// the future, separately authorized notifypreferences evaluation package --
// this package never imports notifydevicestore/notifyconsentstore/
// notifyprefsstore, enforced by notifyoutbox's own depcheck_test.go), and
// reads no GFR_NOTIFY_* environment variable.
//
// Enqueue, Get, MarkSent, RecordFailedAttempt, MarkExpired, and Cancel are
// implemented (Step 8D-B Part 6). ClaimNextDue, ClaimSpecific, ReleaseClaim,
// and (as of Step 8D-B Part 14A) RescheduleClaim are implemented (Step 8D-B
// Part 10/14A): a fenced claim/lease
// foundation so a future worker can safely reserve one outbox row for one
// delivery attempt at a time, and safely detect a stale, already-superseded
// completion attempt from a worker whose lease has already been reclaimed
// by someone else. Claiming itself performs no side effect beyond stamping
// claim_token/next_attempt_at: it never increments attempt_count, never
// writes notification_deliveries, never evaluates eligibility, and never
// sends anything -- all of that remains the future, separately authorized
// worker/orchestration component's job. notification_deliveries itself is
// deliberately deferred to its own, already-built, separate package
// (notifydeliverystore): it is a distinct table with distinct (hard
// immutable/append-only) semantics, not something this mutable-state-machine
// package should also own.
package notifyoutboxstore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

// Sentinel errors. None of these ever echoes a caller-supplied value.
var (
	// ErrInput marks a caller/validation error caught before any database
	// access, or a database-side check/format violation that is not the
	// specific ownership FK case Enqueue itself translates to ErrForbidden.
	// This includes migration 000010's own matched-event trigger rejection
	// (23514, raised by validate_notification_outbox_event() when the
	// referenced alert_events row's state is not 'matched') -- there is no
	// ownership meaning to that rejection, so it stays exactly the same
	// generic ErrInput safeDB already produces for every other check
	// violation, with no local reinterpretation.
	ErrInput = errors.New("notifyoutboxstore: invalid input")
	// ErrForbidden marks an Enqueue call whose (device_id, user_id) pair
	// does not match any row in notification_devices -- i.e. deviceID does
	// not belong to userID (or does not exist at all). See Enqueue's own
	// doc comment for exactly how this is derived from the database
	// response.
	ErrForbidden = errors.New("notifyoutboxstore: device does not belong to user")
	// ErrNotFound marks an OutboxID this store has no row for at all.
	ErrNotFound = errors.New("notifyoutboxstore: entry not found")
	// ErrConflict marks an OutboxID that exists but is not eligible for the
	// requested transition. This one sentinel deliberately covers every
	// such case, since from a caller's perspective "this entry's current
	// state does not permit this transition right now" is exactly one
	// condition, whichever underlying reason produced it:
	//   - already terminal (every transition method only ever operates
	//     FROM state = 'pending'; a terminal entry never transitions
	//     again);
	//   - for MarkExpired specifically, still pending but not yet past its
	//     own expires_at;
	//   - for MarkSent/RecordFailedAttempt/ReleaseClaim (Step 8D-B Part
	//     10), the claim_token supplied no longer matches the entry's
	//     current claim_token -- either because a different worker has
	//     already reclaimed this entry after the caller's own lease
	//     expired (see ClaimNextDue's own doc comment), or because the
	//     entry was never claimed with that token at all; this is the
	//     fencing mechanism that rejects a stale worker's completion
	//     attempt outright, with zero mutation;
	//   - for Cancel/MarkExpired (Part 10), the entry is currently claimed
	//     at all (claim_token IS NOT NULL) -- an active claim is never
	//     silently overridden or cleared by an unrelated terminal
	//     transition; the claim owner's own eventual completion or the
	//     lease's own expiry is the only way such an entry becomes
	//     cancelable/expirable again.
	ErrConflict = errors.New("notifyoutboxstore: entry is not eligible for this transition")
	// ErrUnavailable marks a database failure. Callers must fail closed.
	ErrUnavailable = errors.New("notifyoutboxstore: database unavailable")
)

// newClaimToken generates a fresh, cryptographically random claim-fencing
// token: 16 random bytes, hex-encoded in the standard, hyphenated UUID
// textual representation (8-4-4-4-12 hex digits). This deliberately
// duplicates internal/transcription.NewClaim's own exact random-generation
// approach locally (stdlib crypto/rand only, no external UUID library)
// rather than importing the transcription domain merely to generate
// tokens -- transcription is an unrelated domain, and this package must
// not depend on it just to reuse a few lines of token-generation logic.
//
// The hyphenated format is deliberate, not cosmetic: PostgreSQL's `uuid`
// type always normalizes to this exact canonical textual form on output,
// regardless of whether the hyphens were present in whatever text was
// originally cast to `uuid` on input (Postgres accepts both). Since every
// claim token a caller ever sees again after the initial claim comes back
// through a RETURNING clause (i.e. always canonicalized, always
// hyphenated), generating it in that same canonical shape from the start
// means exactly one token format exists anywhere in this package, with
// nothing to reconcile between "freshly generated" and "read back from the
// database."
func newClaimToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// eventIDPattern duplicates alert_events.event_id's own migration-level
// CHECK shape (database/migrations/000007_alert_persistence.sql) locally,
// for the same reason notifydevicestore/notifyconsentstore/notifyprefs
// already duplicate small sibling-package rules rather than importing them:
// this package must never import internal/alerts or internal/alertstore
// (enforced by notifyoutbox's own depcheck_test.go), and must still
// validate before ever reaching SQL.
var eventIDPattern = regexp.MustCompile(`^evt_[0-9a-f]{64}$`)

// claimTokenPattern matches the standard hyphenated UUID textual
// representation (8-4-4-4-12 lowercase hex digits) -- both newClaimToken's
// own output shape, and the canonical shape PostgreSQL always returns a
// `uuid` column's value as, regardless of the format originally used to
// write it. A caller-supplied claimToken is validated against this shape
// before it ever reaches SQL, rather than relying on a raw ::uuid cast
// failure (Postgres error 22P02, invalid_text_representation, which
// safeDB does not classify as ErrInput) to reject a malformed value.
var claimTokenPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Querier is the minimal pgx surface every method here needs: each is a
// single atomic statement (one INSERT ... RETURNING, one plain SELECT, or
// one UPDATE ... RETURNING), so nothing beyond QueryRow is required for any
// of them -- exactly mirroring notifyconsentstore/notifyprefsstore's own
// minimal Querier. Enqueue's duplicate-handling fallback SELECT (see its
// own doc comment) is a second, independent QueryRow call, not a
// transaction: by the time it runs, the row it reads is guaranteed already
// committed (the failed INSERT was itself a single autocommitted
// statement), so no explicit Begin is ever needed here.
type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Postgres is the PostgreSQL-backed store for notification_outbox.
// Construct with Open, or by setting DB directly in a test.
type Postgres struct{ DB Querier }

// Open connects to a local-development PostgreSQL instance only, mirroring
// the existing notifyprefsstore/notifyconsentstore/notifydevicestore
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
// ErrForbidden here, and it never special-cases 23514 (check_violation,
// which is what migration 000010's matched-event trigger raises) into
// anything but the already-correct generic ErrInput. The one place a
// composite-FK violation on notification_outbox means "this device does
// not belong to this user" is Enqueue itself, which derives that meaning
// from the shape of its own query (see Enqueue's doc comment) before
// safeDB is ever consulted -- not from a generic error-code classification
// that every other caller would also trip over.
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

// scanEntry reads exactly the column list every method here produces,
// whether via RETURNING or a plain SELECT: id, event_id, device_id,
// user_id, state, attempt_count, max_attempts, next_attempt_at, expires_at,
// created_at, updated_at, claim_token.
func scanEntry(row pgx.Row) (notifyoutbox.Entry, error) {
	var (
		id, deviceID, userID            int64
		eventID, state                  string
		attemptCount, maxAttempts       int
		nextAttemptAt                   *time.Time
		expiresAt, createdAt, updatedAt time.Time
		claimToken                      *string
	)
	if err := row.Scan(&id, &eventID, &deviceID, &userID, &state, &attemptCount, &maxAttempts,
		&nextAttemptAt, &expiresAt, &createdAt, &updatedAt, &claimToken); err != nil {
		return notifyoutbox.Entry{}, err
	}
	return notifyoutbox.Entry{
		ID:            notifyoutbox.OutboxID(id),
		EventID:       eventID,
		DeviceID:      notifydevices.DeviceID(deviceID),
		UserID:        identity.UserID(userID),
		State:         notifyoutbox.State(state),
		AttemptCount:  attemptCount,
		MaxAttempts:   maxAttempts,
		NextAttemptAt: nextAttemptAt,
		ExpiresAt:     expiresAt,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		ClaimToken:    claimToken,
	}, nil
}

const selectColumns = `id, event_id, device_id, user_id, state, attempt_count, max_attempts, next_attempt_at, expires_at, created_at, updated_at, claim_token`

// classifyMissingOrConflict runs after any transition method's UPDATE ...
// RETURNING returns zero rows, to decide which of the two possible reasons
// applies: id does not exist at all (ErrNotFound), or id exists but its
// current state does not permit the requested transition (ErrConflict).
// This single existence-only follow-up check is enough to classify every
// transition method's own zero-rows case correctly, because each
// transition's UPDATE ... WHERE clause already encodes every condition that
// must hold beyond mere existence (state = 'pending', and for MarkExpired,
// also expires_at <= now) -- if the row exists at all but the UPDATE still
// matched zero rows, one of those other conditions must be what failed.
func (p *Postgres) classifyMissingOrConflict(ctx context.Context, id notifyoutbox.OutboxID) error {
	var exists bool
	err := p.DB.QueryRow(ctx, `SELECT true FROM notification_outbox WHERE id = $1`, int64(id)).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return safeDB(err)
	}
	return ErrConflict
}

// Enqueue creates the single outbox entry for the (eventID, deviceID) pair,
// or -- if one already exists -- returns it completely unchanged (Section
// 10's idempotency key: "a retried send... cannot create a second visible
// notification for the same event on the same device"). now is the
// caller-supplied clock value; this package never calls time.Now() itself.
//
// This is a single INSERT ... ON CONFLICT (event_id, device_id) DO NOTHING
// RETURNING statement. DO NOTHING (rather than DO UPDATE) is deliberate: a
// duplicate enqueue attempt must never reset state, attempt_count,
// next_attempt_at, expires_at, or touch updated_at (migration 000010's own
// notification_outbox_updated_at trigger unconditionally overwrites
// updated_at on any UPDATE, so even a no-op "SET id = id"-style DO UPDATE
// would spuriously bump it -- DO NOTHING avoids that entirely, at the cost
// of a second round trip on the (expected to be rare) conflict path only).
// When the INSERT's own RETURNING yields zero rows (a real conflict fired),
// this method issues one independent, non-transactional follow-up SELECT
// by (event_id, device_id) to read the existing row back exactly as it
// already is. This is race-free: the INSERT that won the conflict was
// itself one autocommitted statement, so by the time this follow-up SELECT
// runs, that row is guaranteed already committed and visible.
//
// eventID must match alert_events.event_id's own established shape
// (^evt_[0-9a-f]{64}$, duplicated locally -- see eventIDPattern's own doc
// comment); expiresAt must be strictly after now, and firstAttemptAt must
// be strictly before expiresAt (an entry whose first eligible attempt time
// is already at or past its own expiry could never be attempted at all).
// Every other Enqueue parameter (deviceID, userID, maxAttempts) is
// validated for basic shape (positive, non-zero) before any database
// access; none of this validation trusts the caller to have checked first.
//
// This statement targets the migration's own composite foreign key,
// FOREIGN KEY (device_id, user_id) REFERENCES notification_devices
// (id, user_id): that FK is the sole mechanism that enforces deviceID
// actually belongs to userID. Its violation is Postgres error code 23503
// (foreign_key_violation), which safeDB deliberately leaves generic (see
// safeDB's own doc comment) -- so it is translated to the specific
// ErrForbidden right here, in the one place its meaning is unambiguous.
// notification_outbox in fact has a SECOND foreign key too (event_id
// REFERENCES alert_events (event_id)), which textually could also raise
// 23503 -- but it never actually can reach that path in practice: the
// migration's own validate_notification_outbox_event() trigger is a BEFORE
// INSERT row-level trigger, which Postgres always fires before the row is
// inserted and therefore strictly before the (AFTER-row-level) foreign key
// check ever runs for that row. That trigger's own query
// (`... WHERE event_id = NEW.event_id AND state = 'matched'`) already
// returns "not found" for BOTH a nonexistent eventID and an
// existing-but-not-matched one, raising its own 23514 in either case before
// the plain event_id FK constraint ever gets a chance to fire. So a 23503
// actually reaching this method can only ever mean the composite
// (device_id, user_id) FK failed -- exactly the same reasoning
// notifyconsentstore.Record's own doc comment already establishes for its
// own single-FK case, extended here to a two-FK table where only one FK
// can ever actually produce this code in practice.
func (p *Postgres) Enqueue(ctx context.Context, now time.Time, eventID string, deviceID notifydevices.DeviceID,
	userID identity.UserID, maxAttempts int, firstAttemptAt, expiresAt time.Time) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || !eventIDPattern.MatchString(eventID) || deviceID <= 0 || userID <= 0 ||
		maxAttempts <= 0 || firstAttemptAt.IsZero() || !expiresAt.After(now) || !firstAttemptAt.Before(expiresAt) {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `INSERT INTO notification_outbox
            (event_id, device_id, user_id, state, attempt_count, max_attempts, next_attempt_at, expires_at, created_at, updated_at)
        VALUES ($1, $2, $3, 'pending', 0, $4, $5, $6, $7, $7)
        ON CONFLICT (event_id, device_id) DO NOTHING
        RETURNING `+selectColumns,
		eventID, int64(deviceID), int64(userID), maxAttempts, firstAttemptAt, expiresAt, now)

	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			existing := p.DB.QueryRow(ctx, `SELECT `+selectColumns+` FROM notification_outbox WHERE event_id = $1 AND device_id = $2`,
				eventID, int64(deviceID))
			entry, err := scanEntry(existing)
			if err != nil {
				return notifyoutbox.Entry{}, safeDB(err)
			}
			return entry, nil
		}
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23503" {
			return notifyoutbox.Entry{}, ErrForbidden
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

// Get returns the outbox entry for id. ok is false with a nil error exactly
// when no row exists for id.
func (p *Postgres) Get(ctx context.Context, id notifyoutbox.OutboxID) (notifyoutbox.Entry, bool, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, false, ErrUnavailable
	}
	if id <= 0 {
		return notifyoutbox.Entry{}, false, ErrInput
	}

	row := p.DB.QueryRow(ctx, `SELECT `+selectColumns+` FROM notification_outbox WHERE id = $1`, int64(id))
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, false, nil
		}
		return notifyoutbox.Entry{}, false, safeDB(err)
	}
	return entry, true, nil
}

// MarkSent transitions id from pending to sent (a successful delivery
// attempt). Only a currently pending entry may transition; an unknown id
// fails closed with ErrNotFound, and an existing but already-terminal id
// fails closed with ErrConflict (see ErrConflict's own doc comment) --
// never silently re-succeeding and never overwriting a terminal outcome.
//
// As of Step 8D-B Part 10, this transition is fenced: claimToken must
// equal the entry's own current claim_token, checked in the same WHERE
// clause as id/state (id, state, and claim-token equality together are the
// complete and sufficient fencing condition -- no additional lease-expiry
// re-validation is performed at completion time, exactly mirroring the
// existing radio_transmissions/transcription_claim precedent's own
// finish_transcription logic). If a different worker has since reclaimed
// this entry (its claim_token no longer equals the caller's own, stale
// token), this call matches zero rows and fails closed with ErrConflict,
// mutating nothing -- the reclaiming worker's own attempt is completely
// unaffected. On success, claim_token is cleared to NULL: a sent entry is
// terminal and carries no claim at all (migration 000011's own
// notification_outbox_claim_implies_pending CHECK would reject a terminal
// row with a non-NULL claim_token in any case).
func (p *Postgres) MarkSent(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || id <= 0 || !claimTokenPattern.MatchString(claimToken) {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox SET state = 'sent', next_attempt_at = NULL, claim_token = NULL
        WHERE id = $1 AND state = 'pending' AND claim_token = $2::uuid
        RETURNING `+selectColumns, int64(id), claimToken)
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, p.classifyMissingOrConflict(ctx, id)
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

// RecordFailedAttempt records one more failed delivery attempt against id,
// and owns the entire deterministic outcome decision itself (never left to
// the caller to pick the wrong terminal state) -- computed atomically, in
// one UPDATE, entirely from the row's own current values under Postgres's
// own row lock, so two concurrent callers can never race each other into
// an inconsistent attempt_count:
//
//   - If now is at or past the entry's own expires_at, the entry becomes
//     expired -- this check is made FIRST, and wins even if attempts also
//     happen to be exhausted at the same moment (Section 10's Expiration
//     bullet: "an entry that exhausts its attempts OR reaches expiration
//     without success" -- these are two distinct terminal outcomes, and
//     expiration is the one that applies whenever both could otherwise
//     apply at once).
//   - Otherwise, if this attempt would bring attempt_count to or past
//     max_attempts, the entry becomes dead_letter.
//   - Otherwise, the entry stays pending, and next_attempt_at is set to
//     the caller-supplied nextAttemptAt (the actual backoff duration/
//     formula is a future caller's decision -- Section 10: "an explicit
//     caller/configuration value, never assumed" -- this method only
//     stores whatever value it is given).
//
// attempt_count is incremented by exactly one on every call that matches a
// pending row, regardless of which of the three outcomes above applies --
// including the terminal (expired or dead_letter) outcomes, so the
// returned Entry always accurately reflects how many attempts were
// actually made, and migration 000010's own
// notification_outbox_attempt_count_bounded CHECK (attempt_count <=
// max_attempts) can never be violated: once attempt_count reaches
// max_attempts the row is dead_letter, not pending, so no further call
// can ever match it and increment attempt_count again.
//
// next_attempt_at is cleared to NULL whenever the outcome is terminal
// (expired or dead_letter): a terminal entry has no next attempt.
//
// As with every other transition method, this only ever operates from
// state = 'pending'; an unknown id fails closed with ErrNotFound, and an
// already-terminal id fails closed with ErrConflict.
//
// As of Step 8D-B Part 10, this transition is fenced identically to
// MarkSent: claimToken must equal the entry's own current claim_token, in
// the same WHERE clause as id/state, with no additional lease-expiry
// re-validation. This applies uniformly to all three of the CASE
// expression's own branches above -- the fencing check happens at row
// selection, before any of those branches are ever evaluated, so a stale
// worker's claim can never increment attempt_count, transition the entry
// to dead_letter/expired, or overwrite a newer claim's own next_attempt_at,
// regardless of which outcome its own (unfenced) view of the row would
// have produced. claim_token is cleared to NULL on every one of the three
// branches: a fresh claim is always required for whatever attempt comes
// next (Part 10's own "retries create a new claim token each time" design),
// and a terminal branch (expired/dead_letter) must carry no claim at all,
// exactly like MarkSent.
func (p *Postgres) RecordFailedAttempt(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, nextAttemptAt time.Time, claimToken string) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || id <= 0 || nextAttemptAt.IsZero() || !claimTokenPattern.MatchString(claimToken) {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox
        SET
            state = CASE
                WHEN $2::timestamptz >= expires_at THEN 'expired'
                WHEN attempt_count + 1 >= max_attempts THEN 'dead_letter'
                ELSE 'pending'
            END,
            attempt_count = attempt_count + 1,
            next_attempt_at = CASE
                WHEN $2::timestamptz >= expires_at THEN NULL
                WHEN attempt_count + 1 >= max_attempts THEN NULL
                ELSE $3::timestamptz
            END,
            claim_token = NULL
        WHERE id = $1 AND state = 'pending' AND claim_token = $4::uuid
        RETURNING `+selectColumns, int64(id), now, nextAttemptAt, claimToken)
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, p.classifyMissingOrConflict(ctx, id)
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

// MarkExpired transitions id from pending directly to expired, for the
// case where an entry reaches its own expires_at without any delivery
// attempt ever having been made at all (a future reaper-style sweep's
// primitive -- the sweep loop itself is out of scope here, only this
// single-row conditional transition is). It only ever succeeds for an
// entry that is currently pending, already at or past its own expires_at,
// AND currently unclaimed; an unknown id fails closed with ErrNotFound, and
// an id that exists but is either already terminal, still pending but not
// yet actually due to expire, or currently claimed (Step 8D-B Part 10: an
// active claim is never silently overridden or cleared by this
// transition), all fail closed with the same ErrConflict (see ErrConflict's
// own doc comment for why one sentinel covers every such case).
func (p *Postgres) MarkExpired(ctx context.Context, now time.Time, id notifyoutbox.OutboxID) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || id <= 0 {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox SET state = 'expired', next_attempt_at = NULL
        WHERE id = $1 AND state = 'pending' AND claim_token IS NULL AND expires_at <= $2
        RETURNING `+selectColumns, int64(id), now)
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, p.classifyMissingOrConflict(ctx, id)
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

// Cancel transitions id from pending to canceled (Section 10:
// "if a user revokes consent, disables notifications, or is disabled by an
// administrator while an entry is still queued, the entry is canceled, not
// delivered on its next scheduled attempt"; also the primitive a future,
// separately authorized quiet-hours-suppression evaluator would call, per
// migration 000010's own Decision 4). This package makes no decision about
// *when* Cancel should be called for any of those reasons -- it only
// provides the transition itself.
//
// As of Step 8D-B Part 10, Cancel only ever operates on a currently
// unclaimed entry (claim_token IS NULL): a cancellation must never
// terminate a row out from under an active claim, silently clearing or
// overriding whatever a claim owner is currently doing with it. If Cancel
// is attempted against a claimed, still-pending entry, this fails closed
// with ErrConflict, exactly like every other not-currently-eligible case --
// the claim owner's own eventual completion (MarkSent/RecordFailedAttempt)
// or the lease's own expiry is the only way such an entry becomes
// cancelable again. As with every other transition method, an unknown id
// fails closed with ErrNotFound, and an already-terminal id fails closed
// with ErrConflict too.
func (p *Postgres) Cancel(ctx context.Context, now time.Time, id notifyoutbox.OutboxID) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || id <= 0 {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox SET state = 'canceled', next_attempt_at = NULL
        WHERE id = $1 AND state = 'pending' AND claim_token IS NULL
        RETURNING `+selectColumns, int64(id))
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, p.classifyMissingOrConflict(ctx, id)
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

// ClaimNextDue atomically finds one currently-actionable pending entry --
// state = 'pending' AND next_attempt_at <= now, exactly the same condition
// that has always meant "due for a fresh retry," now additionally
// satisfied by any entry whose previous claim's lease has already lapsed
// (Step 8D-B Part 10: abandoned-claim recovery is free, automatic, and
// requires no special-case code beyond this one shared condition) -- and
// reserves it for exactly one delivery attempt: a fresh claim_token is
// generated (never reused across attempts) and next_attempt_at is bumped
// forward to now + leaseDuration, now serving as this claim's own lease
// expiry rather than a retry-due time (see notifyoutbox.Entry.NextAttemptAt's
// own doc comment for this dual meaning).
//
// This is a single atomic statement: an outer UPDATE targets the id chosen
// by an inner SELECT ... FOR UPDATE SKIP LOCKED, so two concurrent callers
// can never claim the same row, and a caller racing against a row another
// transaction already has locked simply skips it and considers the next
// candidate, rather than blocking. No explicit transaction is used here,
// exactly like every other method in this package: the single statement's
// own atomicity is sufficient.
//
// Claiming itself performs no side effect beyond stamping claim_token/
// next_attempt_at: it never increments attempt_count, never writes
// notification_deliveries, never evaluates eligibility, and never sends
// anything.
//
// ok is false with a nil error exactly when no entry is currently due --
// an empty queue is a normal, expected outcome, never an error.
func (p *Postgres) ClaimNextDue(ctx context.Context, now time.Time, leaseDuration time.Duration) (notifyoutbox.Entry, bool, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, false, ErrUnavailable
	}
	if now.IsZero() || leaseDuration <= 0 {
		return notifyoutbox.Entry{}, false, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox
        SET claim_token = $2::uuid, next_attempt_at = $3
        WHERE id = (
            SELECT id FROM notification_outbox
            WHERE state = 'pending' AND next_attempt_at <= $1
            ORDER BY next_attempt_at
            FOR UPDATE SKIP LOCKED
            LIMIT 1
        )
        RETURNING `+selectColumns, now, newClaimToken(), now.Add(leaseDuration))
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, false, nil
		}
		return notifyoutbox.Entry{}, false, safeDB(err)
	}
	return entry, true, nil
}

// ClaimSpecific claims id specifically, rather than whichever entry happens
// to be next due -- otherwise identical to ClaimNextDue: the same
// fresh-token, same lease-duration-from-now semantics, the same "no side
// effect beyond claim_token/next_attempt_at" guarantee. It only succeeds
// for an entry that is both currently pending and already due (the
// existing "not yet due" condition and the new "already claimed by a
// still-valid lease" condition are indistinguishable at this single
// next_attempt_at <= now check, exactly as intended: both mean "not
// currently claimable"); an unknown id fails closed with ErrNotFound, and
// an id that exists but is not currently claimable fails closed with
// ErrConflict.
//
// A plain UPDATE ... WHERE id = $1 is sufficient here, with no FOR UPDATE
// SKIP LOCKED: targeting one already-known id has no "which of several
// candidates" ambiguity for SKIP LOCKED to resolve -- the UPDATE's own
// ordinary row-level lock already fully serializes two concurrent callers
// targeting the same id.
func (p *Postgres) ClaimSpecific(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, leaseDuration time.Duration) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || id <= 0 || leaseDuration <= 0 {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox
        SET claim_token = $3::uuid, next_attempt_at = $4
        WHERE id = $1 AND state = 'pending' AND next_attempt_at <= $2
        RETURNING `+selectColumns, int64(id), now, newClaimToken(), now.Add(leaseDuration))
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, p.classifyMissingOrConflict(ctx, id)
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

// ReleaseClaim voluntarily releases id's current claim before its lease
// would otherwise expire (for example, a worker shutting down cleanly
// rather than making some other worker wait out the full lease
// unnecessarily): claim_token is cleared to NULL and next_attempt_at is set
// to now, making the entry immediately reclaimable by a future
// ClaimNextDue/ClaimSpecific call. This never touches attempt_count: a
// released, never-attempted claim costs nothing, exactly like an
// abandoned claim recovered automatically via lease expiry.
//
// Fenced identically to MarkSent/RecordFailedAttempt: claimToken must
// equal id's own current claim_token, checked in the same WHERE clause as
// id/state. A stale or already-superseded token fails closed with
// ErrConflict, mutating nothing -- releasing with the wrong token can
// never clear a different (newer) claim out from under its own owner.
func (p *Postgres) ReleaseClaim(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || id <= 0 || !claimTokenPattern.MatchString(claimToken) {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox SET claim_token = NULL, next_attempt_at = $2
        WHERE id = $1 AND state = 'pending' AND claim_token = $3::uuid
        RETURNING `+selectColumns, int64(id), now, claimToken)
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, p.classifyMissingOrConflict(ctx, id)
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

// RescheduleClaim releases id's current claim like ReleaseClaim, but sets
// next_attempt_at to a caller-supplied future time instead of now (Step
// 8D-B Part 14A). It exists so a caller (the notifyworker orchestrator) can
// requeue an entry that is ineligible for a reason expected to resolve
// later -- quiet hours, or a dev-only worker encountering a device that is
// not test_mode -- without either consuming an attempt via
// RecordFailedAttempt (reserved for an actual failed provider attempt) or
// creating a hot reclaim loop via ReleaseClaim's own always-immediate
// semantics.
//
// Fenced identically to ReleaseClaim: claimToken must equal id's own
// current claim_token, checked in the same WHERE clause as id/state. A
// stale or already-superseded token fails closed with ErrConflict,
// mutating nothing -- rescheduling with the wrong token can never clear a
// different (newer) claim out from under its own owner.
//
// nextAttemptAt must be strictly after now (ErrInput otherwise -- a caller
// requeuing to "now or earlier" should call ReleaseClaim instead) and must
// not be after id's own expires_at: this is checked against the row's own
// expires_at column in the same statement (mirroring RecordFailedAttempt's
// own "$2::timestamptz >= expires_at" style comparison against the row's
// live data), so a caller that fails to cap at expires_at itself is
// rejected -- ErrConflict, exactly like every other not-currently-eligible
// case -- rather than silently scheduling a dead attempt past the entry's
// own expiry.
func (p *Postgres) RescheduleClaim(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string, nextAttemptAt time.Time) (notifyoutbox.Entry, error) {
	if p == nil || p.DB == nil {
		return notifyoutbox.Entry{}, ErrUnavailable
	}
	if now.IsZero() || id <= 0 || !claimTokenPattern.MatchString(claimToken) || nextAttemptAt.IsZero() || !nextAttemptAt.After(now) {
		return notifyoutbox.Entry{}, ErrInput
	}

	row := p.DB.QueryRow(ctx, `UPDATE notification_outbox SET claim_token = NULL, next_attempt_at = $2
        WHERE id = $1 AND state = 'pending' AND claim_token = $3::uuid AND $2::timestamptz <= expires_at
        RETURNING `+selectColumns, int64(id), nextAttemptAt, claimToken)
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notifyoutbox.Entry{}, p.classifyMissingOrConflict(ctx, id)
		}
		return notifyoutbox.Entry{}, safeDB(err)
	}
	return entry, nil
}

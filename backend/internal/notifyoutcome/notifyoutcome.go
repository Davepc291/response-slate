// Package notifyoutcome is the Step 8D-B Part 14B atomic outcome-recording
// coordinator: for every outbox-delivery outcome that must write both
// notification_outbox and notification_deliveries (and, for an unauthorized
// outcome, notification_devices too), this package begins one PostgreSQL
// transaction, composes the existing, already-reviewed, already-authoritative
// store implementations (backend/internal/notifyoutboxstore,
// backend/internal/notifydeliverystore, backend/internal/notifydevicestore)
// over that same transaction, and commits all of their writes together or
// rolls back all of them together.
//
// This package contains zero persistence SQL of its own. Every statement
// that ever executes through a Store method here is issued by one of the
// three existing store packages' own, unchanged, already-tested methods --
// this package only owns the transaction's lifecycle (Begin/Commit/Rollback)
// and decides which existing methods to call in which order. There is
// exactly one authoritative implementation of each outbox transition (still
// notifyoutboxstore) and exactly one authoritative implementation of
// delivery insertion (still notifydeliverystore.Record); this package merely
// composes them.
//
// This composition works because notifyoutboxstore.Postgres,
// notifydeliverystore.Postgres, and notifydevicestore.Postgres each already
// accept any value satisfying their own existing (and already minimal)
// Querier/Pool interface, and a single pgx.Tx value already satisfies every
// one of those interfaces simultaneously (pgx.Tx implements QueryRow, Query,
// and Begin -- the last of which composes as a Postgres SAVEPOINT when the
// receiver is already a transaction, exactly the mechanism
// notifydevicestore.Replace/Revoke already rely on internally). No adapter,
// no interface change, and no SQL duplication was needed to make this work.
//
// This package is never registered by the live server (backend/cmd/api); a
// caller (a test, or backend/internal/notifyworker) must construct and
// invoke it explicitly, mirroring every other notify-family package's own
// dormant-by-construction convention. It performs no HTTP I/O, no
// eligibility evaluation, and no provider SDK call of any kind.
package notifyoutcome

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydelivery"
	"greenwich-fire-responder/backend/internal/notifydeliverystore"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifydevicestore"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifyoutboxstore"
)

// ErrUnavailable marks a failure to begin or commit the coordinating
// transaction itself (never a raw driver error). A fencing conflict, a
// not-found id, or an input-shape error from one of the composed store
// calls is NEVER translated to this: those propagate as the exact same
// sentinel the underlying store package already defines (for example
// notifyoutboxstore.ErrConflict), so a caller's existing errors.Is checks
// against those sentinels continue to work unchanged.
var ErrUnavailable = errors.New("notifyoutcome: database unavailable")

// Pool is the minimal surface Store needs: the ability to begin a
// transaction. Satisfied directly by *pgxpool.Pool. Declared locally
// (rather than depending on the concrete type) so this package's own
// fail-closed input-validation paths are testable without a real database,
// exactly mirroring notifydevicestore.Pool's identical convention.
type Pool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store is the Part 14B atomic outcome coordinator. Construct it by setting
// DB directly (there is no Open: this package never dials a database
// itself, only ever operates on a pool/transaction a caller already holds).
type Store struct {
	DB Pool
}

// rollback is a best-effort cleanup helper for the "defer rollback(tx)
// unless already committed" pattern, duplicated locally rather than
// imported, exactly mirroring notifydevicestore's own identical helper: a
// Rollback on an already-committed transaction is safe to call and simply
// reports (and here, discards) an already-closed-transaction error.
func rollback(tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

// deliveryOutcomeForState maps the notifyoutbox.State RecordFailedAttempt
// actually returned to the matching notifydelivery.Outcome. The caller must
// use the ACTUAL returned state, never assume which of
// RecordFailedAttempt's own three CASE branches was taken -- this is why
// RecordFailedAttempt (the method on Store, below) derives the outcome
// itself rather than trusting a caller-supplied one.
func deliveryOutcomeForState(state notifyoutbox.State) (notifydelivery.Outcome, bool) {
	switch state {
	case notifyoutbox.StatePending:
		return notifydelivery.OutcomeFailed, true
	case notifyoutbox.StateDeadLetter:
		return notifydelivery.OutcomeDeadLetter, true
	case notifyoutbox.StateExpired:
		return notifydelivery.OutcomeExpired, true
	default:
		return "", false
	}
}

// RecordSent begins a transaction, calls the existing, unchanged
// notifyoutboxstore.MarkSent, then the existing, unchanged
// notifydeliverystore.Record (d.Outcome must already be
// notifydelivery.OutcomeSent), committing both or neither. A fencing
// conflict from MarkSent propagates as the unchanged
// notifyoutboxstore.ErrConflict sentinel, with zero delivery row ever
// inserted (Record is never reached).
func (s *Store) RecordSent(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	if s == nil || s.DB == nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	defer rollback(tx)

	outbox := &notifyoutboxstore.Postgres{DB: tx}
	entry, err := outbox.MarkSent(ctx, now, id, claimToken)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	deliveries := &notifydeliverystore.Postgres{DB: tx}
	recorded, err := deliveries.Record(ctx, d)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	return entry, recorded, nil
}

// RecordCanceled begins a transaction, calls the new (Step 8D-B Part 14B)
// notifyoutboxstore.CancelClaimed -- a direct, single-statement,
// claim-fenced transition straight to canceled, with no ReleaseClaim
// intermediate step at all -- then the existing, unchanged
// notifydeliverystore.Record (d.Outcome must already be
// notifydelivery.OutcomeCanceled), committing both or neither.
func (s *Store) RecordCanceled(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	if s == nil || s.DB == nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	defer rollback(tx)

	outbox := &notifyoutboxstore.Postgres{DB: tx}
	entry, err := outbox.CancelClaimed(ctx, now, id, claimToken)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	deliveries := &notifydeliverystore.Postgres{DB: tx}
	recorded, err := deliveries.Record(ctx, d)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	return entry, recorded, nil
}

// RecordFailedAttempt begins a transaction, calls the existing, UNCHANGED
// notifyoutboxstore.RecordFailedAttempt (temporary-failure retry/backoff
// semantics preserved exactly: the same three-way pending/dead_letter/
// expired CASE, the same attempt_count/expiry rules), then derives the
// correct notifydelivery.Outcome from the ACTUAL returned notifyoutbox.State
// and records it, committing both or neither.
//
// d.Outcome is ignored and overwritten: it cannot be known until the
// transition's real result is seen. The caller supplies
// OutboxID/EventID/DeviceID/AttemptNumber/ErrorCode only.
func (s *Store) RecordFailedAttempt(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, nextAttemptAt time.Time, claimToken string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	if s == nil || s.DB == nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	defer rollback(tx)

	outbox := &notifyoutboxstore.Postgres{DB: tx}
	entry, err := outbox.RecordFailedAttempt(ctx, now, id, nextAttemptAt, claimToken)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	outcome, ok := deliveryOutcomeForState(entry.State)
	if !ok {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, errors.New("notifyoutcome: unexpected outbox state after RecordFailedAttempt")
	}
	d.Outcome = outcome

	deliveries := &notifydeliverystore.Postgres{DB: tx}
	recorded, err := deliveries.Record(ctx, d)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	return entry, recorded, nil
}

// RecordDeadLetter begins a transaction, calls the new (Step 8D-B Part 14B)
// notifyoutboxstore.DeadLetterClaimed -- a direct, single-statement,
// claim-fenced, UNCONDITIONAL transition to dead_letter, incrementing
// attempt_count by exactly one -- then records d (d.Outcome must already be
// notifydelivery.OutcomeDeadLetter), committing both or neither. Used for a
// permanent (non-retryable) provider failure; no device mutation.
func (s *Store) RecordDeadLetter(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string, d notifydelivery.Delivery) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	if s == nil || s.DB == nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	defer rollback(tx)

	outbox := &notifyoutboxstore.Postgres{DB: tx}
	entry, err := outbox.DeadLetterClaimed(ctx, now, id, claimToken)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	deliveries := &notifydeliverystore.Postgres{DB: tx}
	recorded, err := deliveries.Record(ctx, d)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	return entry, recorded, nil
}

// RecordUnauthorized begins a transaction, calls the same
// notifyoutboxstore.DeadLetterClaimed used by RecordDeadLetter, then records
// d (d.Outcome must already be notifydelivery.OutcomeUnauthorized), then
// calls the existing, unchanged notifydevicestore.Revoke -- all three
// writes in the SAME transaction, committing all or none (Part 14B Decision
// 5: atomic). notifydevicestore.Revoke's own internal Begin call composes
// correctly as a Postgres SAVEPOINT nested inside this outer transaction
// (proven directly, not assumed: see this package's own integration tests).
//
// Ordering is terminalize -> audit -> revoke. If the fenced terminalization
// loses a race (a stale claim_token), this returns the unchanged
// notifyoutboxstore.ErrConflict and neither the delivery row nor the device
// revocation ever happens. If the delivery insert fails, the terminalization
// is rolled back too -- the entry is left exactly as it was before this
// call, still pending and claimed, to be retried or to expire naturally. If
// Revoke fails (for any reason -- not-found, forbidden, invalid input, or a
// database error), the ENTIRE transaction is rolled back, undoing the
// terminalization and the audit insert as well: true atomicity means a
// revoke failure cannot leave a dead-lettered-but-unrevoked entry. A process
// crash at any point before Commit returns leaves no partial effect
// whatsoever -- PostgreSQL's own transactional guarantees ensure either all
// three writes are durable or none are.
//
// Completing in exactly one call means no next-cycle cleanup is ever
// required: the entry is immediately, atomically terminal.
func (s *Store) RecordUnauthorized(ctx context.Context, now time.Time, id notifyoutbox.OutboxID, claimToken string, d notifydelivery.Delivery, userID identity.UserID, deviceID notifydevices.DeviceID) (notifyoutbox.Entry, notifydelivery.Delivery, error) {
	if s == nil || s.DB == nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	defer rollback(tx)

	outbox := &notifyoutboxstore.Postgres{DB: tx}
	entry, err := outbox.DeadLetterClaimed(ctx, now, id, claimToken)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	deliveries := &notifydeliverystore.Postgres{DB: tx}
	recorded, err := deliveries.Record(ctx, d)
	if err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	devices := &notifydevicestore.Postgres{DB: tx}
	if err := devices.Revoke(ctx, now, userID, deviceID); err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return notifyoutbox.Entry{}, notifydelivery.Delivery{}, ErrUnavailable
	}
	return entry, recorded, nil
}

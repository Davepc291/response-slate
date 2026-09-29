package notifyoutboxstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

func validEventID() string {
	return "evt_" + strings.Repeat("a", 64)
}

func validClaimToken() string {
	return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
}

// fakeRow implements pgx.Row over a fixed row of the exact 12-column shape
// every method here produces, or a fixed error.
type fakeRow struct {
	id, deviceID, userID            int64
	eventID, state                  string
	attemptCount, maxAttempts       int
	nextAttemptAt                   *time.Time
	expiresAt, createdAt, updatedAt time.Time
	claimToken                      *string
	err                             error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.id
	*dest[1].(*string) = r.eventID
	*dest[2].(*int64) = r.deviceID
	*dest[3].(*int64) = r.userID
	*dest[4].(*string) = r.state
	*dest[5].(*int) = r.attemptCount
	*dest[6].(*int) = r.maxAttempts
	*dest[7].(**time.Time) = r.nextAttemptAt
	*dest[8].(*time.Time) = r.expiresAt
	*dest[9].(*time.Time) = r.createdAt
	*dest[10].(*time.Time) = r.updatedAt
	*dest[11].(**string) = r.claimToken
	return nil
}

// fakeQuerier captures every QueryRow call in order, so a test can inspect
// each statement's SQL/args individually (Enqueue's duplicate path issues
// two calls), and returns each pre-programmed row in sequence.
type fakeQuerier struct {
	calls []struct {
		sql  string
		args []any
	}
	rows []pgx.Row
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	idx := len(q.calls)
	q.calls = append(q.calls, struct {
		sql  string
		args []any
	}{sql, args})
	if idx < len(q.rows) {
		return q.rows[idx]
	}
	return fakeRow{err: errors.New("fakeQuerier: no more programmed rows")}
}

func single(row pgx.Row) *fakeQuerier { return &fakeQuerier{rows: []pgx.Row{row}} }

func fixedNow() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }

func TestEnqueueUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	now := fixedNow()
	if _, err := p.Enqueue(context.Background(), now, validEventID(), 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestEnqueueRejectsInvalidInputBeforeQuerying(t *testing.T) {
	now := fixedNow()
	cases := []struct {
		name           string
		now            time.Time
		eventID        string
		deviceID       notifydevices.DeviceID
		userID         identity.UserID
		maxAttempts    int
		firstAttemptAt time.Time
		expiresAt      time.Time
	}{
		{"zero now", time.Time{}, validEventID(), 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)},
		{"bad event id", now, "not-an-event-id", 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)},
		{"short event id hex", now, "evt_abc", 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)},
		{"uppercase hex rejected", now, "evt_" + strings.Repeat("A", 64), 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)},
		{"zero device id", now, validEventID(), 0, 1, 3, now.Add(time.Minute), now.Add(time.Hour)},
		{"negative device id", now, validEventID(), -1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)},
		{"zero user id", now, validEventID(), 1, 0, 3, now.Add(time.Minute), now.Add(time.Hour)},
		{"zero max attempts", now, validEventID(), 1, 1, 0, now.Add(time.Minute), now.Add(time.Hour)},
		{"negative max attempts", now, validEventID(), 1, 1, -1, now.Add(time.Minute), now.Add(time.Hour)},
		{"zero first attempt at", now, validEventID(), 1, 1, 3, time.Time{}, now.Add(time.Hour)},
		{"expires at not after now", now, validEventID(), 1, 1, 3, now.Add(time.Minute), now},
		{"expires at before now", now, validEventID(), 1, 1, 3, now.Add(time.Minute), now.Add(-time.Hour)},
		{"first attempt at not before expires at", now, validEventID(), 1, 1, 3, now.Add(time.Hour), now.Add(time.Hour)},
		{"first attempt at after expires at", now, validEventID(), 1, 1, 3, now.Add(2 * time.Hour), now.Add(time.Hour)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &fakeQuerier{}
			p := &Postgres{DB: q}
			if _, err := p.Enqueue(context.Background(), c.now, c.eventID, c.deviceID, c.userID, c.maxAttempts, c.firstAttemptAt, c.expiresAt); err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if len(q.calls) != 0 {
				t.Fatal("invalid input must never reach the database")
			}
		})
	}
}

func TestEnqueueFreshInsertSucceeds(t *testing.T) {
	now := fixedNow()
	firstAttempt := now.Add(time.Minute)
	expires := now.Add(time.Hour)
	q := single(fakeRow{
		id: 1, eventID: validEventID(), deviceID: 7, userID: 3, state: "pending",
		attemptCount: 0, maxAttempts: 3, nextAttemptAt: &firstAttempt, expiresAt: expires,
		createdAt: now, updatedAt: now,
	})
	p := &Postgres{DB: q}
	entry, err := p.Enqueue(context.Background(), now, validEventID(), 7, 3, 3, firstAttempt, expires)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q.calls) != 1 {
		t.Fatalf("expected exactly one statement for a fresh insert, got %d", len(q.calls))
	}
	sql := q.calls[0].sql
	if !strings.Contains(sql, "INSERT INTO notification_outbox") || !strings.Contains(sql, "ON CONFLICT (event_id, device_id) DO NOTHING") || !strings.Contains(sql, "RETURNING") {
		t.Fatalf("expected an idempotent INSERT ... ON CONFLICT DO NOTHING ... RETURNING statement, got %q", sql)
	}
	if strings.Contains(sql, "'") && !strings.Contains(sql, "'pending'") {
		t.Fatalf("query text must contain only placeholders (and the fixed 'pending' literal), got %q", sql)
	}
	if entry.ID != 1 || entry.EventID != validEventID() || entry.DeviceID != 7 || entry.UserID != 3 || entry.State != notifyoutbox.StatePending {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if entry.AttemptCount != 0 || entry.MaxAttempts != 3 {
		t.Fatalf("unexpected attempt bookkeeping: %+v", entry)
	}
	if entry.Claimed() {
		t.Fatalf("expected a freshly enqueued entry to be unclaimed, got %+v", entry)
	}
}

func TestEnqueueDuplicateReturnsExistingRowViaFallbackSelect(t *testing.T) {
	now := fixedNow()
	firstAttempt := now.Add(time.Minute)
	expires := now.Add(time.Hour)
	existingNext := now.Add(30 * time.Minute)
	existingCreated := now.Add(-time.Hour)
	existingUpdated := now.Add(-30 * time.Minute)
	q := &fakeQuerier{rows: []pgx.Row{
		fakeRow{err: pgx.ErrNoRows}, // ON CONFLICT DO NOTHING: zero rows
		fakeRow{ // the fallback SELECT's existing row, deliberately different from what a fresh insert would have produced
			id: 42, eventID: validEventID(), deviceID: 7, userID: 3, state: "sent",
			attemptCount: 2, maxAttempts: 3, nextAttemptAt: &existingNext, expiresAt: expires,
			createdAt: existingCreated, updatedAt: existingUpdated,
		},
	}}
	p := &Postgres{DB: q}
	entry, err := p.Enqueue(context.Background(), now, validEventID(), 7, 3, 3, firstAttempt, expires)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q.calls) != 2 {
		t.Fatalf("expected exactly two statements on the duplicate path (INSERT then fallback SELECT), got %d", len(q.calls))
	}
	if !strings.Contains(q.calls[1].sql, "SELECT") || strings.Contains(q.calls[1].sql, "INSERT") {
		t.Fatalf("expected the second statement to be a plain SELECT, got %q", q.calls[1].sql)
	}
	// The returned entry must be the EXISTING row exactly as stored, never
	// reset: state=sent, attempt_count=2, and its own original
	// created_at/updated_at, not the fresh values this call supplied.
	if entry.ID != 42 || entry.State != notifyoutbox.StateSent || entry.AttemptCount != 2 {
		t.Fatalf("expected the untouched existing row, got %+v", entry)
	}
	if !entry.CreatedAt.Equal(existingCreated) || !entry.UpdatedAt.Equal(existingUpdated) {
		t.Fatalf("expected the existing row's own timestamps to be returned unchanged, got %+v", entry)
	}
}

func TestEnqueueForeignKeyViolationIsForbidden(t *testing.T) {
	now := fixedNow()
	q := single(fakeRow{err: &pgconn.PgError{Code: "23503", Message: "violates foreign key constraint"}})
	p := &Postgres{DB: q}
	if _, err := p.Enqueue(context.Background(), now, validEventID(), 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestEnqueueMatchedEventTriggerViolationIsInput(t *testing.T) {
	// 23514 (check_violation) is what validate_notification_outbox_event()
	// raises for a non-matched or nonexistent event_id -- must stay the
	// already-generic ErrInput, never reinterpreted as ErrForbidden.
	now := fixedNow()
	q := single(fakeRow{err: &pgconn.PgError{Code: "23514", Message: "may only reference an alert_events row whose state is matched"}})
	p := &Postgres{DB: q}
	if _, err := p.Enqueue(context.Background(), now, validEventID(), 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)); err != ErrInput {
		t.Fatalf("expected ErrInput, got %v", err)
	}
}

func TestEnqueueDuplicateFallbackDatabaseErrorFailsClosed(t *testing.T) {
	now := fixedNow()
	q := &fakeQuerier{rows: []pgx.Row{
		fakeRow{err: pgx.ErrNoRows},
		fakeRow{err: errors.New("connection reset")},
	}}
	p := &Postgres{DB: q}
	if _, err := p.Enqueue(context.Background(), now, validEventID(), 1, 1, 3, now.Add(time.Minute), now.Add(time.Hour)); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestGetUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, _, err := p.Get(context.Background(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestGetRejectsInvalidID(t *testing.T) {
	for _, id := range []notifyoutbox.OutboxID{0, -1} {
		q := &fakeQuerier{}
		p := &Postgres{DB: q}
		if _, _, err := p.Get(context.Background(), id); err != ErrInput {
			t.Fatalf("expected ErrInput for id %v, got %v", id, err)
		}
		if len(q.calls) != 0 {
			t.Fatal("an invalid id must never reach the database")
		}
	}
}

func TestGetNotFoundReturnsFalseFalseNil(t *testing.T) {
	q := single(fakeRow{err: pgx.ErrNoRows})
	p := &Postgres{DB: q}
	entry, ok, err := p.Get(context.Background(), 999)
	if err != nil {
		t.Fatalf("expected a nil error for a nonexistent id, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a nonexistent id")
	}
	if entry.ID != 0 || entry.Claimed() {
		t.Fatalf("expected a zero-value Entry, got %+v", entry)
	}
}

func TestGetFoundRoundTrips(t *testing.T) {
	now := fixedNow()
	next := now.Add(time.Minute)
	q := single(fakeRow{
		id: 7, eventID: validEventID(), deviceID: 2, userID: 5, state: "pending",
		attemptCount: 1, maxAttempts: 3, nextAttemptAt: &next, expiresAt: now.Add(time.Hour),
		createdAt: now, updatedAt: now,
	})
	p := &Postgres{DB: q}
	entry, ok, err := p.Get(context.Background(), 7)
	if err != nil || !ok {
		t.Fatalf("unexpected: ok=%v err=%v", ok, err)
	}
	if entry.ID != 7 || entry.DeviceID != 2 || entry.UserID != 5 || entry.State != notifyoutbox.StatePending || entry.AttemptCount != 1 {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if entry.NextAttemptAt == nil || !entry.NextAttemptAt.Equal(next) {
		t.Fatalf("unexpected next attempt at: %+v", entry.NextAttemptAt)
	}
	if entry.Claimed() {
		t.Fatal("expected an unclaimed entry")
	}
}

func TestGetRoundTripsClaimToken(t *testing.T) {
	now := fixedNow()
	next := now.Add(time.Minute)
	token := validClaimToken()
	q := single(fakeRow{
		id: 7, eventID: validEventID(), deviceID: 2, userID: 5, state: "pending",
		attemptCount: 1, maxAttempts: 3, nextAttemptAt: &next, expiresAt: now.Add(time.Hour),
		createdAt: now, updatedAt: now, claimToken: &token,
	})
	p := &Postgres{DB: q}
	entry, ok, err := p.Get(context.Background(), 7)
	if err != nil || !ok {
		t.Fatalf("unexpected: ok=%v err=%v", ok, err)
	}
	if !entry.Claimed() || entry.ClaimToken == nil || *entry.ClaimToken != token {
		t.Fatalf("expected the claim token to round-trip, got %+v", entry)
	}
}

func TestGetDatabaseErrorFailsClosed(t *testing.T) {
	q := single(fakeRow{err: errors.New("connection reset")})
	p := &Postgres{DB: q}
	if _, ok, err := p.Get(context.Background(), 1); err != ErrUnavailable || ok {
		t.Fatalf("expected (false, ErrUnavailable), got (%v, %v)", ok, err)
	}
}

// --- Shared transition-method test helpers ---
// classifyMissingOrConflict issues a second QueryRow after a zero-rows
// UPDATE; every transition test that expects ErrNotFound/ErrConflict
// programs a two-row fakeQuerier: [0]=the UPDATE (zero rows), [1]=the
// existence check.

func TestMarkSentUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.MarkSent(context.Background(), fixedNow(), 1, validClaimToken()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestMarkSentRejectsInvalidInputBeforeQuerying(t *testing.T) {
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.MarkSent(context.Background(), time.Time{}, 1, validClaimToken()); err != ErrInput {
		t.Fatalf("expected ErrInput for zero now, got %v", err)
	}
	if _, err := p.MarkSent(context.Background(), fixedNow(), 0, validClaimToken()); err != ErrInput {
		t.Fatalf("expected ErrInput for zero id, got %v", err)
	}
	for _, bad := range []string{"", "not-hex-and-wrong-length", strings.Repeat("A", 32), strings.Repeat("a", 31), strings.Repeat("a", 33)} {
		if _, err := p.MarkSent(context.Background(), fixedNow(), 1, bad); err != ErrInput {
			t.Fatalf("expected ErrInput for malformed claim token %q, got %v", bad, err)
		}
	}
	if len(q.calls) != 0 {
		t.Fatal("invalid input must never reach the database")
	}
}

func TestMarkSentSuccess(t *testing.T) {
	now := fixedNow()
	q := single(fakeRow{id: 1, eventID: validEventID(), deviceID: 1, userID: 1, state: "sent", maxAttempts: 3, expiresAt: now.Add(time.Hour), createdAt: now, updatedAt: now})
	p := &Postgres{DB: q}
	token := validClaimToken()
	entry, err := p.MarkSent(context.Background(), now, 1, token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := q.calls[0].sql
	if !strings.Contains(sql, "SET state = 'sent'") || !strings.Contains(sql, "claim_token = NULL") ||
		!strings.Contains(sql, "WHERE id = $1 AND state = 'pending' AND claim_token = $2::uuid") {
		t.Fatalf("expected a pending-and-claim-fenced transition to sent, got %q", sql)
	}
	if q.calls[0].args[1] != token {
		t.Fatalf("expected the claim token to travel as a bound parameter, got %#v", q.calls[0].args[1])
	}
	if entry.State != notifyoutbox.StateSent {
		t.Fatalf("unexpected state: %+v", entry)
	}
	if entry.Claimed() {
		t.Fatal("expected claim_token to be cleared on the returned entry")
	}
}

func TestMarkSentNotFound(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, fakeRow{err: pgx.ErrNoRows}}}
	p := &Postgres{DB: q}
	if _, err := p.MarkSent(context.Background(), fixedNow(), 999, validClaimToken()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMarkSentConflictOnTerminalOrStaleClaimRow(t *testing.T) {
	// This same zero-rows-then-exists path is reached whether the row is
	// already terminal OR still pending but claimed by a different
	// (newer) token than the caller's own -- classifyMissingOrConflict
	// only distinguishes existence, not the reason, exactly as documented.
	// The real claim-fencing behavior (a genuinely different stored
	// claim_token causing the UPDATE itself to match zero rows) is proven
	// against a real database by the live integration test and the
	// two-connection concurrency test; this fake cannot simulate a real
	// SQL equality comparison.
	var exists bool
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, boolRow{v: &exists}}}
	p := &Postgres{DB: q}
	if _, err := p.MarkSent(context.Background(), fixedNow(), 5, validClaimToken()); err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// boolRow implements pgx.Row for the classifyMissingOrConflict existence
// check (`SELECT true FROM ...`).
type boolRow struct {
	v   *bool
	err error
}

func (r boolRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*bool) = true
	return nil
}

func TestMarkSentDatabaseErrorFailsClosed(t *testing.T) {
	q := single(fakeRow{err: errors.New("connection reset")})
	p := &Postgres{DB: q}
	if _, err := p.MarkSent(context.Background(), fixedNow(), 1, validClaimToken()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestCancelUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.Cancel(context.Background(), fixedNow(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestCancelSuccess(t *testing.T) {
	now := fixedNow()
	q := single(fakeRow{id: 1, eventID: validEventID(), state: "canceled", maxAttempts: 3, expiresAt: now.Add(time.Hour), createdAt: now, updatedAt: now})
	p := &Postgres{DB: q}
	entry, err := p.Cancel(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.State != notifyoutbox.StateCanceled {
		t.Fatalf("unexpected state: %+v", entry)
	}
	if !strings.Contains(q.calls[0].sql, "WHERE id = $1 AND state = 'pending' AND claim_token IS NULL") {
		t.Fatalf("expected a pending-and-unclaimed-guarded transition, got %q", q.calls[0].sql)
	}
}

func TestCancelConflictOnTerminalOrClaimedRow(t *testing.T) {
	// Reached whether the row is already terminal OR still pending but
	// currently claimed (claim_token IS NOT NULL) -- Cancel must never
	// silently override or clear an active claim; both cases collapse to
	// the same ErrConflict via classifyMissingOrConflict, exactly as
	// documented. The live integration test proves the claimed-row case
	// specifically against a real database.
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, boolRow{}}}
	p := &Postgres{DB: q}
	if _, err := p.Cancel(context.Background(), fixedNow(), 5); err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestCancelNotFound(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, fakeRow{err: pgx.ErrNoRows}}}
	p := &Postgres{DB: q}
	if _, err := p.Cancel(context.Background(), fixedNow(), 999); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMarkExpiredUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.MarkExpired(context.Background(), fixedNow(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestMarkExpiredSuccess(t *testing.T) {
	now := fixedNow()
	q := single(fakeRow{id: 1, eventID: validEventID(), state: "expired", maxAttempts: 3, expiresAt: now.Add(-time.Minute), createdAt: now.Add(-time.Hour), updatedAt: now})
	p := &Postgres{DB: q}
	entry, err := p.MarkExpired(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := q.calls[0].sql
	if !strings.Contains(sql, "WHERE id = $1 AND state = 'pending' AND claim_token IS NULL AND expires_at <= $2") {
		t.Fatalf("expected a pending-unclaimed-and-due-guarded transition, got %q", sql)
	}
	if entry.State != notifyoutbox.StateExpired {
		t.Fatalf("unexpected state: %+v", entry)
	}
}

func TestMarkExpiredConflictWhenNotYetDueOrNotPendingOrClaimed(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, boolRow{}}}
	p := &Postgres{DB: q}
	if _, err := p.MarkExpired(context.Background(), fixedNow(), 5); err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestMarkExpiredNotFound(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, fakeRow{err: pgx.ErrNoRows}}}
	p := &Postgres{DB: q}
	if _, err := p.MarkExpired(context.Background(), fixedNow(), 999); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRecordFailedAttemptUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	now := fixedNow()
	if _, err := p.RecordFailedAttempt(context.Background(), now, 1, now.Add(time.Minute), validClaimToken()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRecordFailedAttemptRejectsInvalidInputBeforeQuerying(t *testing.T) {
	now := fixedNow()
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.RecordFailedAttempt(context.Background(), time.Time{}, 1, now.Add(time.Minute), validClaimToken()); err != ErrInput {
		t.Fatalf("expected ErrInput for zero now, got %v", err)
	}
	if _, err := p.RecordFailedAttempt(context.Background(), now, 0, now.Add(time.Minute), validClaimToken()); err != ErrInput {
		t.Fatalf("expected ErrInput for zero id, got %v", err)
	}
	if _, err := p.RecordFailedAttempt(context.Background(), now, 1, time.Time{}, validClaimToken()); err != ErrInput {
		t.Fatalf("expected ErrInput for zero nextAttemptAt, got %v", err)
	}
	if _, err := p.RecordFailedAttempt(context.Background(), now, 1, now.Add(time.Minute), ""); err != ErrInput {
		t.Fatalf("expected ErrInput for an empty claim token, got %v", err)
	}
	if len(q.calls) != 0 {
		t.Fatal("invalid input must never reach the database")
	}
}

func TestRecordFailedAttemptSuccessStaysPending(t *testing.T) {
	now := fixedNow()
	next := now.Add(5 * time.Minute)
	q := single(fakeRow{id: 1, eventID: validEventID(), state: "pending", attemptCount: 1, maxAttempts: 3, nextAttemptAt: &next, expiresAt: now.Add(time.Hour), createdAt: now.Add(-time.Hour), updatedAt: now})
	p := &Postgres{DB: q}
	token := validClaimToken()
	entry, err := p.RecordFailedAttempt(context.Background(), now, 1, next, token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.State != notifyoutbox.StatePending || entry.AttemptCount != 1 {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if entry.Claimed() {
		t.Fatal("expected claim_token to be cleared even on the stays-pending branch")
	}
	sql := q.calls[0].sql
	if !strings.Contains(sql, "attempt_count = attempt_count + 1") ||
		!strings.Contains(sql, "claim_token = NULL") ||
		!strings.Contains(sql, "WHERE id = $1 AND state = 'pending' AND claim_token = $4::uuid") {
		t.Fatalf("unexpected statement shape: %q", sql)
	}
	if q.calls[0].args[3] != token {
		t.Fatalf("expected the claim token to travel as a bound parameter, got %#v", q.calls[0].args[3])
	}
}

func TestRecordFailedAttemptConflictOnTerminalOrStaleClaimRow(t *testing.T) {
	now := fixedNow()
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, boolRow{}}}
	p := &Postgres{DB: q}
	if _, err := p.RecordFailedAttempt(context.Background(), now, 5, now.Add(time.Minute), validClaimToken()); err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestRecordFailedAttemptNotFound(t *testing.T) {
	now := fixedNow()
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, fakeRow{err: pgx.ErrNoRows}}}
	p := &Postgres{DB: q}
	if _, err := p.RecordFailedAttempt(context.Background(), now, 999, now.Add(time.Minute), validClaimToken()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- ClaimNextDue ---

func TestClaimNextDueUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, _, err := p.ClaimNextDue(context.Background(), fixedNow(), time.Minute); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestClaimNextDueRejectsInvalidInputBeforeQuerying(t *testing.T) {
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, _, err := p.ClaimNextDue(context.Background(), time.Time{}, time.Minute); err != ErrInput {
		t.Fatalf("expected ErrInput for zero now, got %v", err)
	}
	if _, _, err := p.ClaimNextDue(context.Background(), fixedNow(), 0); err != ErrInput {
		t.Fatalf("expected ErrInput for zero lease duration, got %v", err)
	}
	if _, _, err := p.ClaimNextDue(context.Background(), fixedNow(), -time.Minute); err != ErrInput {
		t.Fatalf("expected ErrInput for a negative lease duration, got %v", err)
	}
	if len(q.calls) != 0 {
		t.Fatal("invalid input must never reach the database")
	}
}

func TestClaimNextDueSuccess(t *testing.T) {
	now := fixedNow()
	returnedToken := validClaimToken()
	lease := now.Add(2 * time.Minute)
	q := single(fakeRow{
		id: 1, eventID: validEventID(), deviceID: 1, userID: 1, state: "pending",
		maxAttempts: 3, nextAttemptAt: &lease, expiresAt: now.Add(time.Hour),
		createdAt: now, updatedAt: now, claimToken: &returnedToken,
	})
	p := &Postgres{DB: q}
	entry, ok, err := p.ClaimNextDue(context.Background(), now, 2*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	sql := q.calls[0].sql
	if !strings.Contains(sql, "FOR UPDATE SKIP LOCKED") || !strings.Contains(sql, "ORDER BY next_attempt_at") ||
		!strings.Contains(sql, "WHERE state = 'pending' AND next_attempt_at <= $1") {
		t.Fatalf("expected a SKIP LOCKED claim-next-due statement, got %q", sql)
	}
	// args: [1]=now, [2]=freshly generated token, [3]=lease expiry
	generatedToken, ok := q.calls[0].args[1].(string)
	if !ok || !claimTokenPattern.MatchString(generatedToken) {
		t.Fatalf("expected a well-formed generated claim token argument, got %#v", q.calls[0].args[1])
	}
	if !entry.Claimed() {
		t.Fatal("expected the returned entry to be claimed")
	}
}

func TestClaimNextDueNoDueEntry(t *testing.T) {
	q := single(fakeRow{err: pgx.ErrNoRows})
	p := &Postgres{DB: q}
	entry, ok, err := p.ClaimNextDue(context.Background(), fixedNow(), time.Minute)
	if err != nil {
		t.Fatalf("expected a nil error for an empty queue, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false when nothing is due")
	}
	if entry.ID != 0 {
		t.Fatalf("expected a zero-value Entry, got %+v", entry)
	}
}

func TestClaimNextDueDatabaseErrorFailsClosed(t *testing.T) {
	q := single(fakeRow{err: errors.New("connection reset")})
	p := &Postgres{DB: q}
	if _, ok, err := p.ClaimNextDue(context.Background(), fixedNow(), time.Minute); err != ErrUnavailable || ok {
		t.Fatalf("expected (false, ErrUnavailable), got (%v, %v)", ok, err)
	}
}

// --- ClaimSpecific ---

func TestClaimSpecificUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.ClaimSpecific(context.Background(), fixedNow(), 1, time.Minute); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestClaimSpecificRejectsInvalidInputBeforeQuerying(t *testing.T) {
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.ClaimSpecific(context.Background(), time.Time{}, 1, time.Minute); err != ErrInput {
		t.Fatalf("expected ErrInput for zero now, got %v", err)
	}
	if _, err := p.ClaimSpecific(context.Background(), fixedNow(), 0, time.Minute); err != ErrInput {
		t.Fatalf("expected ErrInput for zero id, got %v", err)
	}
	if _, err := p.ClaimSpecific(context.Background(), fixedNow(), 1, 0); err != ErrInput {
		t.Fatalf("expected ErrInput for zero lease duration, got %v", err)
	}
	if len(q.calls) != 0 {
		t.Fatal("invalid input must never reach the database")
	}
}

func TestClaimSpecificSuccess(t *testing.T) {
	now := fixedNow()
	returnedToken := validClaimToken()
	lease := now.Add(time.Minute)
	q := single(fakeRow{id: 5, eventID: validEventID(), state: "pending", maxAttempts: 3, nextAttemptAt: &lease, expiresAt: now.Add(time.Hour), createdAt: now, updatedAt: now, claimToken: &returnedToken})
	p := &Postgres{DB: q}
	entry, err := p.ClaimSpecific(context.Background(), now, 5, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := q.calls[0].sql
	if strings.Contains(sql, "SKIP LOCKED") {
		t.Fatalf("expected ClaimSpecific to never use SKIP LOCKED (single-target UPDATE), got %q", sql)
	}
	if !strings.Contains(sql, "WHERE id = $1 AND state = 'pending' AND next_attempt_at <= $2") {
		t.Fatalf("expected a pending-and-due-guarded specific claim, got %q", sql)
	}
	if !entry.Claimed() {
		t.Fatal("expected the returned entry to be claimed")
	}
}

func TestClaimSpecificNotFound(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, fakeRow{err: pgx.ErrNoRows}}}
	p := &Postgres{DB: q}
	if _, err := p.ClaimSpecific(context.Background(), fixedNow(), 999, time.Minute); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClaimSpecificConflictWhenNotDueOrAlreadyClaimed(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, boolRow{}}}
	p := &Postgres{DB: q}
	if _, err := p.ClaimSpecific(context.Background(), fixedNow(), 5, time.Minute); err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// --- ReleaseClaim ---

func TestReleaseClaimUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.ReleaseClaim(context.Background(), fixedNow(), 1, validClaimToken()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestReleaseClaimRejectsInvalidInputBeforeQuerying(t *testing.T) {
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.ReleaseClaim(context.Background(), time.Time{}, 1, validClaimToken()); err != ErrInput {
		t.Fatalf("expected ErrInput for zero now, got %v", err)
	}
	if _, err := p.ReleaseClaim(context.Background(), fixedNow(), 0, validClaimToken()); err != ErrInput {
		t.Fatalf("expected ErrInput for zero id, got %v", err)
	}
	if _, err := p.ReleaseClaim(context.Background(), fixedNow(), 1, "not-a-valid-token"); err != ErrInput {
		t.Fatalf("expected ErrInput for a malformed claim token, got %v", err)
	}
	if len(q.calls) != 0 {
		t.Fatal("invalid input must never reach the database")
	}
}

func TestReleaseClaimSuccess(t *testing.T) {
	now := fixedNow()
	q := single(fakeRow{id: 1, eventID: validEventID(), state: "pending", maxAttempts: 3, nextAttemptAt: &now, expiresAt: now.Add(time.Hour), createdAt: now, updatedAt: now})
	p := &Postgres{DB: q}
	token := validClaimToken()
	entry, err := p.ReleaseClaim(context.Background(), now, 1, token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := q.calls[0].sql
	if !strings.Contains(sql, "SET claim_token = NULL, next_attempt_at = $2") ||
		!strings.Contains(sql, "WHERE id = $1 AND state = 'pending' AND claim_token = $3::uuid") {
		t.Fatalf("unexpected statement shape: %q", sql)
	}
	if q.calls[0].args[2] != token {
		t.Fatalf("expected the claim token to travel as a bound parameter, got %#v", q.calls[0].args[2])
	}
	if entry.Claimed() {
		t.Fatal("expected claim_token to be cleared on the returned entry")
	}
	if entry.AttemptCount != 0 {
		t.Fatalf("expected ReleaseClaim to never touch attempt_count, got %+v", entry)
	}
}

func TestReleaseClaimConflictOnStaleToken(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, boolRow{}}}
	p := &Postgres{DB: q}
	if _, err := p.ReleaseClaim(context.Background(), fixedNow(), 5, validClaimToken()); err != ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestReleaseClaimNotFound(t *testing.T) {
	q := &fakeQuerier{rows: []pgx.Row{fakeRow{err: pgx.ErrNoRows}, fakeRow{err: pgx.ErrNoRows}}}
	p := &Postgres{DB: q}
	if _, err := p.ReleaseClaim(context.Background(), fixedNow(), 999, validClaimToken()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- newClaimToken ---

func TestNewClaimTokenWellFormedAndDistinct(t *testing.T) {
	a := newClaimToken()
	b := newClaimToken()
	if !claimTokenPattern.MatchString(a) || !claimTokenPattern.MatchString(b) {
		t.Fatalf("expected both generated tokens to match claimTokenPattern, got %q and %q", a, b)
	}
	if a == b {
		t.Fatal("expected two consecutive calls to produce distinct tokens")
	}
}

func TestSafeDatabaseErrorClassificationNeverMapsCheckOrForeignKeyViolationSpecially(t *testing.T) {
	secret := "postgres://user:VERY-SECRET-PASSWORD@10.0.0.5/private-db: connection refused"
	cases := []struct {
		err  error
		want error
	}{
		{errors.New(secret), ErrUnavailable},
		{&pgconn.PgError{Code: "23514", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "23503", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "23505", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "22001", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "22023", Message: secret}, ErrInput},
		{&pgconn.PgError{Code: "40001", Message: secret}, ErrUnavailable},
	}
	for _, tc := range cases {
		got := safeDB(tc.err)
		if got != tc.want {
			t.Fatalf("safeDB(%v) = %v, want %v", tc.err, got, tc.want)
		}
		if strings.Contains(got.Error(), "SECRET") || strings.Contains(got.Error(), "10.0.0.5") {
			t.Fatalf("database error leaked into a safe error: %v", got)
		}
	}
}

func TestOpenRejectsNonLocalOrInvalid(t *testing.T) {
	for _, url := range []string{"", "not-a-url", "postgres://user:secret@remote.invalid/db", "postgres://user:secret@127.0.0.1/db?host=remote.invalid"} {
		_, closeDB, err := Open(context.Background(), url)
		if closeDB != nil {
			closeDB()
		}
		if err != ErrUnavailable {
			t.Fatalf("expected ErrUnavailable for %q, got %v", url, err)
		}
	}
}

func TestOpenRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, closeDB, err := Open(ctx, "postgres://fixture:secret@127.0.0.1:1/fixture?sslmode=disable")
	if closeDB != nil {
		closeDB()
	}
	if err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable for a canceled dial, got %v", err)
	}
}

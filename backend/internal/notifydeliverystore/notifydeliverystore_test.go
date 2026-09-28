package notifydeliverystore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"greenwich-fire-responder/backend/internal/notifydelivery"
)

func validEventID() string {
	return "evt_" + strings.Repeat("a", 64)
}

func strPtr(s string) *string { return &s }

func validDelivery() notifydelivery.Delivery {
	return notifydelivery.Delivery{
		OutboxID:      1,
		EventID:       validEventID(),
		DeviceID:      1,
		Outcome:       notifydelivery.OutcomeSent,
		AttemptNumber: 1,
	}
}

// fakeRow implements pgx.Row over a fixed row of the exact 8-column shape
// Record's RETURNING clause produces, or a fixed error.
type fakeRow struct {
	id, outboxID, deviceID int64
	eventID, outcome       string
	attemptNumber          int
	errorCode              *string
	createdAt              time.Time
	err                    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.id
	*dest[1].(*int64) = r.outboxID
	*dest[2].(*string) = r.eventID
	*dest[3].(*int64) = r.deviceID
	*dest[4].(*string) = r.outcome
	*dest[5].(*int) = r.attemptNumber
	*dest[6].(**string) = r.errorCode
	*dest[7].(*time.Time) = r.createdAt
	return nil
}

// fakeQuerier captures the exact SQL and arguments passed to QueryRow, so
// tests can prove every value travels as a bound parameter, never
// interpolated into the query text (mirroring every sibling store's
// identical fakeQuerier pattern).
type fakeQuerier struct {
	sql  string
	args []any
	row  pgx.Row
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql = sql
	q.args = args
	return q.row
}

func TestRecordUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.Record(context.Background(), validDelivery()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRecordRejectsInvalidInputBeforeQuerying(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*notifydelivery.Delivery)
	}{
		{"zero outbox id", func(d *notifydelivery.Delivery) { d.OutboxID = 0 }},
		{"negative outbox id", func(d *notifydelivery.Delivery) { d.OutboxID = -1 }},
		{"zero device id", func(d *notifydelivery.Delivery) { d.DeviceID = 0 }},
		{"invalid event id", func(d *notifydelivery.Delivery) { d.EventID = "not-an-event-id" }},
		{"invalid outcome", func(d *notifydelivery.Delivery) { d.Outcome = "pending" }},
		{"zero attempt number", func(d *notifydelivery.Delivery) { d.AttemptNumber = 0 }},
		{"negative attempt number", func(d *notifydelivery.Delivery) { d.AttemptNumber = -1 }},
		{"malformed error code", func(d *notifydelivery.Delivery) { d.ErrorCode = strPtr("BAD CODE") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := validDelivery()
			c.mutate(&d)
			q := &fakeQuerier{}
			p := &Postgres{DB: q}
			if _, err := p.Record(context.Background(), d); err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if q.sql != "" {
				t.Fatal("invalid input must never reach the database")
			}
		})
	}
}

func TestRecordSuccessInsertsAndReturnsDelivery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := &fakeQuerier{row: fakeRow{
		id: 1, outboxID: 7, eventID: validEventID(), deviceID: 3, outcome: "sent",
		attemptNumber: 1, errorCode: nil, createdAt: now,
	}}
	p := &Postgres{DB: q}
	d := validDelivery()
	d.OutboxID = 7
	d.DeviceID = 3
	recorded, err := p.Record(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q.sql, "INSERT INTO notification_deliveries") || !strings.Contains(q.sql, "RETURNING") {
		t.Fatalf("expected a single INSERT ... RETURNING statement, got %q", q.sql)
	}
	if strings.Contains(q.sql, "'") {
		t.Fatal("query text must contain only placeholders, never an inlined value")
	}
	if len(q.args) != 6 {
		t.Fatalf("expected 6 bound parameters, got %d", len(q.args))
	}
	if recorded.ID != 1 || recorded.OutboxID != 7 || recorded.DeviceID != 3 || recorded.Outcome != notifydelivery.OutcomeSent || recorded.AttemptNumber != 1 {
		t.Fatalf("unexpected recorded delivery: %+v", recorded)
	}
	if !recorded.CreatedAt.Equal(now) {
		t.Fatalf("expected the database-generated created_at to round-trip, got %v", recorded.CreatedAt)
	}
	if recorded.ErrorCode != nil {
		t.Fatalf("expected nil error_code to round-trip, got %+v", recorded.ErrorCode)
	}
}

func TestRecordEveryOutcomeSucceeds(t *testing.T) {
	now := time.Now()
	for _, o := range []notifydelivery.Outcome{
		notifydelivery.OutcomeSent, notifydelivery.OutcomeFailed, notifydelivery.OutcomeExpired,
		notifydelivery.OutcomeCanceled, notifydelivery.OutcomeDeadLetter, notifydelivery.OutcomeUnauthorized,
	} {
		t.Run(string(o), func(t *testing.T) {
			q := &fakeQuerier{row: fakeRow{id: 1, outboxID: 1, eventID: validEventID(), deviceID: 1, outcome: string(o), attemptNumber: 1, createdAt: now}}
			p := &Postgres{DB: q}
			d := validDelivery()
			d.Outcome = o
			recorded, err := p.Record(context.Background(), d)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if recorded.Outcome != o {
				t.Fatalf("expected outcome %q to round-trip, got %q", o, recorded.Outcome)
			}
			if q.args[3] != string(o) {
				t.Fatalf("expected the outcome argument to travel as its string value, got %#v", q.args[3])
			}
		})
	}
}

func TestRecordValidErrorCodeRoundTrips(t *testing.T) {
	now := time.Now()
	code := "quiet_hours_suppressed"
	q := &fakeQuerier{row: fakeRow{id: 1, outboxID: 1, eventID: validEventID(), deviceID: 1, outcome: "canceled", attemptNumber: 1, errorCode: &code, createdAt: now}}
	p := &Postgres{DB: q}
	d := validDelivery()
	d.Outcome = notifydelivery.OutcomeCanceled
	d.ErrorCode = &code
	recorded, err := p.Record(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorded.ErrorCode == nil || *recorded.ErrorCode != code {
		t.Fatalf("expected error_code to round-trip, got %+v", recorded.ErrorCode)
	}
	if q.args[5] == nil || *(q.args[5].(*string)) != code {
		t.Fatalf("expected the error_code argument to travel as a bound *string, got %#v", q.args[5])
	}
}

func TestRecordForeignKeyViolationIsInput(t *testing.T) {
	// A 23503 here only ever means (outbox_id, event_id, device_id) does
	// not match a real, internally consistent notification_outbox row --
	// there is no ownership meaning to translate, unlike
	// notifyconsentstore.Record's or notifyoutboxstore.Enqueue's own
	// composite FKs. This package defines no ErrForbidden at all.
	q := &fakeQuerier{row: fakeRow{err: &pgconn.PgError{Code: "23503", Message: "violates foreign key constraint"}}}
	p := &Postgres{DB: q}
	if _, err := p.Record(context.Background(), validDelivery()); err != ErrInput {
		t.Fatalf("expected ErrInput, got %v", err)
	}
}

func TestRecordDatabaseErrorFailsClosed(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: errors.New("connection reset")}}
	p := &Postgres{DB: q}
	if _, err := p.Record(context.Background(), validDelivery()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestSafeDatabaseErrorClassification(t *testing.T) {
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
		// The immutability trigger's own code (55000) is deliberately NOT
		// specially classified: it is never actually reachable through
		// this package's own API (Record never issues an UPDATE/DELETE/
		// TRUNCATE), so it falls through to the generic ErrUnavailable
		// like any other unrecognized code.
		{&pgconn.PgError{Code: "55000", Message: secret}, ErrUnavailable},
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

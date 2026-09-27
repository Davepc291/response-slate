package notifyconsentstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyconsent"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

// fakeConsentRow implements pgx.Row over a fixed row of the exact shape
// Record's RETURNING clause produces, or a fixed error.
type fakeConsentRow struct {
	id, userID, deviceID int64
	event                string
	createdAt            time.Time
	err                  error
}

func (r fakeConsentRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.id
	*dest[1].(*int64) = r.userID
	*dest[2].(*int64) = r.deviceID
	*dest[3].(*string) = r.event
	*dest[4].(*time.Time) = r.createdAt
	return nil
}

// fakeEventRow implements pgx.Row over the single event column Current's
// own lookup query shape produces.
type fakeEventRow struct {
	event string
	err   error
}

func (r fakeEventRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.event
	return nil
}

// fakeQuerier captures the exact SQL and arguments passed to QueryRow, so
// tests can prove every value travels as a bound parameter, never
// interpolated into the query text (mirroring notifydevicestore_test.go's
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
	if _, err := p.Record(context.Background(), time.Now(), 1, 1, notifyconsent.EventGranted); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestRecordRejectsInvalidInputBeforeQuerying(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		userID   identity.UserID
		deviceID notifydevices.DeviceID
		now      time.Time
		event    notifyconsent.Event
	}{
		{"zero user id", 0, 1, now, notifyconsent.EventGranted},
		{"negative user id", -1, 1, now, notifyconsent.EventGranted},
		{"zero device id", 1, 0, now, notifyconsent.EventGranted},
		{"negative device id", 1, -1, now, notifyconsent.EventGranted},
		{"zero time", 1, 1, time.Time{}, notifyconsent.EventGranted},
		{"invalid event", 1, 1, now, notifyconsent.Event("subscribed")},
		{"empty event", 1, 1, now, notifyconsent.Event("")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &fakeQuerier{}
			p := &Postgres{DB: q}
			if _, err := p.Record(context.Background(), c.now, c.userID, c.deviceID, c.event); err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if q.sql != "" {
				t.Fatal("invalid input must never reach the database")
			}
		})
	}
}

func TestRecordSuccessInsertsAndReturnsConsent(t *testing.T) {
	now := time.Now()
	q := &fakeQuerier{row: fakeConsentRow{id: 1, userID: 3, deviceID: 7, event: "granted", createdAt: now}}
	p := &Postgres{DB: q}
	c, err := p.Record(context.Background(), now, 3, 7, notifyconsent.EventGranted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q.sql, "INSERT INTO notification_consents") || !strings.Contains(q.sql, "RETURNING") {
		t.Fatalf("expected a single INSERT ... RETURNING statement, got %q", q.sql)
	}
	if strings.Contains(q.sql, "'") {
		t.Fatal("query text must contain only placeholders, never an inlined value")
	}
	if len(q.args) != 4 {
		t.Fatalf("expected 4 bound parameters, got %d", len(q.args))
	}
	if q.args[2] != "granted" {
		t.Fatalf("expected the event argument to travel as the string value, got %#v", q.args[2])
	}
	if c.ID != 1 || c.UserID != 3 || c.DeviceID != 7 || c.Event != notifyconsent.EventGranted || !c.CreatedAt.Equal(now) {
		t.Fatalf("unexpected consent: %+v", c)
	}
}

func TestRecordAllowsRepeatedConsecutiveEventWithoutDeduplicating(t *testing.T) {
	// Record has no read-before-write step of any kind: every validated
	// call reaches the same single INSERT statement regardless of what the
	// device's prior consent history looked like, so a repeated granted ->
	// granted event is never specially detected or collapsed here.
	now := time.Now()
	q := &fakeQuerier{row: fakeConsentRow{id: 2, userID: 3, deviceID: 7, event: "granted", createdAt: now}}
	p := &Postgres{DB: q}
	c, err := p.Record(context.Background(), now, 3, 7, notifyconsent.EventGranted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.ID != 2 {
		t.Fatalf("expected the freshly inserted row's own id, got %v", c.ID)
	}
}

func TestRecordForeignKeyViolationIsForbidden(t *testing.T) {
	// The composite FK (device_id, user_id) REFERENCES notification_devices
	// (id, user_id) is the only FK this INSERT can violate: a 23503 here can
	// only mean deviceID does not belong to userID.
	q := &fakeQuerier{row: fakeConsentRow{err: &pgconn.PgError{Code: "23503", Message: "violates foreign key constraint"}}}
	p := &Postgres{DB: q}
	if _, err := p.Record(context.Background(), time.Now(), 1, 1, notifyconsent.EventGranted); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestSafeDatabaseErrorClassificationNeverMapsForeignKeyViolationToForbidden(t *testing.T) {
	// safeDB itself (as opposed to Record's own local translation) must
	// stay generic: this proves a raw 23503 reaching safeDB directly (as
	// Current's query would, since it has no FK of its own to violate) is
	// classified as ErrInput, never ErrForbidden.
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

func TestRecordDatabaseErrorFailsClosed(t *testing.T) {
	q := &fakeQuerier{row: fakeConsentRow{err: errors.New("connection reset")}}
	p := &Postgres{DB: q}
	if _, err := p.Record(context.Background(), time.Now(), 1, 1, notifyconsent.EventGranted); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestCurrentUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, _, err := p.Current(context.Background(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestCurrentRejectsInvalidDeviceID(t *testing.T) {
	for _, id := range []notifydevices.DeviceID{0, -1} {
		q := &fakeQuerier{}
		p := &Postgres{DB: q}
		if _, _, err := p.Current(context.Background(), id); err != ErrInput {
			t.Fatalf("expected ErrInput for device id %v, got %v", id, err)
		}
		if q.sql != "" {
			t.Fatal("an invalid device id must never reach the database")
		}
	}
}

func TestCurrentGrantedReturnsTrueTrueNil(t *testing.T) {
	q := &fakeQuerier{row: fakeEventRow{event: "granted"}}
	p := &Postgres{DB: q}
	granted, ok, err := p.Current(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || !granted {
		t.Fatalf("expected (true, true, nil), got (%v, %v, %v)", granted, ok, err)
	}
	if !strings.Contains(q.sql, "ORDER BY created_at DESC, id DESC") {
		t.Fatalf("expected deterministic newest-first ordering, got %q", q.sql)
	}
	if len(q.args) != 1 || q.args[0] != int64(7) {
		t.Fatalf("expected exactly one bound device_id argument (7), got %v", q.args)
	}
}

func TestCurrentRevokedReturnsFalseTrueNil(t *testing.T) {
	q := &fakeQuerier{row: fakeEventRow{event: "revoked"}}
	p := &Postgres{DB: q}
	granted, ok, err := p.Current(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted || !ok {
		t.Fatalf("expected (false, true, nil), got (%v, %v, %v)", granted, ok, err)
	}
}

func TestCurrentNoConsentHistoryReturnsFalseFalseNil(t *testing.T) {
	q := &fakeQuerier{row: fakeEventRow{err: pgx.ErrNoRows}}
	p := &Postgres{DB: q}
	granted, ok, err := p.Current(context.Background(), 7)
	if err != nil {
		t.Fatalf("expected a nil error for no consent history, got %v", err)
	}
	if granted || ok {
		t.Fatalf("expected (false, false, nil), got (%v, %v, %v)", granted, ok, err)
	}
}

func TestCurrentDatabaseErrorFailsClosed(t *testing.T) {
	q := &fakeQuerier{row: fakeEventRow{err: errors.New("connection reset")}}
	p := &Postgres{DB: q}
	if _, ok, err := p.Current(context.Background(), 7); err != ErrUnavailable || ok {
		t.Fatalf("expected (false, ErrUnavailable), got (%v, %v)", ok, err)
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

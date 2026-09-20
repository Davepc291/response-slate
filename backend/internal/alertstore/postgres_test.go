package alertstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"greenwich-fire-responder/backend/internal/alerts"
)

// fakeRow implements pgx.Row for a fixed 3-column
// (audit_id, event_created, event_suppressed) result, or a fixed error.
type fakeRow struct {
	auditID    int64
	created    bool
	suppressed bool
	err        error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.auditID
	*dest[1].(*bool) = r.created
	*dest[2].(*bool) = r.suppressed
	return nil
}

// fakeQuerier captures the exact SQL and arguments passed to QueryRow, so
// tests can prove every value travels as a bound parameter, never
// interpolated into the query text.
type fakeQuerier struct {
	sql  string
	args []any
	row  fakeRow
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.sql = sql
	q.args = args
	return q.row
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

func TestRecordDetectionSuccessTone(t *testing.T) {
	rec := baseRecord(t)
	q := &fakeQuerier{row: fakeRow{auditID: 7, created: true}}
	p := &Postgres{DB: q}
	result, err := p.RecordDetection(context.Background(), 5*time.Minute, rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AuditID != 7 || !result.EventCreated || result.Suppressed {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.Contains(q.sql, "record_alert_detection") {
		t.Fatalf("expected the sole write path function to be invoked: %q", q.sql)
	}
	if len(q.args) != 21 {
		t.Fatalf("expected 21 bound parameters, got %d", len(q.args))
	}
	// The 21st positional argument is the cooldown, in whole seconds.
	if q.args[20] != int64(300) {
		t.Fatalf("expected cooldown of 300 seconds, got %v", q.args[20])
	}
}

func TestRecordDetectionSuccessKeyword(t *testing.T) {
	b, err := alerts.NewBuilder(alerts.Limits{MaxExpiryWindow: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ev, err := b.New(alerts.Input{
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Minute),
		SourceKind:    alerts.SourceShadowReplay,
		Channel:       alerts.ChannelCH2B,
		TGID:          57202,
		DetectorKind:  alerts.DetectorKeyword,
		KeywordListID: "structure-fire",
		Evidence:      alerts.Evidence{SourceIdentity: strings.Repeat("b", 64)},
		State:         alerts.StateAmbiguous,
		Label:         "structure fire",
	})
	if err != nil {
		t.Fatal(err)
	}
	occ := 2
	rec := Record{
		CreatedAt:          ev.CreatedAt,
		SourceKind:         ev.SourceKind,
		Channel:            ev.Channel,
		TGID:               ev.TGID,
		DetectorKind:       ev.DetectorKind,
		ConfigID:           ev.KeywordListID,
		State:              ev.State,
		Reason:             "keyword_ambiguous_context",
		KeywordOccurrences: &occ,
		Evidence:           alerts.Evidence{SourceIdentity: strings.Repeat("b", 64)},
		Event:              &ev,
	}
	q := &fakeQuerier{row: fakeRow{auditID: 1, created: true}}
	p := &Postgres{DB: q}
	if _, err := p.RecordDetection(context.Background(), 0, rec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.args[20] != int64(0) {
		t.Fatalf("expected zero cooldown to be an explicit, valid, passed-through value, got %v", q.args[20])
	}
}

func TestRecordDetectionDuplicateRetry(t *testing.T) {
	rec := baseRecord(t)
	q := &fakeQuerier{row: fakeRow{auditID: 9, created: false, suppressed: false}}
	p := &Postgres{DB: q}
	result, err := p.RecordDetection(context.Background(), time.Minute, rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Duplicate() {
		t.Fatalf("expected a duplicate/idempotent-retry result: %+v", result)
	}
}

func TestRecordDetectionSuppressedByCooldown(t *testing.T) {
	rec := baseRecord(t)
	q := &fakeQuerier{row: fakeRow{auditID: 12, created: false, suppressed: true}}
	p := &Postgres{DB: q}
	result, err := p.RecordDetection(context.Background(), time.Minute, rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Duplicate() || !result.Suppressed {
		t.Fatalf("expected a cooldown-suppressed, non-duplicate result: %+v", result)
	}
}

func TestRecordDetectionRejectsInvalidRecordBeforeQuerying(t *testing.T) {
	rec := baseRecord(t)
	rec.TGID = 57202 // mismatched channel/tgid
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.RecordDetection(context.Background(), time.Minute, rec); err != ErrInput {
		t.Fatalf("expected ErrInput, got %v", err)
	}
	if q.sql != "" {
		t.Fatal("an invalid record must never reach the database")
	}
}

func TestRecordDetectionRejectsInvalidCooldown(t *testing.T) {
	rec := baseRecord(t)
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.RecordDetection(context.Background(), -time.Second, rec); err != ErrInput {
		t.Fatalf("expected ErrInput for negative cooldown, got %v", err)
	}
	if q.sql != "" {
		t.Fatal("an invalid cooldown must never reach the database")
	}
}

func TestRecordDetectionUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.RecordDetection(context.Background(), time.Minute, baseRecord(t)); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable for an unconfigured store, got %v", err)
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
		{pgx.ErrNoRows, ErrUnavailable},
	}
	for _, tc := range cases {
		rec := baseRecord(t)
		q := &fakeQuerier{row: fakeRow{err: tc.err}}
		p := &Postgres{DB: q}
		_, err := p.RecordDetection(context.Background(), time.Minute, rec)
		if err != tc.want {
			t.Fatalf("classify(%v) = %v, want %v", tc.err, err, tc.want)
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "10.0.0.5") {
			t.Fatalf("database error leaked into a safe error: %v", err)
		}
	}
}

func TestRecordDetectionSQLInjectionShapedLabelsAreInertParameters(t *testing.T) {
	rec := baseRecord(t)
	injected := "quick'; DROP TABLE alert_events; --"
	rec.ConfigID = injected
	rec.Event.ToneSetID = injected
	// The store-level Validate rejects this shape outright (config_id must
	// match the allow-listed id pattern), proving it never reaches SQL text.
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.RecordDetection(context.Background(), time.Minute, rec); err != ErrInput {
		t.Fatalf("expected SQL-injection-shaped config_id to be rejected, got %v", err)
	}
	if q.sql != "" {
		t.Fatal("rejected input must never reach the database")
	}

	// Even a value that legitimately reaches the query travels as a bound
	// parameter, never concatenated into the SQL text itself.
	rec2 := baseRecord(t)
	q2 := &fakeQuerier{row: fakeRow{auditID: 1, created: true}}
	p2 := &Postgres{DB: q2}
	if _, err := p2.RecordDetection(context.Background(), time.Minute, rec2); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q2.sql, "'") {
		t.Fatalf("query text must contain only placeholders, never an inlined value: %q", q2.sql)
	}
}

func TestRecordDetectionNeverPassesRawTranscriptOrAudio(t *testing.T) {
	// Record has no field capable of holding transcript text or raw audio
	// bytes; this test documents that boundary by exercising every field a
	// caller can set and confirming none of them is forwarded as anything
	// but a bounded scalar/array parameter to the sole write-path function.
	rec := baseRecord(t)
	q := &fakeQuerier{row: fakeRow{auditID: 1, created: true}}
	p := &Postgres{DB: q}
	if _, err := p.RecordDetection(context.Background(), time.Minute, rec); err != nil {
		t.Fatal(err)
	}
	for _, arg := range q.args {
		if s, ok := arg.(string); ok && len(s) > 256 {
			t.Fatalf("unexpectedly large string parameter (possible unbounded content): %d bytes", len(s))
		}
		if sp, ok := arg.(*string); ok && sp != nil && len(*sp) > 256 {
			t.Fatalf("unexpectedly large string parameter (possible unbounded content): %d bytes", len(*sp))
		}
	}
}

package notifyprefsstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyprefs"
)

func strPtr(s string) *string     { return &s }
func floatPtr(f float64) *float64 { return &f }
func durPtr(d time.Duration) *time.Duration {
	return &d
}

// fakeRow implements pgx.Row over a fixed row of the exact 11-column shape
// both Get's SELECT and Upsert's RETURNING clause produce, or a fixed
// error.
type fakeRow struct {
	userID                     int64
	enabled                    bool
	channels                   []string
	toneSetIDs, keywordListIDs []string
	minConfidence              *float64
	quietStart, quietEnd       pgtype.Time
	quietTZ                    *string
	createdAt, updatedAt       time.Time
	err                        error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.userID
	*dest[1].(*bool) = r.enabled
	*dest[2].(*[]string) = r.channels
	*dest[3].(*[]string) = r.toneSetIDs
	*dest[4].(*[]string) = r.keywordListIDs
	*dest[5].(**float64) = r.minConfidence
	*dest[6].(*pgtype.Time) = r.quietStart
	*dest[7].(*pgtype.Time) = r.quietEnd
	*dest[8].(**string) = r.quietTZ
	*dest[9].(*time.Time) = r.createdAt
	*dest[10].(*time.Time) = r.updatedAt
	return nil
}

// fakeQuerier captures the exact SQL and arguments passed to QueryRow, so
// tests can prove every value travels as a bound parameter, never
// interpolated into the query text (mirroring notifyconsentstore_test.go's
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

func validPreferences() notifyprefs.Preferences {
	return notifyprefs.Preferences{
		UserID:             3,
		Enabled:            true,
		Channels:           []notifyprefs.Channel{notifyprefs.ChannelCH1A, notifyprefs.ChannelCH2B},
		ToneSetIDs:         []string{"engine-1"},
		KeywordListIDs:     []string{"structure-fire"},
		MinConfidence:      floatPtr(0.75),
		QuietHoursStart:    durPtr(22 * time.Hour),
		QuietHoursEnd:      durPtr(6 * time.Hour),
		QuietHoursTimezone: strPtr("America/New_York"),
	}
}

func TestGetUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, _, err := p.Get(context.Background(), 1); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestGetRejectsInvalidUserID(t *testing.T) {
	for _, id := range []identity.UserID{0, -1} {
		q := &fakeQuerier{}
		p := &Postgres{DB: q}
		if _, _, err := p.Get(context.Background(), id); err != ErrInput {
			t.Fatalf("expected ErrInput for user id %v, got %v", id, err)
		}
		if q.sql != "" {
			t.Fatal("an invalid user id must never reach the database")
		}
	}
}

func TestGetNoRowReturnsFalseFalseNil(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}
	p := &Postgres{DB: q}
	prefs, ok, err := p.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("expected a nil error for no row, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a user with no preferences row")
	}
	if prefs.UserID != 0 || prefs.Enabled || len(prefs.Channels) != 0 || len(prefs.ToneSetIDs) != 0 ||
		len(prefs.KeywordListIDs) != 0 || prefs.MinConfidence != nil || prefs.QuietHoursStart != nil ||
		prefs.QuietHoursEnd != nil || prefs.QuietHoursTimezone != nil {
		t.Fatalf("expected a zero-value Preferences, got %+v", prefs)
	}
}

func TestGetRoundTripsAllFieldsIncludingQuietHours(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := &fakeQuerier{row: fakeRow{
		userID: 7, enabled: true,
		channels: []string{"CH1A", "CH3B"}, toneSetIDs: []string{"engine-1"}, keywordListIDs: []string{"mvc"},
		minConfidence: floatPtr(0.9),
		quietStart:    pgtype.Time{Microseconds: int64((22 * time.Hour) / time.Microsecond), Valid: true},
		quietEnd:      pgtype.Time{Microseconds: int64((6 * time.Hour) / time.Microsecond), Valid: true},
		quietTZ:       strPtr("America/New_York"),
		createdAt:     now, updatedAt: now,
	}}
	p := &Postgres{DB: q}
	prefs, ok, err := p.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for an existing row")
	}
	if prefs.UserID != 7 || !prefs.Enabled {
		t.Fatalf("unexpected identity/enabled fields: %+v", prefs)
	}
	if len(prefs.Channels) != 2 || prefs.Channels[0] != notifyprefs.ChannelCH1A || prefs.Channels[1] != notifyprefs.ChannelCH3B {
		t.Fatalf("unexpected channels: %+v", prefs.Channels)
	}
	if len(prefs.ToneSetIDs) != 1 || prefs.ToneSetIDs[0] != "engine-1" {
		t.Fatalf("unexpected tone set ids: %+v", prefs.ToneSetIDs)
	}
	if len(prefs.KeywordListIDs) != 1 || prefs.KeywordListIDs[0] != "mvc" {
		t.Fatalf("unexpected keyword list ids: %+v", prefs.KeywordListIDs)
	}
	if prefs.MinConfidence == nil || *prefs.MinConfidence != 0.9 {
		t.Fatalf("unexpected min confidence: %+v", prefs.MinConfidence)
	}
	if prefs.QuietHoursStart == nil || *prefs.QuietHoursStart != 22*time.Hour {
		t.Fatalf("unexpected quiet hours start: %+v", prefs.QuietHoursStart)
	}
	if prefs.QuietHoursEnd == nil || *prefs.QuietHoursEnd != 6*time.Hour {
		t.Fatalf("unexpected quiet hours end: %+v", prefs.QuietHoursEnd)
	}
	if prefs.QuietHoursTimezone == nil || *prefs.QuietHoursTimezone != "America/New_York" {
		t.Fatalf("unexpected quiet hours timezone: %+v", prefs.QuietHoursTimezone)
	}
	if !prefs.CreatedAt.Equal(now) || !prefs.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected timestamps: %+v", prefs)
	}
}

func TestGetRoundTripsNoQuietHoursAsNil(t *testing.T) {
	now := time.Now()
	q := &fakeQuerier{row: fakeRow{
		userID: 1, channels: []string{}, toneSetIDs: []string{}, keywordListIDs: []string{},
		createdAt: now, updatedAt: now,
		// quietStart/quietEnd left as zero-value pgtype.Time{Valid: false}
	}}
	p := &Postgres{DB: q}
	prefs, ok, err := p.Get(context.Background(), 1)
	if err != nil || !ok {
		t.Fatalf("unexpected: ok=%v err=%v", ok, err)
	}
	if prefs.QuietHoursStart != nil || prefs.QuietHoursEnd != nil {
		t.Fatalf("expected nil quiet hours when not configured, got %+v/%+v", prefs.QuietHoursStart, prefs.QuietHoursEnd)
	}
	if prefs.MinConfidence != nil {
		t.Fatalf("expected nil min confidence, got %+v", prefs.MinConfidence)
	}
	if prefs.Enabled {
		t.Fatal("expected Enabled to be false (fail-closed default)")
	}
}

func TestGetDatabaseErrorFailsClosed(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: errors.New("connection reset")}}
	p := &Postgres{DB: q}
	if _, ok, err := p.Get(context.Background(), 1); err != ErrUnavailable || ok {
		t.Fatalf("expected (false, ErrUnavailable), got (%v, %v)", ok, err)
	}
}

func TestUpsertUnconfiguredStoreFailsClosed(t *testing.T) {
	p := &Postgres{}
	if _, err := p.Upsert(context.Background(), time.Now(), validPreferences()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestUpsertRejectsZeroTimeBeforeQuerying(t *testing.T) {
	q := &fakeQuerier{}
	p := &Postgres{DB: q}
	if _, err := p.Upsert(context.Background(), time.Time{}, validPreferences()); err != ErrInput {
		t.Fatalf("expected ErrInput for a zero-value now, got %v", err)
	}
	if q.sql != "" {
		t.Fatal("a zero-value now must never reach the database")
	}
}

func TestUpsertRejectsInvalidPreferencesBeforeQuerying(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*notifyprefs.Preferences)
	}{
		{"zero user id", func(p *notifyprefs.Preferences) { p.UserID = 0 }},
		{"invalid channel", func(p *notifyprefs.Preferences) { p.Channels = []notifyprefs.Channel{"CH5D"} }},
		{"duplicate channel", func(p *notifyprefs.Preferences) {
			p.Channels = []notifyprefs.Channel{notifyprefs.ChannelCH1A, notifyprefs.ChannelCH1A}
		}},
		{"invalid tone set id", func(p *notifyprefs.Preferences) { p.ToneSetIDs = []string{"bad id!"} }},
		{"min confidence out of range", func(p *notifyprefs.Preferences) { p.MinConfidence = floatPtr(1.5) }},
		{"unpaired quiet hours", func(p *notifyprefs.Preferences) { p.QuietHoursEnd = nil }},
		{"quiet hours missing timezone", func(p *notifyprefs.Preferences) { p.QuietHoursTimezone = nil }},
		{"invalid timezone", func(p *notifyprefs.Preferences) { p.QuietHoursTimezone = strPtr("Not/AZone") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefs := validPreferences()
			c.mutate(&prefs)
			q := &fakeQuerier{}
			p := &Postgres{DB: q}
			if _, err := p.Upsert(context.Background(), time.Now(), prefs); err != ErrInput {
				t.Fatalf("expected ErrInput, got %v", err)
			}
			if q.sql != "" {
				t.Fatal("invalid preferences must never reach the database")
			}
		})
	}
}

func TestUpsertSuccessInsertsAndReturnsPreferences(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	prefs := validPreferences()
	q := &fakeQuerier{row: fakeRow{
		userID: 3, enabled: true,
		channels: []string{"CH1A", "CH2B"}, toneSetIDs: []string{"engine-1"}, keywordListIDs: []string{"structure-fire"},
		minConfidence: floatPtr(0.75),
		quietStart:    pgtype.Time{Microseconds: int64((22 * time.Hour) / time.Microsecond), Valid: true},
		quietEnd:      pgtype.Time{Microseconds: int64((6 * time.Hour) / time.Microsecond), Valid: true},
		quietTZ:       strPtr("America/New_York"),
		createdAt:     now, updatedAt: now,
	}}
	p := &Postgres{DB: q}
	updated, err := p.Upsert(context.Background(), now, prefs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(q.sql, "INSERT INTO alert_preferences") || !strings.Contains(q.sql, "RETURNING") {
		t.Fatalf("expected a single INSERT ... RETURNING statement, got %q", q.sql)
	}
	if !strings.Contains(q.sql, "ON CONFLICT (user_id) DO UPDATE") {
		t.Fatalf("expected the user_id-keyed upsert conflict target, got %q", q.sql)
	}
	if strings.Contains(q.sql, "'") {
		t.Fatal("query text must contain only placeholders, never an inlined value")
	}
	if updated.UserID != 3 || !updated.Enabled {
		t.Fatalf("unexpected result: %+v", updated)
	}
}

func TestUpsertFullReplaceNeverPatchesCreatedOrUpdatedAt(t *testing.T) {
	// created_at/updated_at must never appear in the DO UPDATE SET list:
	// created_at is preserved by omission, and updated_at is left entirely
	// to migration 000010's own alert_preferences_updated_at trigger,
	// which unconditionally overwrites it with clock_timestamp() on every
	// UPDATE regardless of what any SET clause supplies.
	now := time.Now()
	q := &fakeQuerier{row: fakeRow{userID: 1, createdAt: now, updatedAt: now}}
	p := &Postgres{DB: q}
	if _, err := p.Upsert(context.Background(), now, notifyprefs.Preferences{UserID: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	setClause := q.sql[strings.Index(q.sql, "DO UPDATE SET"):strings.Index(q.sql, "RETURNING")]
	if strings.Contains(setClause, "created_at") {
		t.Fatalf("expected created_at to never appear in the DO UPDATE SET clause, got %q", setClause)
	}
	if strings.Contains(setClause, "updated_at") {
		t.Fatalf("expected updated_at to never appear in the DO UPDATE SET clause (the trigger owns it), got %q", setClause)
	}
	for _, col := range []string{"enabled", "channels", "tone_set_ids", "keyword_list_ids", "min_confidence", "quiet_hours_start", "quiet_hours_end", "quiet_hours_timezone"} {
		if !strings.Contains(setClause, col) {
			t.Fatalf("expected %q to be fully replaced in the DO UPDATE SET clause, got %q", col, setClause)
		}
	}
}

func TestUpsertMinimalPreferencesSendsNonNilEmptyArrays(t *testing.T) {
	// Every array column is NOT NULL DEFAULT '{}'::text[]; a Preferences
	// value with nil Channels/ToneSetIDs/KeywordListIDs must still send a
	// non-nil empty slice, never an attempted SQL NULL.
	now := time.Now()
	q := &fakeQuerier{row: fakeRow{userID: 1, createdAt: now, updatedAt: now}}
	p := &Postgres{DB: q}
	if _, err := p.Upsert(context.Background(), now, notifyprefs.Preferences{UserID: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	channelsArg, ok := q.args[2].([]string)
	if !ok || channelsArg == nil {
		t.Fatalf("expected a non-nil empty []string for channels, got %#v", q.args[2])
	}
	toneArg, ok := q.args[3].([]string)
	if !ok || toneArg == nil {
		t.Fatalf("expected a non-nil empty []string for tone_set_ids, got %#v", q.args[3])
	}
	kwArg, ok := q.args[4].([]string)
	if !ok || kwArg == nil {
		t.Fatalf("expected a non-nil empty []string for keyword_list_ids, got %#v", q.args[4])
	}
}

func TestUpsertQuietHoursArgumentsUsePgtypeTime(t *testing.T) {
	now := time.Now()
	prefs := validPreferences()
	q := &fakeQuerier{row: fakeRow{userID: 3, createdAt: now, updatedAt: now}}
	p := &Postgres{DB: q}
	if _, err := p.Upsert(context.Background(), now, prefs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	startArg, ok := q.args[6].(pgtype.Time)
	if !ok || !startArg.Valid || startArg.Microseconds != int64((22*time.Hour)/time.Microsecond) {
		t.Fatalf("unexpected quiet_hours_start argument: %#v", q.args[6])
	}
	endArg, ok := q.args[7].(pgtype.Time)
	if !ok || !endArg.Valid || endArg.Microseconds != int64((6*time.Hour)/time.Microsecond) {
		t.Fatalf("unexpected quiet_hours_end argument: %#v", q.args[7])
	}
}

func TestUpsertForeignKeyViolationIsInput(t *testing.T) {
	// A 23503 here only ever means user_id does not reference a real users
	// row -- there is no ownership meaning to translate, unlike
	// notifyconsentstore.Record's own composite FK. This is a positive
	// test proving no ErrForbidden-shaped local translation exists: this
	// package does not even define such a sentinel.
	q := &fakeQuerier{row: fakeRow{err: &pgconn.PgError{Code: "23503", Message: "violates foreign key constraint"}}}
	p := &Postgres{DB: q}
	if _, err := p.Upsert(context.Background(), time.Now(), validPreferences()); err != ErrInput {
		t.Fatalf("expected ErrInput, got %v", err)
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

func TestUpsertDatabaseErrorFailsClosed(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: errors.New("connection reset")}}
	p := &Postgres{DB: q}
	if _, err := p.Upsert(context.Background(), time.Now(), validPreferences()); err != ErrUnavailable {
		t.Fatalf("expected ErrUnavailable, got %v", err)
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

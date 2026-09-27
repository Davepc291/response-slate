package notifyprefsstore

// Optional real-database verification, mirroring the existing
// notifyconsentstore/notifydevicestore/alertstore/identitystore convention:
// skipped unless explicitly opted into a local rollback-only database,
// since this sandbox/CI environment cannot assume a running PostgreSQL
// instance. Everything here runs inside one transaction that is always
// rolled back; no fixture is left behind.
//
// This exercises the already-applied migration 000010 schema (the
// alert_preferences table's channel/id-cardinality CHECK constraints, its
// paired quiet-hours CHECK constraint, its user_id-keyed upsert target, and
// its alert_preferences_updated_at trigger) through Get/Upsert themselves,
// the same behaviors a future database/tests/000010_schema_test.sql would
// check directly in psql.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyprefs"
)

// withSavepoint runs fn inside a Postgres SAVEPOINT and always rolls back
// to it afterward. A real constraint-violation error aborts the entire
// enclosing transaction at the Postgres level; this lets one test
// intentionally trigger such an error and then keep using the same shared,
// rollback-only outer transaction for the rest of its assertions --
// identical to the pattern notifyconsentstore's own integration test
// already established for the same reason.
func withSavepoint(t *testing.T, ctx context.Context, tx pgx.Tx, name string, fn func()) {
	t.Helper()
	if _, err := tx.Exec(ctx, "SAVEPOINT "+name); err != nil {
		t.Fatalf("SAVEPOINT %s failed: %v", name, err)
	}
	fn()
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); err != nil {
		t.Fatalf("ROLLBACK TO SAVEPOINT %s failed: %v", name, err)
	}
}

func insertFixtureUser(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Prefs Test', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}
	return id
}

func strPtrI(s string) *string               { return &s }
func floatPtrI(f float64) *float64           { return &f }
func durPtrI(d time.Duration) *time.Duration { return &d }

func TestLiveAlertPreferencesGetAndUpsertRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_PREFS_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_PREFS_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-prefs-store database unavailable")
	}
	defer closeDB()
	pool := db.DB.(*pgxpool.Pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("test transaction failed")
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tx.Rollback(cleanup)
		}
	}()

	var migrated bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = '000010')`).Scan(&migrated); err != nil || !migrated {
		t.Fatal("migration 000010 is not applied to this database")
	}

	userID := insertFixtureUser(t, ctx, tx, "notifyprefsstore-a@example.com")

	p := &Postgres{DB: tx}

	// 1. No row yet: Get is (zero, false, nil).
	prefs, ok, err := p.Get(ctx, identity.UserID(userID))
	if err != nil {
		t.Fatalf("unexpected error on empty Get: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false with no preferences row yet")
	}
	if prefs.Enabled {
		t.Fatal("expected Enabled=false as the fail-closed zero value")
	}

	// 2. First Upsert: full field set, fresh insert.
	now := time.Now().UTC().Truncate(time.Microsecond)
	full := notifyprefs.Preferences{
		UserID:             identity.UserID(userID),
		Enabled:            true,
		Channels:           []notifyprefs.Channel{notifyprefs.ChannelCH1A, notifyprefs.ChannelCH2B},
		ToneSetIDs:         []string{"engine-1", "engine-2"},
		KeywordListIDs:     []string{"structure-fire"},
		MinConfidence:      floatPtrI(0.75),
		QuietHoursStart:    durPtrI(22 * time.Hour),
		QuietHoursEnd:      durPtrI(6 * time.Hour),
		QuietHoursTimezone: strPtrI("America/New_York"),
	}
	created, err := p.Upsert(ctx, now, full)
	if err != nil {
		t.Fatalf("Upsert (insert) failed against a real database: %v", err)
	}
	if !created.CreatedAt.Equal(now) {
		t.Fatalf("expected CreatedAt to equal the supplied now on first insert, got %v vs %v", created.CreatedAt, now)
	}
	if len(created.Channels) != 2 || len(created.ToneSetIDs) != 2 || len(created.KeywordListIDs) != 1 {
		t.Fatalf("unexpected array lengths on insert: %+v", created)
	}
	if created.QuietHoursStart == nil || *created.QuietHoursStart != 22*time.Hour {
		t.Fatalf("unexpected quiet hours start round-trip: %+v", created.QuietHoursStart)
	}
	if created.QuietHoursEnd == nil || *created.QuietHoursEnd != 6*time.Hour {
		t.Fatalf("unexpected quiet hours end round-trip: %+v", created.QuietHoursEnd)
	}
	if created.QuietHoursTimezone == nil || *created.QuietHoursTimezone != "America/New_York" {
		t.Fatalf("unexpected quiet hours timezone round-trip: %+v", created.QuietHoursTimezone)
	}

	gotAfterInsert, ok, err := p.Get(ctx, identity.UserID(userID))
	if err != nil || !ok {
		t.Fatalf("expected the freshly inserted row to be readable, ok=%v err=%v", ok, err)
	}
	if gotAfterInsert.MinConfidence == nil || *gotAfterInsert.MinConfidence != 0.75 {
		t.Fatalf("unexpected min confidence round-trip via Get: %+v", gotAfterInsert.MinConfidence)
	}

	// 3. Second Upsert: full replace with a disjoint field set, at a later
	// time. created_at must survive unchanged; updated_at must move
	// forward (owned by the trigger, not by the second now we supply).
	later := now.Add(time.Hour)
	replaced := notifyprefs.Preferences{
		UserID:         identity.UserID(userID),
		Enabled:        false,
		Channels:       []notifyprefs.Channel{notifyprefs.ChannelCH4C},
		ToneSetIDs:     []string{"ladder-9"},
		KeywordListIDs: nil,
		MinConfidence:  nil,
		// Quiet hours cleared entirely this time.
	}
	updated, err := p.Upsert(ctx, later, replaced)
	if err != nil {
		t.Fatalf("Upsert (replace) failed against a real database: %v", err)
	}
	if !updated.CreatedAt.Equal(now) {
		t.Fatalf("expected CreatedAt to be preserved across an update, got %v (want %v)", updated.CreatedAt, now)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("expected UpdatedAt to move forward after an update, got %v (was %v)", updated.UpdatedAt, created.UpdatedAt)
	}
	// The trigger, not our supplied `later`, owns updated_at: confirm the
	// stored value is real wall-clock time (close to now), not necessarily
	// equal to `later` at all (a deliberately future-dated fixture value).
	if updated.UpdatedAt.After(time.Now().Add(time.Minute)) || updated.UpdatedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("expected UpdatedAt to reflect real wall-clock time (trigger-owned), got %v", updated.UpdatedAt)
	}
	if updated.Enabled {
		t.Fatal("expected Enabled to be fully replaced to false")
	}
	if len(updated.Channels) != 1 || updated.Channels[0] != notifyprefs.ChannelCH4C {
		t.Fatalf("expected full replacement of channels, got %+v", updated.Channels)
	}
	if len(updated.KeywordListIDs) != 0 {
		t.Fatalf("expected keyword_list_ids fully cleared, got %+v", updated.KeywordListIDs)
	}
	if updated.MinConfidence != nil {
		t.Fatalf("expected min_confidence fully cleared, got %+v", updated.MinConfidence)
	}
	if updated.QuietHoursStart != nil || updated.QuietHoursEnd != nil || updated.QuietHoursTimezone != nil {
		t.Fatalf("expected quiet hours fully cleared, got start=%+v end=%+v tz=%+v", updated.QuietHoursStart, updated.QuietHoursEnd, updated.QuietHoursTimezone)
	}

	// Exactly one row still exists for this user (primary key, never a
	// second row from the "insert" half of the upsert).
	var rowCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM alert_preferences WHERE user_id = $1`, userID).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly one alert_preferences row for this user, got %d", rowCount)
	}

	// 4. Empty/default Upsert: a minimal Preferences (UserID only) fully
	// clears every remaining field back to defaults.
	minimal := notifyprefs.Preferences{UserID: identity.UserID(userID)}
	cleared, err := p.Upsert(ctx, later.Add(time.Minute), minimal)
	if err != nil {
		t.Fatalf("Upsert (minimal/default) failed: %v", err)
	}
	if cleared.Enabled || len(cleared.Channels) != 0 || len(cleared.ToneSetIDs) != 0 || len(cleared.KeywordListIDs) != 0 {
		t.Fatalf("expected a minimal Upsert to clear every configurable field, got %+v", cleared)
	}

	// 5. Invalid/nonexistent user: Upsert against a user_id with no
	// corresponding users row is rejected as ErrInput (FK violation), and
	// creates nothing. A real FK violation aborts the transaction, so this
	// runs inside its own SAVEPOINT.
	withSavepoint(t, ctx, tx, "nonexistent_user", func() {
		bogus := notifyprefs.Preferences{UserID: 999999999}
		if _, err := p.Upsert(ctx, now, bogus); err != ErrInput {
			t.Fatalf("expected ErrInput for a nonexistent user id, got %v", err)
		}
	})
	var bogusCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM alert_preferences WHERE user_id = $1`, int64(999999999)).Scan(&bogusCount); err != nil {
		t.Fatal(err)
	}
	if bogusCount != 0 {
		t.Fatalf("expected zero rows for a rejected nonexistent-user upsert, got %d", bogusCount)
	}

	// 6. Get for a different, never-configured user still returns
	// (zero, false, nil), never leaking the fixture user's own row.
	otherUserID := insertFixtureUser(t, ctx, tx, "notifyprefsstore-b@example.com")
	otherPrefs, ok, err := p.Get(ctx, identity.UserID(otherUserID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a different, never-configured user")
	}
	if otherPrefs.Enabled {
		t.Fatal("expected Enabled=false as the fail-closed default for the other user too")
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

// TestLiveAlertPreferencesConstraintsRollsBack proves the migration's own
// CHECK constraints still independently reject malformed data even when
// bypassing this package's own Go-level Preferences.Validate() entirely --
// direct-SQL defense-in-depth, mirroring notifyconsentstore's own direct-SQL
// immutability-trigger test.
func TestLiveAlertPreferencesConstraintsRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_PREFS_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_PREFS_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-prefs-store database unavailable")
	}
	defer closeDB()
	pool := db.DB.(*pgxpool.Pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("test transaction failed")
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tx.Rollback(cleanup)
		}
	}()

	userID := insertFixtureUser(t, ctx, tx, "notifyprefsstore-constraints@example.com")

	// 1. An invalid channel value bypassing Go validation entirely.
	withSavepoint(t, ctx, tx, "invalid_channel", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO alert_preferences (user_id, channels) VALUES ($1, ARRAY['CH5D']::text[])`, userID); err == nil {
			t.Fatal("expected the database's own channels CHECK constraint to reject an unlisted channel value")
		}
	})

	// 2. A half-configured quiet-hours window bypassing Go validation
	// entirely.
	withSavepoint(t, ctx, tx, "unpaired_quiet_hours", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO alert_preferences (user_id, quiet_hours_start) VALUES ($1, '22:00'::time)`, userID); err == nil {
			t.Fatal("expected the database's own paired quiet-hours CHECK constraint to reject a half-configured window")
		}
	})

	// 3. A tone_set_ids array exceeding the cardinality bound bypassing Go
	// validation entirely.
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = "id"
	}
	withSavepoint(t, ctx, tx, "too_many_tone_ids", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO alert_preferences (user_id, tone_set_ids) VALUES ($1, $2::text[])`, userID, tooMany); err == nil {
			t.Fatal("expected the database's own cardinality CHECK constraint to reject 201 tone_set_ids")
		}
	})

	// Confirm none of the rejected attempts left any row behind.
	var count int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM alert_preferences WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected zero rows after every constraint-violating attempt was rejected, got %d", count)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

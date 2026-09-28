package notifyoutboxstore

// Optional real-database verification, mirroring the existing
// notifyprefsstore/notifyconsentstore/notifydevicestore convention: skipped
// unless explicitly opted into a local rollback-only database, since this
// sandbox/CI environment cannot assume a running PostgreSQL instance.
// Everything here runs inside one transaction that is always rolled back;
// no fixture is left behind.
//
// This exercises the already-applied migration 000010 schema (the
// notification_outbox table's composite ownership foreign key, its
// matched-event-only trigger, its attempt-count-bounded CHECK, and its
// pending-guarded state machine) through Enqueue/Get/MarkSent/
// RecordFailedAttempt/MarkExpired/Cancel themselves, the same behaviors a
// future database/tests/000010_schema_test.sql would check directly in
// psql.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

// withSavepoint runs fn inside a Postgres SAVEPOINT and always rolls back
// to it afterward. A real constraint-violation error aborts the entire
// enclosing transaction at the Postgres level; this lets one test
// intentionally trigger such an error and then keep using the same shared,
// rollback-only outer transaction for the rest of its assertions --
// identical to the pattern notifyconsentstore/notifyprefsstore's own
// integration tests already established for the same reason.
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

func hex64(label string) string {
	h := sha256.Sum256([]byte(label))
	return hex.EncodeToString(h[:])
}

func insertFixtureUser(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Outbox Test', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}
	return id
}

func insertFixtureDevice(t *testing.T, ctx context.Context, tx pgx.Tx, userID int64, endpoint string) int64 {
	t.Helper()
	raw := make([]byte, 65)
	raw[0] = 0x04
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO notification_devices (user_id, endpoint, p256dh, auth, platform, created_at, updated_at)
        VALUES ($1, $2, $3, $4, NULL, now(), now()) RETURNING id`,
		userID, endpoint, raw, make([]byte, 16)).Scan(&id); err != nil {
		t.Fatalf("fixture device insert failed: %v", err)
	}
	return id
}

// insertFixtureTransmission inserts a bare radio_transmissions row keyed by
// audioFingerprint, so detection_audit's own
// FOREIGN KEY (audio_fingerprint) REFERENCES radio_transmissions
// (audio_fingerprint) has a real row to reference. Only the two columns
// radio_transmissions actually requires NOT NULL (audio_fingerprint,
// source_filename) are set; every other column is left at its own default
// or NULL.
func insertFixtureTransmission(t *testing.T, ctx context.Context, tx pgx.Tx, audioFingerprint string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `INSERT INTO radio_transmissions (audio_fingerprint, source_filename) VALUES ($1, 'fixture.wav')`, audioFingerprint); err != nil {
		t.Fatalf("fixture radio_transmissions insert failed: %v", err)
	}
}

// insertFixtureAlertEvent inserts a fixture radio_transmissions row, a
// detection_audit row, and a matching alert_events row with the given
// event_id and state ('matched', 'ambiguous', 'partial', or
// 'low_confidence'), so this store's own tests have a real event_id to
// reference from notification_outbox's own event_id foreign key and
// matched-event-only trigger. Uses the audio_fingerprint evidence path
// (rather than source_identity) because it needs only a minimal
// radio_transmissions fixture row, not the much larger set of companion
// columns migration 000002's recording_source_metadata_required CHECK
// demands whenever source_identity is set.
func insertFixtureAlertEvent(t *testing.T, ctx context.Context, tx pgx.Tx, eventID, state string, createdAt, expiresAt time.Time) {
	t.Helper()
	label := eventID
	audioFingerprint := "pcm-s16le-16000-mono-v1:sha256:" + hex64(label+"-audio")
	insertFixtureTransmission(t, ctx, tx, audioFingerprint)
	dedupKey := hex64(label + "-dedup")

	var auditID int64
	if err := tx.QueryRow(ctx, `INSERT INTO detection_audit (created_at, source_kind, channel, tgid, detector_kind, config_id, state, reason, audio_fingerprint, dedup_key, event_id)
        VALUES ($1, 'synthetic', 'CH1A', 57201, 'tone', 'engine-1', $2, 'quick_call', $3, $4, $5) RETURNING id`,
		createdAt, state, audioFingerprint, dedupKey, eventID).Scan(&auditID); err != nil {
		t.Fatalf("fixture detection_audit insert failed: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO alert_events (event_id, schema_version, created_at, expires_at, source_kind, channel, tgid,
            detector_kind, tone_set_id, confidence, state, synthetic, shadow_only, dedup_key, display_summary, detection_audit_id)
        VALUES ($1, 'alert-event-v1', $2, $3, 'synthetic', 'CH1A', 57201, 'tone', 'engine-1', NULL, $4, true, true, $5, $6, $7)`,
		eventID, createdAt, expiresAt, state, dedupKey,
		"CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.", auditID); err != nil {
		t.Fatalf("fixture alert_events insert failed: %v", err)
	}
}

func TestLiveNotificationOutboxEnqueueAndTransitionsRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_OUTBOX_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_OUTBOX_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-outbox-store database unavailable")
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

	userA := insertFixtureUser(t, ctx, tx, "notifyoutboxstore-a@example.com")
	userB := insertFixtureUser(t, ctx, tx, "notifyoutboxstore-b@example.com")
	deviceA := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-a")

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	eventMatched := "evt_" + hex64("outbox-matched-1")
	insertFixtureAlertEvent(t, ctx, tx, eventMatched, "matched", base, base.Add(24*time.Hour))
	eventAmbiguous := "evt_" + hex64("outbox-ambiguous-1")
	insertFixtureAlertEvent(t, ctx, tx, eventAmbiguous, "ambiguous", base, base.Add(24*time.Hour))

	p := &Postgres{DB: tx}

	// 1. Fresh enqueue against a matched event succeeds.
	firstAttempt := base.Add(time.Minute)
	expires := base.Add(time.Hour)
	entry, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceA), identity.UserID(userA), 3, firstAttempt, expires)
	if err != nil {
		t.Fatalf("Enqueue failed against a real database: %v", err)
	}
	if entry.State != notifyoutbox.StatePending || entry.AttemptCount != 0 || entry.MaxAttempts != 3 {
		t.Fatalf("unexpected fresh entry: %+v", entry)
	}
	if entry.NextAttemptAt == nil || !entry.NextAttemptAt.Equal(firstAttempt) {
		t.Fatalf("unexpected next_attempt_at: %+v", entry.NextAttemptAt)
	}
	firstUpdatedAt := entry.UpdatedAt

	// 2. Duplicate enqueue for the same (event_id, device_id) returns the
	// SAME row, completely unchanged -- including updated_at, proving the
	// DO NOTHING path never issues an UPDATE at all.
	dup, err := p.Enqueue(ctx, base.Add(5*time.Minute), eventMatched, notifydevices.DeviceID(deviceA), identity.UserID(userA), 7, base.Add(10*time.Minute), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("duplicate Enqueue failed: %v", err)
	}
	if dup.ID != entry.ID || dup.MaxAttempts != 3 || dup.AttemptCount != 0 {
		t.Fatalf("expected the duplicate enqueue to return the original row unchanged, got %+v (original %+v)", dup, entry)
	}
	if !dup.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("expected updated_at to be untouched by a duplicate enqueue, got %v (original %v)", dup.UpdatedAt, firstUpdatedAt)
	}
	var outboxRowCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE event_id = $1 AND device_id = $2`, eventMatched, deviceA).Scan(&outboxRowCount); err != nil {
		t.Fatal(err)
	}
	if outboxRowCount != 1 {
		t.Fatalf("expected exactly one outbox row for this (event, device) pair, got %d", outboxRowCount)
	}

	// 3. Get finds the entry; a nonexistent id does not.
	got, ok, err := p.Get(ctx, entry.ID)
	if err != nil || !ok || got.ID != entry.ID {
		t.Fatalf("Get failed to round-trip the entry: ok=%v err=%v got=%+v", ok, err, got)
	}
	_, ok, err = p.Get(ctx, notifyoutbox.OutboxID(999999999))
	if err != nil {
		t.Fatalf("expected a nil error for a nonexistent id, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a nonexistent id")
	}

	// 4. Non-matched event rejected by the DB's own matched-event trigger.
	// A real constraint violation aborts the transaction, so this runs
	// inside its own SAVEPOINT.
	deviceForAmbiguous := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-ambiguous")
	withSavepoint(t, ctx, tx, "non_matched_event", func() {
		if _, err := p.Enqueue(ctx, base, eventAmbiguous, notifydevices.DeviceID(deviceForAmbiguous), identity.UserID(userA), 3, firstAttempt, expires); err != ErrInput {
			t.Fatalf("expected ErrInput for a non-matched event, got %v", err)
		}
	})
	var ambiguousRowCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE event_id = $1`, eventAmbiguous).Scan(&ambiguousRowCount); err != nil {
		t.Fatal(err)
	}
	if ambiguousRowCount != 0 {
		t.Fatalf("expected zero rows for a rejected non-matched-event enqueue, got %d", ambiguousRowCount)
	}

	// 5. Wrong device/user ownership rejected: deviceForOwnership belongs
	// to userA, not userB. This must use a real, matched event_id (rather
	// than a nonexistent one) to isolate the ownership failure
	// specifically: the matched-event trigger fires BEFORE INSERT, before
	// the composite ownership FK is ever checked for that row, so a
	// nonexistent event_id would surface ErrInput here instead, not
	// ErrForbidden (see Enqueue's own doc comment for why only one of this
	// table's two foreign keys can ever actually produce a 23503 in
	// practice).
	deviceForOwnership := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-ownership")
	withSavepoint(t, ctx, tx, "wrong_ownership", func() {
		if _, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceForOwnership), identity.UserID(userB), 3, firstAttempt, expires); err != ErrForbidden {
			t.Fatalf("expected ErrForbidden for a cross-user (deviceID, userID) pair against a real matched event, got %v", err)
		}
	})
	var ownershipRowCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE device_id = $1`, deviceForOwnership).Scan(&ownershipRowCount); err != nil {
		t.Fatal(err)
	}
	if ownershipRowCount != 0 {
		t.Fatalf("expected zero rows for every rejected wrong-ownership attempt, got %d", ownershipRowCount)
	}

	// 6. pending -> retry: RecordFailedAttempt with attempts remaining
	// stays pending, attempt_count increments, next_attempt_at advances.
	retryNext := base.Add(15 * time.Minute)
	retried, err := p.RecordFailedAttempt(ctx, base.Add(2*time.Minute), entry.ID, retryNext)
	if err != nil {
		t.Fatalf("RecordFailedAttempt (retry) failed: %v", err)
	}
	if retried.State != notifyoutbox.StatePending || retried.AttemptCount != 1 {
		t.Fatalf("expected a retry to stay pending with attempt_count=1, got %+v", retried)
	}
	if retried.NextAttemptAt == nil || !retried.NextAttemptAt.Equal(retryNext) {
		t.Fatalf("expected next_attempt_at to advance to the supplied value, got %+v", retried.NextAttemptAt)
	}

	// 7. pending -> sent.
	sent, err := p.MarkSent(ctx, base.Add(20*time.Minute), entry.ID)
	if err != nil {
		t.Fatalf("MarkSent failed: %v", err)
	}
	if sent.State != notifyoutbox.StateSent {
		t.Fatalf("expected state=sent, got %+v", sent)
	}
	if sent.NextAttemptAt != nil {
		t.Fatalf("expected next_attempt_at cleared on a terminal transition, got %+v", sent.NextAttemptAt)
	}

	// 8. terminal -> any further transition fails closed with ErrConflict,
	// for every transition method, and mutates nothing.
	if _, err := p.MarkSent(ctx, base.Add(30*time.Minute), entry.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict re-sending an already-sent entry, got %v", err)
	}
	if _, err := p.Cancel(ctx, base.Add(30*time.Minute), entry.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict canceling an already-sent entry, got %v", err)
	}
	if _, err := p.RecordFailedAttempt(ctx, base.Add(30*time.Minute), entry.ID, base.Add(31*time.Minute)); err != ErrConflict {
		t.Fatalf("expected ErrConflict recording a failed attempt against an already-sent entry, got %v", err)
	}
	if _, err := p.MarkExpired(ctx, base.Add(30*time.Minute), entry.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict expiring an already-sent entry, got %v", err)
	}
	stillSent, ok, err := p.Get(ctx, entry.ID)
	if err != nil || !ok || stillSent.State != notifyoutbox.StateSent || stillSent.AttemptCount != 1 {
		t.Fatalf("expected the sent entry to remain completely unchanged after every rejected transition, got %+v (ok=%v err=%v)", stillSent, ok, err)
	}

	// 9. pending -> dead_letter: max_attempts=2, so a second failed attempt
	// exhausts attempts before expiry.
	deviceDeadLetter := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-deadletter")
	dlEntry, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceDeadLetter), identity.UserID(userA), 2, base.Add(time.Minute), base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (dead-letter fixture) failed: %v", err)
	}
	afterFirstFail, err := p.RecordFailedAttempt(ctx, base.Add(2*time.Minute), dlEntry.ID, base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("RecordFailedAttempt (first) failed: %v", err)
	}
	if afterFirstFail.State != notifyoutbox.StatePending || afterFirstFail.AttemptCount != 1 {
		t.Fatalf("expected the first failed attempt to stay pending with attempt_count=1, got %+v", afterFirstFail)
	}
	afterSecondFail, err := p.RecordFailedAttempt(ctx, base.Add(4*time.Minute), dlEntry.ID, base.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("RecordFailedAttempt (second, exhausting) failed: %v", err)
	}
	if afterSecondFail.State != notifyoutbox.StateDeadLetter || afterSecondFail.AttemptCount != 2 {
		t.Fatalf("expected the second failed attempt to reach dead_letter with attempt_count=2, got %+v", afterSecondFail)
	}
	if afterSecondFail.AttemptCount > afterSecondFail.MaxAttempts {
		t.Fatalf("attempt_count must never exceed max_attempts, got %+v", afterSecondFail)
	}
	if afterSecondFail.NextAttemptAt != nil {
		t.Fatalf("expected next_attempt_at cleared on reaching dead_letter, got %+v", afterSecondFail.NextAttemptAt)
	}
	if _, err := p.MarkSent(ctx, base.Add(10*time.Minute), dlEntry.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict transitioning a dead_letter entry further, got %v", err)
	}

	// 10. pending -> expired via a failed attempt made at/after expires_at
	// (expiry takes priority even though attempts also remain).
	deviceExpireViaAttempt := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-expire-attempt")
	expiresSoon := base.Add(10 * time.Minute)
	expireEntry, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceExpireViaAttempt), identity.UserID(userA), 5, base.Add(time.Minute), expiresSoon)
	if err != nil {
		t.Fatalf("Enqueue (expire-via-attempt fixture) failed: %v", err)
	}
	afterExpireAttempt, err := p.RecordFailedAttempt(ctx, expiresSoon.Add(time.Second), expireEntry.ID, expiresSoon.Add(time.Minute))
	if err != nil {
		t.Fatalf("RecordFailedAttempt (past expiry) failed: %v", err)
	}
	if afterExpireAttempt.State != notifyoutbox.StateExpired || afterExpireAttempt.AttemptCount != 1 {
		t.Fatalf("expected expiry to take priority with attempt_count still incremented to 1, got %+v", afterExpireAttempt)
	}
	if afterExpireAttempt.NextAttemptAt != nil {
		t.Fatalf("expected next_attempt_at cleared on reaching expired, got %+v", afterExpireAttempt.NextAttemptAt)
	}

	// 11. Direct expiry with zero attempts: MarkExpired succeeds for a
	// pending entry that reached its own expires_at without ever having a
	// delivery attempt made against it at all.
	deviceExpireDirect := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-expire-direct")
	expiresSoon2 := base.Add(10 * time.Minute)
	directExpireEntry, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceExpireDirect), identity.UserID(userA), 5, base.Add(time.Minute), expiresSoon2)
	if err != nil {
		t.Fatalf("Enqueue (direct-expiry fixture) failed: %v", err)
	}
	// Before its own expiry: MarkExpired must fail with ErrConflict (not
	// yet due).
	if _, err := p.MarkExpired(ctx, base.Add(2*time.Minute), directExpireEntry.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict expiring an entry before its own expires_at, got %v", err)
	}
	expiredDirect, err := p.MarkExpired(ctx, expiresSoon2.Add(time.Second), directExpireEntry.ID)
	if err != nil {
		t.Fatalf("MarkExpired failed: %v", err)
	}
	if expiredDirect.State != notifyoutbox.StateExpired || expiredDirect.AttemptCount != 0 {
		t.Fatalf("expected a direct expiry with zero attempts, got %+v", expiredDirect)
	}

	// 12. pending -> canceled.
	deviceCancel := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-cancel")
	cancelEntry, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceCancel), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (cancel fixture) failed: %v", err)
	}
	canceled, err := p.Cancel(ctx, base.Add(2*time.Minute), cancelEntry.ID)
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if canceled.State != notifyoutbox.StateCanceled {
		t.Fatalf("expected state=canceled, got %+v", canceled)
	}
	if _, err := p.Cancel(ctx, base.Add(3*time.Minute), cancelEntry.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict canceling an already-canceled entry, got %v", err)
	}

	// 13. Missing id fails closed with ErrNotFound for every transition
	// method.
	missing := notifyoutbox.OutboxID(999999999)
	if _, err := p.MarkSent(ctx, base, missing); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (MarkSent), got %v", err)
	}
	if _, err := p.Cancel(ctx, base, missing); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (Cancel), got %v", err)
	}
	if _, err := p.MarkExpired(ctx, base, missing); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (MarkExpired), got %v", err)
	}
	if _, err := p.RecordFailedAttempt(ctx, base, missing, base.Add(time.Minute)); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (RecordFailedAttempt), got %v", err)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

// TestLiveNotificationOutboxAttemptCountBoundRollsBack proves the
// migration's own notification_outbox_attempt_count_bounded CHECK
// constraint (attempt_count <= max_attempts) still independently rejects a
// direct-SQL attempt to exceed it, bypassing this package's own
// RecordFailedAttempt logic entirely -- direct-SQL defense-in-depth,
// mirroring notifyprefsstore's own constraint tests.
func TestLiveNotificationOutboxAttemptCountBoundRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_OUTBOX_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_OUTBOX_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-outbox-store database unavailable")
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

	userID := insertFixtureUser(t, ctx, tx, "notifyoutboxstore-boundcheck@example.com")
	deviceID := insertFixtureDevice(t, ctx, tx, userID, "https://push.example.invalid/outbox/device-boundcheck")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	eventID := "evt_" + hex64("outbox-boundcheck-1")
	insertFixtureAlertEvent(t, ctx, tx, eventID, "matched", base, base.Add(24*time.Hour))

	withSavepoint(t, ctx, tx, "attempt_count_exceeds_max", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO notification_outbox (event_id, device_id, user_id, state, attempt_count, max_attempts, next_attempt_at, expires_at)
            VALUES ($1, $2, $3, 'dead_letter', 5, 3, NULL, $4)`, eventID, deviceID, userID, base.Add(time.Hour)); err == nil {
			t.Fatal("expected the database's own attempt_count_bounded CHECK constraint to reject attempt_count > max_attempts")
		}
	})

	var count int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE event_id = $1`, eventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected zero rows after the constraint-violating attempt was rejected, got %d", count)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

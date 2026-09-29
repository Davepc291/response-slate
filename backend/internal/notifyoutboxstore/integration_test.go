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
// RecordFailedAttempt/MarkExpired/Cancel themselves, plus migration 000011's
// claim_token column and its fencing behavior through ClaimNextDue/
// ClaimSpecific/ReleaseClaim and the now-claim-aware MarkSent/
// RecordFailedAttempt/Cancel/MarkExpired, the same behaviors a future
// database/tests/000010_schema_test.sql (or 000011 equivalent) would check
// directly in psql.

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
	// RecordFailedAttempt now requires a matching claim_token (Step 8D-B
	// Part 10), so entry must be claimed first.
	claim1, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entry.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific before retry failed: %v", err)
	}
	if claim1.ClaimToken == nil {
		t.Fatal("expected a claim token")
	}
	retryNext := base.Add(15 * time.Minute)
	retried, err := p.RecordFailedAttempt(ctx, base.Add(2*time.Minute), entry.ID, retryNext, *claim1.ClaimToken)
	if err != nil {
		t.Fatalf("RecordFailedAttempt (retry) failed: %v", err)
	}
	if retried.State != notifyoutbox.StatePending || retried.AttemptCount != 1 {
		t.Fatalf("expected a retry to stay pending with attempt_count=1, got %+v", retried)
	}
	if retried.NextAttemptAt == nil || !retried.NextAttemptAt.Equal(retryNext) {
		t.Fatalf("expected next_attempt_at to advance to the supplied value, got %+v", retried.NextAttemptAt)
	}
	if retried.Claimed() {
		t.Fatal("expected claim_token to be cleared after RecordFailedAttempt's stays-pending branch")
	}

	// 7. pending -> sent. A fresh claim is required (the previous one was
	// cleared by step 6's RecordFailedAttempt).
	claim2, err := p.ClaimSpecific(ctx, base.Add(16*time.Minute), entry.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific before send failed: %v", err)
	}
	if claim2.ClaimToken == nil || *claim2.ClaimToken == *claim1.ClaimToken {
		t.Fatalf("expected a fresh, distinct claim token, got %+v (previous %+v)", claim2.ClaimToken, claim1.ClaimToken)
	}
	sent, err := p.MarkSent(ctx, base.Add(20*time.Minute), entry.ID, *claim2.ClaimToken)
	if err != nil {
		t.Fatalf("MarkSent failed: %v", err)
	}
	if sent.State != notifyoutbox.StateSent {
		t.Fatalf("expected state=sent, got %+v", sent)
	}
	if sent.NextAttemptAt != nil {
		t.Fatalf("expected next_attempt_at cleared on a terminal transition, got %+v", sent.NextAttemptAt)
	}
	if sent.Claimed() {
		t.Fatal("expected claim_token to be cleared on a sent (terminal) entry")
	}

	// 8. terminal -> any further transition fails closed with ErrConflict,
	// for every transition method, and mutates nothing. The entry is
	// terminal regardless of claim token value, so an arbitrary
	// well-formed token is used.
	arbitraryToken := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if _, err := p.MarkSent(ctx, base.Add(30*time.Minute), entry.ID, arbitraryToken); err != ErrConflict {
		t.Fatalf("expected ErrConflict re-sending an already-sent entry, got %v", err)
	}
	if _, err := p.Cancel(ctx, base.Add(30*time.Minute), entry.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict canceling an already-sent entry, got %v", err)
	}
	if _, err := p.RecordFailedAttempt(ctx, base.Add(30*time.Minute), entry.ID, base.Add(31*time.Minute), arbitraryToken); err != ErrConflict {
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
	// exhausts attempts before expiry. Each attempt requires its own fresh
	// claim.
	deviceDeadLetter := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-deadletter")
	dlEntry, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceDeadLetter), identity.UserID(userA), 2, base.Add(time.Minute), base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (dead-letter fixture) failed: %v", err)
	}
	dlClaim1, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), dlEntry.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (dead-letter, first) failed: %v", err)
	}
	afterFirstFail, err := p.RecordFailedAttempt(ctx, base.Add(2*time.Minute), dlEntry.ID, base.Add(3*time.Minute), *dlClaim1.ClaimToken)
	if err != nil {
		t.Fatalf("RecordFailedAttempt (first) failed: %v", err)
	}
	if afterFirstFail.State != notifyoutbox.StatePending || afterFirstFail.AttemptCount != 1 {
		t.Fatalf("expected the first failed attempt to stay pending with attempt_count=1, got %+v", afterFirstFail)
	}
	dlClaim2, err := p.ClaimSpecific(ctx, base.Add(3*time.Minute), dlEntry.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (dead-letter, second) failed: %v", err)
	}
	afterSecondFail, err := p.RecordFailedAttempt(ctx, base.Add(4*time.Minute), dlEntry.ID, base.Add(5*time.Minute), *dlClaim2.ClaimToken)
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
	if afterSecondFail.Claimed() {
		t.Fatal("expected claim_token to be cleared on reaching dead_letter")
	}
	if _, err := p.MarkSent(ctx, base.Add(10*time.Minute), dlEntry.ID, arbitraryToken); err != ErrConflict {
		t.Fatalf("expected ErrConflict transitioning a dead_letter entry further, got %v", err)
	}

	// 10. pending -> expired via a failed attempt made at/after expires_at
	// (expiry takes priority even though attempts also remain). Requires a
	// claim, exactly like any other RecordFailedAttempt call.
	deviceExpireViaAttempt := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/device-expire-attempt")
	expiresSoon := base.Add(10 * time.Minute)
	expireEntry, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceExpireViaAttempt), identity.UserID(userA), 5, base.Add(time.Minute), expiresSoon)
	if err != nil {
		t.Fatalf("Enqueue (expire-via-attempt fixture) failed: %v", err)
	}
	expireClaim, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), expireEntry.ID, 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimSpecific (expire-via-attempt) failed: %v", err)
	}
	afterExpireAttempt, err := p.RecordFailedAttempt(ctx, expiresSoon.Add(time.Second), expireEntry.ID, expiresSoon.Add(time.Minute), *expireClaim.ClaimToken)
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
	if _, err := p.MarkSent(ctx, base, missing, arbitraryToken); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (MarkSent), got %v", err)
	}
	if _, err := p.Cancel(ctx, base, missing); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (Cancel), got %v", err)
	}
	if _, err := p.MarkExpired(ctx, base, missing); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (MarkExpired), got %v", err)
	}
	if _, err := p.RecordFailedAttempt(ctx, base, missing, base.Add(time.Minute), arbitraryToken); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (RecordFailedAttempt), got %v", err)
	}
	if _, err := p.ReleaseClaim(ctx, base, missing, arbitraryToken); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (ReleaseClaim), got %v", err)
	}
	if _, err := p.ClaimSpecific(ctx, base, missing, time.Minute); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound (ClaimSpecific), got %v", err)
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

// TestLiveNotificationOutboxClaimFencingRollsBack proves migration 000011's
// claim_token column and its fencing behavior against the real schema:
// ClaimNextDue/ClaimSpecific/ReleaseClaim, the now-claim-aware MarkSent/
// RecordFailedAttempt (matching token succeeds, stale token fails with zero
// mutation), and Cancel/MarkExpired's new refusal to terminate an actively
// claimed row. Scenario A is the only one that uses ClaimNextDue (a
// "whichever row is next due" scan); every later scenario uses
// ClaimSpecific against its own dedicated device/entry so scenarios never
// interfere with each other by racing the same "next due" scan.
func TestLiveNotificationOutboxClaimFencingRollsBack(t *testing.T) {
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
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = '000011')`).Scan(&migrated); err != nil || !migrated {
		t.Fatal("migration 000011 is not applied to this database")
	}

	userA := insertFixtureUser(t, ctx, tx, "notifyoutboxstore-claim-a@example.com")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	eventMatched := "evt_" + hex64("outbox-claim-matched-1")
	insertFixtureAlertEvent(t, ctx, tx, eventMatched, "matched", base, base.Add(24*time.Hour))

	p := &Postgres{DB: tx}
	wrongToken := "11111111-2222-3333-4444-555555555555"

	// --- Scenario A: ClaimNextDue -- empty queue, success, lease, cannot
	// double-claim while leased, reclaim after expiry with a different
	// token. This is the ONLY scenario in this test that uses
	// ClaimNextDue, and it runs before any other fixture entry exists, so
	// its own "next due" scan can never be ambiguous about which row it
	// finds.
	deviceA := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-a")

	if _, ok, err := p.ClaimNextDue(ctx, base, time.Minute); err != nil {
		t.Fatalf("ClaimNextDue (empty queue) failed: %v", err)
	} else if ok {
		t.Fatal("expected no due entry before any fixture is enqueued")
	}

	entryA, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceA), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario A) failed: %v", err)
	}

	claimA1, ok, err := p.ClaimNextDue(ctx, base.Add(90*time.Second), 2*time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextDue (first claim) failed: %v", err)
	}
	if !ok || claimA1.ID != entryA.ID || !claimA1.Claimed() {
		t.Fatalf("expected to claim entryA, got ok=%v entry=%+v", ok, claimA1)
	}
	if claimA1.AttemptCount != 0 {
		t.Fatalf("expected claiming to never consume an attempt, got %+v", claimA1)
	}
	tokenA1 := *claimA1.ClaimToken
	leaseA1 := base.Add(90 * time.Second).Add(2 * time.Minute)
	if claimA1.NextAttemptAt == nil || !claimA1.NextAttemptAt.Equal(leaseA1) {
		t.Fatalf("expected next_attempt_at to be bumped forward to the lease expiry, got %+v (want %v)", claimA1.NextAttemptAt, leaseA1)
	}

	if _, ok2, err := p.ClaimNextDue(ctx, base.Add(91*time.Second), 2*time.Minute); err != nil {
		t.Fatalf("ClaimNextDue (still leased) failed: %v", err)
	} else if ok2 {
		t.Fatal("expected the actively-leased row to not be claimable again before its lease expires")
	}

	reclaimA, ok3, err := p.ClaimNextDue(ctx, leaseA1.Add(time.Second), 2*time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextDue (after lease expiry) failed: %v", err)
	}
	if !ok3 || reclaimA.ID != entryA.ID {
		t.Fatalf("expected to reclaim the same entry after lease expiry, got ok=%v entry=%+v", ok3, reclaimA)
	}
	if reclaimA.ClaimToken == nil || *reclaimA.ClaimToken == tokenA1 {
		t.Fatalf("expected a fresh, distinct claim token on reclaim, got %+v (previous %q)", reclaimA.ClaimToken, tokenA1)
	}
	if reclaimA.AttemptCount != 0 {
		t.Fatalf("expected reclaiming an abandoned lease to never consume an attempt either, got %+v", reclaimA)
	}

	// --- Scenario B: ClaimSpecific -- not yet due, success, nonexistent id.
	deviceB := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-b")
	entryB, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceB), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario B) failed: %v", err)
	}
	if _, err := p.ClaimSpecific(ctx, base, entryB.ID, time.Minute); err != ErrConflict {
		t.Fatalf("expected ErrConflict claiming an entry before it is due, got %v", err)
	}
	claimB, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entryB.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (due) failed: %v", err)
	}
	if !claimB.Claimed() {
		t.Fatal("expected entryB to be claimed")
	}
	if _, err := p.ClaimSpecific(ctx, base, notifyoutbox.OutboxID(999999998), time.Minute); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound claiming a nonexistent id, got %v", err)
	}

	// --- Scenario C: MarkSent fencing -- stale token fails with zero
	// mutation; matching token succeeds.
	deviceC := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-c")
	entryC, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceC), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario C) failed: %v", err)
	}
	claimC1, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entryC.ID, 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimSpecific (C, first) failed: %v", err)
	}
	tokenC1 := *claimC1.ClaimToken
	leaseC1 := base.Add(90 * time.Second).Add(30 * time.Second)
	reclaimC, err := p.ClaimSpecific(ctx, leaseC1.Add(time.Second), entryC.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (C, reclaim) failed: %v", err)
	}
	tokenC2 := *reclaimC.ClaimToken
	if tokenC2 == tokenC1 {
		t.Fatal("expected a distinct reclaim token")
	}

	if _, err := p.MarkSent(ctx, leaseC1.Add(2*time.Second), entryC.ID, tokenC1); err != ErrConflict {
		t.Fatalf("expected ErrConflict for MarkSent with a stale (superseded) claim token, got %v", err)
	}
	stillPendingC, ok, err := p.Get(ctx, entryC.ID)
	if err != nil || !ok || stillPendingC.State != notifyoutbox.StatePending {
		t.Fatalf("expected entryC to remain pending after the rejected stale MarkSent, ok=%v err=%v entry=%+v", ok, err, stillPendingC)
	}
	if stillPendingC.ClaimToken == nil || *stillPendingC.ClaimToken != tokenC2 {
		t.Fatalf("expected entryC to still belong to the reclaiming worker's own token, got %+v (want %q)", stillPendingC.ClaimToken, tokenC2)
	}

	sentC, err := p.MarkSent(ctx, leaseC1.Add(3*time.Second), entryC.ID, tokenC2)
	if err != nil {
		t.Fatalf("expected MarkSent with the matching (current) claim token to succeed, got %v", err)
	}
	if sentC.State != notifyoutbox.StateSent || sentC.Claimed() {
		t.Fatalf("unexpected result: %+v", sentC)
	}

	// --- Scenario D: RecordFailedAttempt fencing -- stale token fails with
	// zero mutation (attempt_count unchanged); matching token succeeds.
	deviceD := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-d")
	entryD, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceD), identity.UserID(userA), 5, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario D) failed: %v", err)
	}
	claimD1, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entryD.ID, 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimSpecific (D, first) failed: %v", err)
	}
	tokenD1 := *claimD1.ClaimToken
	leaseD1 := base.Add(90 * time.Second).Add(30 * time.Second)
	reclaimD, err := p.ClaimSpecific(ctx, leaseD1.Add(time.Second), entryD.ID, time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (D, reclaim) failed: %v", err)
	}
	tokenD2 := *reclaimD.ClaimToken

	if _, err := p.RecordFailedAttempt(ctx, leaseD1.Add(2*time.Second), entryD.ID, leaseD1.Add(time.Hour), tokenD1); err != ErrConflict {
		t.Fatalf("expected ErrConflict for RecordFailedAttempt with a stale claim token, got %v", err)
	}
	stillD, ok, err := p.Get(ctx, entryD.ID)
	if err != nil || !ok || stillD.AttemptCount != 0 {
		t.Fatalf("expected a stale completion to never increment attempt_count, got ok=%v err=%v entry=%+v", ok, err, stillD)
	}
	if stillD.ClaimToken == nil || *stillD.ClaimToken != tokenD2 {
		t.Fatalf("expected entryD to still belong to the reclaiming worker's own token, got %+v", stillD.ClaimToken)
	}

	nextD := leaseD1.Add(10 * time.Minute)
	afterD, err := p.RecordFailedAttempt(ctx, leaseD1.Add(3*time.Second), entryD.ID, nextD, tokenD2)
	if err != nil {
		t.Fatalf("expected RecordFailedAttempt with the matching claim token to succeed, got %v", err)
	}
	if afterD.State != notifyoutbox.StatePending || afterD.AttemptCount != 1 || afterD.Claimed() {
		t.Fatalf("unexpected result: %+v", afterD)
	}

	// --- Scenario E: ReleaseClaim -- wrong token fails with zero mutation;
	// correct token releases and makes the entry immediately reclaimable
	// without consuming an attempt.
	deviceE := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-e")
	entryE, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceE), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario E) failed: %v", err)
	}
	claimE, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entryE.ID, 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (E) failed: %v", err)
	}
	tokenE := *claimE.ClaimToken

	if _, err := p.ReleaseClaim(ctx, base.Add(91*time.Second), entryE.ID, wrongToken); err != ErrConflict {
		t.Fatalf("expected ErrConflict releasing with the wrong token, got %v", err)
	}
	stillClaimedE, ok, err := p.Get(ctx, entryE.ID)
	if err != nil || !ok || !stillClaimedE.Claimed() || *stillClaimedE.ClaimToken != tokenE {
		t.Fatalf("expected the claim to remain untouched after a wrong-token release attempt, got %+v", stillClaimedE)
	}
	if stillClaimedE.AttemptCount != 0 {
		t.Fatalf("expected attempt_count unchanged, got %+v", stillClaimedE)
	}

	releasedE, err := p.ReleaseClaim(ctx, base.Add(92*time.Second), entryE.ID, tokenE)
	if err != nil {
		t.Fatalf("expected ReleaseClaim with the correct token to succeed, got %v", err)
	}
	if releasedE.Claimed() {
		t.Fatal("expected claim_token to be cleared")
	}
	if releasedE.AttemptCount != 0 {
		t.Fatalf("expected ReleaseClaim to never consume an attempt, got %+v", releasedE)
	}
	if releasedE.NextAttemptAt == nil || !releasedE.NextAttemptAt.Equal(base.Add(92*time.Second)) {
		t.Fatalf("expected next_attempt_at set to now (immediately reclaimable), got %+v", releasedE.NextAttemptAt)
	}
	reclaimAfterRelease, err := p.ClaimSpecific(ctx, base.Add(92*time.Second), entryE.ID, time.Minute)
	if err != nil {
		t.Fatalf("expected the released entry to be immediately reclaimable, got %v", err)
	}
	if !reclaimAfterRelease.Claimed() {
		t.Fatal("expected the reclaim to succeed")
	}

	// --- Scenario F: Cancel/MarkExpired must never terminate an actively
	// claimed row.
	deviceF1 := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-f1")
	entryF1, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceF1), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario F1) failed: %v", err)
	}
	canceledF1, err := p.Cancel(ctx, base.Add(2*time.Minute), entryF1.ID)
	if err != nil {
		t.Fatalf("expected Cancel to succeed on an unclaimed pending entry, got %v", err)
	}
	if canceledF1.State != notifyoutbox.StateCanceled {
		t.Fatalf("unexpected result: %+v", canceledF1)
	}

	deviceF2 := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-f2")
	entryF2, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceF2), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario F2) failed: %v", err)
	}
	claimF2, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entryF2.ID, 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (F2) failed: %v", err)
	}
	if _, err := p.Cancel(ctx, base.Add(91*time.Second), entryF2.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict canceling an actively claimed entry, got %v", err)
	}
	stillClaimedF2, ok, err := p.Get(ctx, entryF2.ID)
	if err != nil || !ok || stillClaimedF2.State != notifyoutbox.StatePending || !stillClaimedF2.Claimed() {
		t.Fatalf("expected the claimed entry to remain completely untouched, got %+v", stillClaimedF2)
	}
	if _, err := p.ReleaseClaim(ctx, base.Add(92*time.Second), entryF2.ID, *claimF2.ClaimToken); err != nil {
		t.Fatalf("ReleaseClaim (F2) failed: %v", err)
	}
	canceledF2, err := p.Cancel(ctx, base.Add(93*time.Second), entryF2.ID)
	if err != nil {
		t.Fatalf("expected Cancel to succeed once the claim is released, got %v", err)
	}
	if canceledF2.State != notifyoutbox.StateCanceled {
		t.Fatalf("unexpected result: %+v", canceledF2)
	}

	deviceF3 := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-f3")
	expiresSoonF3 := base.Add(10 * time.Minute)
	entryF3, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceF3), identity.UserID(userA), 3, base.Add(time.Minute), expiresSoonF3)
	if err != nil {
		t.Fatalf("Enqueue (scenario F3) failed: %v", err)
	}
	claimF3, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entryF3.ID, 20*time.Minute)
	if err != nil {
		t.Fatalf("ClaimSpecific (F3) failed: %v", err)
	}
	if _, err := p.MarkExpired(ctx, expiresSoonF3.Add(time.Second), entryF3.ID); err != ErrConflict {
		t.Fatalf("expected ErrConflict expiring an actively claimed entry even past its own expires_at, got %v", err)
	}
	stillClaimedF3, ok, err := p.Get(ctx, entryF3.ID)
	if err != nil || !ok || stillClaimedF3.State != notifyoutbox.StatePending || !stillClaimedF3.Claimed() {
		t.Fatalf("expected the claimed entry to remain completely untouched, got %+v", stillClaimedF3)
	}
	if _, err := p.ReleaseClaim(ctx, expiresSoonF3.Add(2*time.Second), entryF3.ID, *claimF3.ClaimToken); err != nil {
		t.Fatalf("ReleaseClaim (F3) failed: %v", err)
	}
	expiredF3, err := p.MarkExpired(ctx, expiresSoonF3.Add(3*time.Second), entryF3.ID)
	if err != nil {
		t.Fatalf("expected MarkExpired to succeed once the claim is released and expires_at has passed, got %v", err)
	}
	if expiredF3.State != notifyoutbox.StateExpired {
		t.Fatalf("unexpected result: %+v", expiredF3)
	}

	// --- Scenario G: a terminal row can never carry a claim_token, even
	// bypassing this package's own Go logic entirely -- direct-SQL
	// defense-in-depth for migration 000011's own
	// notification_outbox_claim_implies_pending CHECK.
	deviceG := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/outbox/claim-g")
	entryG, err := p.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceG), identity.UserID(userA), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (scenario G) failed: %v", err)
	}
	if _, err := p.ClaimSpecific(ctx, base.Add(90*time.Second), entryG.ID, 5*time.Minute); err != nil {
		t.Fatalf("ClaimSpecific (G) failed: %v", err)
	}
	withSavepoint(t, ctx, tx, "claim_implies_pending_check", func() {
		if _, err := tx.Exec(ctx, `UPDATE notification_outbox SET state = 'sent' WHERE id = $1`, int64(entryG.ID)); err == nil {
			t.Fatal("expected the database's own notification_outbox_claim_implies_pending CHECK to reject a terminal state while claim_token is still set")
		}
	})
	stillG, ok, err := p.Get(ctx, entryG.ID)
	if err != nil || !ok || stillG.State != notifyoutbox.StatePending || !stillG.Claimed() {
		t.Fatalf("expected entryG to remain completely unchanged after the rejected direct UPDATE, got %+v", stillG)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

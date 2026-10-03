package notifyoutcome

// Optional real-database verification, mirroring the existing
// notifyoutboxstore/notifydevicestore convention: skipped unless explicitly
// opted into a local rollback-only database. Everything here runs inside one
// outer transaction that is always rolled back; no fixture is left behind.
//
// Store.DB is set to that SAME outer transaction (a pgx.Tx), exactly
// mirroring how notifydevicestore's own Replace/Revoke integration tests
// already exercise their internal Begin/Commit as a Postgres SAVEPOINT
// nested inside an outer transaction: each Store method call here opens its
// own inner savepoint, commits (releases) it on success, and the outer
// transaction -- rolled back at the very end of each test -- discards
// everything regardless. This is what lets these tests prove real atomic
// commit/rollback behavior without ever touching a durable row.
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
	"greenwich-fire-responder/backend/internal/notifydelivery"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifyoutboxstore"
)

func hex64(label string) string {
	h := sha256.Sum256([]byte(label))
	return hex.EncodeToString(h[:])
}

func insertFixtureUser(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Outcome Test', 'responder', 'active',
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

func insertFixtureAlertEvent(t *testing.T, ctx context.Context, tx pgx.Tx, eventID string, createdAt, expiresAt time.Time) {
	t.Helper()
	audioFingerprint := "pcm-s16le-16000-mono-v1:sha256:" + hex64(eventID+"-audio")
	if _, err := tx.Exec(ctx, `INSERT INTO radio_transmissions (audio_fingerprint, source_filename) VALUES ($1, 'fixture.wav')`, audioFingerprint); err != nil {
		t.Fatalf("fixture radio_transmissions insert failed: %v", err)
	}
	dedupKey := hex64(eventID + "-dedup")
	var auditID int64
	if err := tx.QueryRow(ctx, `INSERT INTO detection_audit (created_at, source_kind, channel, tgid, detector_kind, config_id, state, reason, audio_fingerprint, dedup_key, event_id)
        VALUES ($1, 'synthetic', 'CH1A', 57201, 'tone', 'engine-1', 'matched', 'quick_call', $2, $3, $4) RETURNING id`,
		createdAt, audioFingerprint, dedupKey, eventID).Scan(&auditID); err != nil {
		t.Fatalf("fixture detection_audit insert failed: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO alert_events (event_id, schema_version, created_at, expires_at, source_kind, channel, tgid,
            detector_kind, tone_set_id, confidence, state, synthetic, shadow_only, dedup_key, display_summary, detection_audit_id)
        VALUES ($1, 'alert-event-v1', $2, $3, 'synthetic', 'CH1A', 57201, 'tone', 'engine-1', NULL, 'matched', true, true, $4, $5, $6)`,
		eventID, createdAt, expiresAt, dedupKey,
		"CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD.", auditID); err != nil {
		t.Fatalf("fixture alert_events insert failed: %v", err)
	}
}

func deliveryRowCount(t *testing.T, ctx context.Context, tx pgx.Tx, outboxID notifyoutbox.OutboxID) int64 {
	t.Helper()
	var count int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1`, int64(outboxID)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func deviceRevokedAt(t *testing.T, ctx context.Context, tx pgx.Tx, deviceID int64) *time.Time {
	t.Helper()
	var revokedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT revoked_at FROM notification_devices WHERE id = $1`, deviceID).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	return revokedAt
}

// TestLiveNotifyOutcomeRollsBack is the single comprehensive live proof for
// every Store method, run against the real schema inside one outer
// rollback-only transaction.
func TestLiveNotifyOutcomeRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_OUTCOME_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_OUTCOME_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outboxDB, closeOutbox, err := notifyoutboxstore.Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local database unavailable")
	}
	defer closeOutbox()
	pool := outboxDB.DB.(*pgxpool.Pool)

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

	userID := insertFixtureUser(t, ctx, tx, "notifyoutcome-live@example.com")
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	eventID := "evt_" + hex64("notifyoutcome-live-1")
	insertFixtureAlertEvent(t, ctx, tx, eventID, base, base.Add(24*time.Hour))

	outboxStore := &notifyoutboxstore.Postgres{DB: tx}
	store := &Store{DB: tx}

	newEntry := func(label string, maxAttempts int, expiresAt time.Time) (notifyoutbox.Entry, int64) {
		deviceID := insertFixtureDevice(t, ctx, tx, userID, "https://push.example.invalid/outcome/"+label)
		entry, err := outboxStore.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceID), identity.UserID(userID), maxAttempts, base.Add(time.Minute), expiresAt)
		if err != nil {
			t.Fatalf("Enqueue (%s) failed: %v", label, err)
		}
		return entry, deviceID
	}
	claim := func(entry notifyoutbox.Entry, at time.Time) string {
		claimed, err := outboxStore.ClaimSpecific(ctx, at, entry.ID, 5*time.Minute)
		if err != nil {
			t.Fatalf("ClaimSpecific failed: %v", err)
		}
		return *claimed.ClaimToken
	}

	// --- RecordSent: atomic MarkSent + Record ---
	entrySent, _ := newEntry("sent", 3, base.Add(time.Hour))
	tokenSent := claim(entrySent, base.Add(90*time.Second))
	gotEntry, gotDelivery, err := store.RecordSent(ctx, base.Add(91*time.Second), entrySent.ID, tokenSent,
		notifydelivery.Delivery{OutboxID: entrySent.ID, EventID: eventID, DeviceID: entrySent.DeviceID, Outcome: notifydelivery.OutcomeSent, AttemptNumber: 1})
	if err != nil {
		t.Fatalf("RecordSent failed: %v", err)
	}
	if gotEntry.State != notifyoutbox.StateSent {
		t.Fatalf("expected state=sent, got %+v", gotEntry)
	}
	if gotDelivery.Outcome != notifydelivery.OutcomeSent || gotDelivery.ID == 0 {
		t.Fatalf("expected a recorded OutcomeSent delivery with a real id, got %+v", gotDelivery)
	}
	if deliveryRowCount(t, ctx, tx, entrySent.ID) != 1 {
		t.Fatal("expected exactly one real delivery row for the sent entry")
	}

	// --- RecordSent fencing conflict: zero delivery rows ---
	entrySentConflict, _ := newEntry("sent-conflict", 3, base.Add(time.Hour))
	claim(entrySentConflict, base.Add(90*time.Second)) // claim with a token we discard
	wrongToken := "11111111-2222-3333-4444-555555555555"
	if _, _, err := store.RecordSent(ctx, base.Add(91*time.Second), entrySentConflict.ID, wrongToken,
		notifydelivery.Delivery{OutboxID: entrySentConflict.ID, EventID: eventID, DeviceID: entrySentConflict.DeviceID, Outcome: notifydelivery.OutcomeSent, AttemptNumber: 1}); err != notifyoutboxstore.ErrConflict {
		t.Fatalf("expected the unchanged notifyoutboxstore.ErrConflict sentinel, got %v", err)
	}
	if deliveryRowCount(t, ctx, tx, entrySentConflict.ID) != 0 {
		t.Fatal("expected ZERO delivery rows after a fencing conflict -- Record must never be reached")
	}
	stillPendingSentConflict, ok, err := outboxStore.Get(ctx, entrySentConflict.ID)
	if err != nil || !ok || stillPendingSentConflict.State != notifyoutbox.StatePending || !stillPendingSentConflict.Claimed() {
		t.Fatalf("expected the entry to remain exactly as the real claim left it, got %+v", stillPendingSentConflict)
	}

	// --- RecordCanceled: atomic CancelClaimed + Record, no ReleaseClaim step ---
	entryCanceled, _ := newEntry("canceled", 3, base.Add(time.Hour))
	tokenCanceled := claim(entryCanceled, base.Add(90*time.Second))
	gotCanceled, gotCanceledDelivery, err := store.RecordCanceled(ctx, base.Add(91*time.Second), entryCanceled.ID, tokenCanceled,
		notifydelivery.Delivery{OutboxID: entryCanceled.ID, EventID: eventID, DeviceID: entryCanceled.DeviceID, Outcome: notifydelivery.OutcomeCanceled, AttemptNumber: 1})
	if err != nil {
		t.Fatalf("RecordCanceled failed: %v", err)
	}
	if gotCanceled.State != notifyoutbox.StateCanceled {
		t.Fatalf("expected state=canceled, got %+v", gotCanceled)
	}
	if gotCanceledDelivery.Outcome != notifydelivery.OutcomeCanceled {
		t.Fatalf("unexpected delivery outcome: %+v", gotCanceledDelivery)
	}

	// --- RecordFailedAttempt: temporary-failure retry path unchanged (stays pending) ---
	entryRetry, _ := newEntry("retry", 5, base.Add(time.Hour))
	tokenRetry := claim(entryRetry, base.Add(90*time.Second))
	retryNext := base.Add(5 * time.Minute)
	gotRetryEntry, gotRetryDelivery, err := store.RecordFailedAttempt(ctx, base.Add(91*time.Second), entryRetry.ID, retryNext, tokenRetry,
		notifydelivery.Delivery{OutboxID: entryRetry.ID, EventID: eventID, DeviceID: entryRetry.DeviceID, AttemptNumber: 1})
	if err != nil {
		t.Fatalf("RecordFailedAttempt (retry) failed: %v", err)
	}
	if gotRetryEntry.State != notifyoutbox.StatePending || gotRetryEntry.AttemptCount != 1 {
		t.Fatalf("expected the entry to stay pending with attempt_count=1, got %+v", gotRetryEntry)
	}
	if gotRetryDelivery.Outcome != notifydelivery.OutcomeFailed {
		t.Fatalf("expected the derived outcome to be OutcomeFailed, got %q", gotRetryDelivery.Outcome)
	}

	// --- RecordFailedAttempt: exhausting attempts reaches dead_letter via the
	// SAME existing CASE, with the derived outcome matching the real state ---
	entryExhaust, _ := newEntry("exhaust", 1, base.Add(time.Hour))
	tokenExhaust := claim(entryExhaust, base.Add(90*time.Second))
	gotExhaustEntry, gotExhaustDelivery, err := store.RecordFailedAttempt(ctx, base.Add(91*time.Second), entryExhaust.ID, base.Add(2*time.Minute), tokenExhaust,
		notifydelivery.Delivery{OutboxID: entryExhaust.ID, EventID: eventID, DeviceID: entryExhaust.DeviceID, AttemptNumber: 1})
	if err != nil {
		t.Fatalf("RecordFailedAttempt (exhaust) failed: %v", err)
	}
	if gotExhaustEntry.State != notifyoutbox.StateDeadLetter {
		t.Fatalf("expected dead_letter after exhausting max_attempts=1, got %+v", gotExhaustEntry)
	}
	if gotExhaustDelivery.Outcome != notifydelivery.OutcomeDeadLetter {
		t.Fatalf("expected the derived outcome to match the actual dead_letter state, got %q", gotExhaustDelivery.Outcome)
	}

	// --- RecordDeadLetter: permanent failure, immediate, one call ---
	entryPermanent, _ := newEntry("permanent", 5, base.Add(time.Hour))
	tokenPermanent := claim(entryPermanent, base.Add(90*time.Second))
	errCode := "malformed_request"
	gotPermanentEntry, gotPermanentDelivery, err := store.RecordDeadLetter(ctx, base.Add(91*time.Second), entryPermanent.ID, tokenPermanent,
		notifydelivery.Delivery{OutboxID: entryPermanent.ID, EventID: eventID, DeviceID: entryPermanent.DeviceID, Outcome: notifydelivery.OutcomeDeadLetter, AttemptNumber: 1, ErrorCode: &errCode})
	if err != nil {
		t.Fatalf("RecordDeadLetter failed: %v", err)
	}
	if gotPermanentEntry.State != notifyoutbox.StateDeadLetter || gotPermanentEntry.AttemptCount != 1 {
		t.Fatalf("expected immediate dead_letter with attempt_count=1 (not %d, the max), got %+v", gotPermanentEntry.MaxAttempts, gotPermanentEntry)
	}
	if gotPermanentDelivery.Outcome != notifydelivery.OutcomeDeadLetter || gotPermanentDelivery.ErrorCode == nil || *gotPermanentDelivery.ErrorCode != errCode {
		t.Fatalf("unexpected delivery record: %+v", gotPermanentDelivery)
	}

	// --- RecordUnauthorized: THE key proof -- one call atomically
	// dead-letters the outbox row, records exactly one OutcomeUnauthorized
	// delivery row, AND revokes the device -- no next-cycle cleanup needed. ---
	entryUnauth, deviceUnauth := newEntry("unauthorized", 5, base.Add(time.Hour))
	tokenUnauth := claim(entryUnauth, base.Add(90*time.Second))
	if revokedAt := deviceRevokedAt(t, ctx, tx, deviceUnauth); revokedAt != nil {
		t.Fatal("expected the device to start unrevoked")
	}
	gotUnauthEntry, gotUnauthDelivery, err := store.RecordUnauthorized(ctx, base.Add(91*time.Second), entryUnauth.ID, tokenUnauth,
		notifydelivery.Delivery{OutboxID: entryUnauth.ID, EventID: eventID, DeviceID: entryUnauth.DeviceID, Outcome: notifydelivery.OutcomeUnauthorized, AttemptNumber: 1},
		identity.UserID(userID), notifydevices.DeviceID(deviceUnauth))
	if err != nil {
		t.Fatalf("RecordUnauthorized failed: %v", err)
	}
	if gotUnauthEntry.State != notifyoutbox.StateDeadLetter || gotUnauthEntry.AttemptCount != 1 {
		t.Fatalf("expected immediate dead_letter with attempt_count=1, got %+v", gotUnauthEntry)
	}
	if gotUnauthDelivery.Outcome != notifydelivery.OutcomeUnauthorized {
		t.Fatalf("unexpected delivery outcome: %+v", gotUnauthDelivery)
	}
	if deliveryRowCount(t, ctx, tx, entryUnauth.ID) != 1 {
		t.Fatal("expected exactly one real delivery row")
	}
	if revokedAt := deviceRevokedAt(t, ctx, tx, deviceUnauth); revokedAt == nil {
		t.Fatal("expected the device to be revoked by the SAME call, with no second cycle")
	}

	// --- RecordUnauthorized fencing conflict: zero delivery rows, zero device mutation ---
	entryUnauthConflict, deviceUnauthConflict := newEntry("unauthorized-conflict", 5, base.Add(time.Hour))
	claim(entryUnauthConflict, base.Add(90*time.Second))
	if _, _, err := store.RecordUnauthorized(ctx, base.Add(91*time.Second), entryUnauthConflict.ID, wrongToken,
		notifydelivery.Delivery{OutboxID: entryUnauthConflict.ID, EventID: eventID, DeviceID: entryUnauthConflict.DeviceID, Outcome: notifydelivery.OutcomeUnauthorized, AttemptNumber: 1},
		identity.UserID(userID), notifydevices.DeviceID(deviceUnauthConflict)); err != notifyoutboxstore.ErrConflict {
		t.Fatalf("expected the unchanged notifyoutboxstore.ErrConflict sentinel, got %v", err)
	}
	if deliveryRowCount(t, ctx, tx, entryUnauthConflict.ID) != 0 {
		t.Fatal("expected ZERO delivery rows after a fencing conflict")
	}
	if revokedAt := deviceRevokedAt(t, ctx, tx, deviceUnauthConflict); revokedAt != nil {
		t.Fatal("expected ZERO device mutation after a fencing conflict -- Revoke must never be reached")
	}

	// --- Forced mid-transaction failure: Revoke fails (nonexistent device id
	// as the revoke target) AFTER DeadLetterClaimed and Record already
	// "succeeded" inside the inner savepoint -- proving the WHOLE inner
	// transaction rolls back, undoing both earlier writes too. ---
	entryRevokeFails, _ := newEntry("revoke-fails", 5, base.Add(time.Hour))
	tokenRevokeFails := claim(entryRevokeFails, base.Add(90*time.Second))
	nonexistentDeviceID := notifydevices.DeviceID(999999999)
	_, _, err = store.RecordUnauthorized(ctx, base.Add(91*time.Second), entryRevokeFails.ID, tokenRevokeFails,
		notifydelivery.Delivery{OutboxID: entryRevokeFails.ID, EventID: eventID, DeviceID: entryRevokeFails.DeviceID, Outcome: notifydelivery.OutcomeUnauthorized, AttemptNumber: 1},
		identity.UserID(userID), nonexistentDeviceID)
	if err == nil {
		t.Fatal("expected RecordUnauthorized to fail when the revoke target does not exist")
	}
	stillPendingRevokeFails, ok, err := outboxStore.Get(ctx, entryRevokeFails.ID)
	if err != nil || !ok || stillPendingRevokeFails.State != notifyoutbox.StatePending {
		t.Fatalf("expected the outbox row to remain pending -- the earlier DeadLetterClaimed must have been rolled back too, got ok=%v err=%v entry=%+v", ok, err, stillPendingRevokeFails)
	}
	if !stillPendingRevokeFails.Claimed() {
		t.Fatal("expected the entry to still be claimed (with its original token) since the whole attempt rolled back")
	}
	if deliveryRowCount(t, ctx, tx, entryRevokeFails.ID) != 0 {
		t.Fatal("expected ZERO delivery rows -- the earlier Record call must have been rolled back too")
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

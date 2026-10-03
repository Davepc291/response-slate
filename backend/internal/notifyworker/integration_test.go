package notifyworker

// Optional real-database verification, mirroring the existing
// notifyoutboxstore/notifydevicestore convention: skipped unless explicitly
// opted into a local rollback-only database. Everything here runs inside one
// transaction that is always rolled back; no fixture is left behind.
//
// This wires a real notifyoutboxstore.Postgres, notifydevicestore.Postgres,
// and (as of Step 8D-B Part 14B) a real notifyoutcome.Store -- the atomic
// outcome coordinator, itself composed of notifyoutboxstore.Postgres,
// notifydeliverystore.Postgres, and notifydevicestore.Postgres sharing the
// SAME transaction -- behind a Worker, with a fake Evaluator and
// notifyrelay.FakeSender. The Evaluator and Sender are deliberately fake
// because notifypreferences' own correctness is already proven by its own
// test suite; this test's job is to prove notifyworker's OWN integration
// with the real outbox/device/outcome stores, which no unit-test fake can
// prove (real claim fencing, real lease-expiry reclaim, real atomic
// commit/rollback, real composite foreign keys).
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
	"greenwich-fire-responder/backend/internal/notifydevicestore"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
	"greenwich-fire-responder/backend/internal/notifyoutboxstore"
	"greenwich-fire-responder/backend/internal/notifyoutcome"
	"greenwich-fire-responder/backend/internal/notifyrelay"
	"greenwich-fire-responder/backend/internal/notifywebpush"
)

func hex64(label string) string {
	h := sha256.Sum256([]byte(label))
	return hex.EncodeToString(h[:])
}

func insertFixtureUser(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Worker Test', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}
	return id
}

func insertFixtureTestModeDevice(t *testing.T, ctx context.Context, tx pgx.Tx, userID int64, endpoint string) int64 {
	t.Helper()
	raw := make([]byte, 65)
	raw[0] = 0x04
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO notification_devices (user_id, endpoint, p256dh, auth, platform, test_mode, created_at, updated_at)
        VALUES ($1, $2, $3, $4, NULL, true, now(), now()) RETURNING id`,
		userID, endpoint, raw, make([]byte, 16)).Scan(&id); err != nil {
		t.Fatalf("fixture test_mode device insert failed: %v", err)
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

// TestLiveNotifyWorkerEndToEndAndCrashRecoveryRollsBack proves, against the
// real schema and the real notifyoutboxstore/notifydevicestore/
// notifydeliverystore implementations, two things no unit-test fake can
// prove: (1) a full claim -> evaluate -> send -> MarkSent -> audit cycle
// actually commits the expected real rows, and (2) the crash-recovery/
// lease-expiry scenario the Part 14A design lock explicitly accepted as a
// residual risk: an entry claimed and then abandoned (simulating a crash
// before any outbox-store completion call) is safely reclaimed and
// completed by a later cycle once its lease has expired.
func TestLiveNotifyWorkerEndToEndAndCrashRecoveryRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_WORKER_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_WORKER_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outboxDB, closeOutbox, err := notifyoutboxstore.Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notifyoutboxstore database unavailable")
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

	userID := insertFixtureUser(t, ctx, tx, "notifyworker-live@example.com")
	deviceID := insertFixtureTestModeDevice(t, ctx, tx, userID, "https://push.example.invalid/worker/live-one")
	base := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	eventID := "evt_" + hex64("notifyworker-live-1")
	insertFixtureAlertEvent(t, ctx, tx, eventID, base, base.Add(24*time.Hour))

	outboxStore := &notifyoutboxstore.Postgres{DB: tx}
	deviceStore := &notifydevicestore.Postgres{DB: tx}
	outcomes := &notifyoutcome.Store{DB: tx}

	entry, err := outboxStore.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceID), identity.UserID(userID), 5, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	sender := notifyrelay.NewFakeSender()
	evaluator := &fakeEvaluator{decision: eligibleDecision()}
	currentNow := base.Add(90 * time.Second)

	w, err := New(Deps{
		Outbox:    outboxStore,
		Evaluator: evaluator,
		Devices:   deviceStore,
		Sender:    sender,
		Outcomes:  outcomes,
		Alerts:    fixedAlerts(validAlertData(), nil),
		Now:       func() time.Time { return currentNow },
	}, Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// 1. End-to-end: claim -> evaluate (eligible) -> send (accepted) ->
	// MarkSent -> audit, against the real stores.
	processed, err := w.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("expected a processed cycle with no error, got processed=%v err=%v", processed, err)
	}
	sent, ok, err := outboxStore.Get(ctx, entry.ID)
	if err != nil || !ok || sent.State != notifyoutbox.StateSent {
		t.Fatalf("expected the real outbox row to be sent, got ok=%v err=%v entry=%+v", ok, err, sent)
	}
	var deliveryCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1 AND outcome = 'sent'`, int64(entry.ID)).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 1 {
		t.Fatalf("expected exactly one real 'sent' delivery-audit row, got %d", deliveryCount)
	}
	if len(sender.Calls()) != 1 {
		t.Fatalf("expected exactly one real send, got %d", len(sender.Calls()))
	}

	// 2. Crash-recovery / lease-expiry reclaim: a second entry is claimed
	// and then abandoned (no completion call at all, simulating a crash
	// before MarkSent/RecordFailedAttempt/ReleaseClaim ever ran). A later
	// cycle, once the lease has expired, must still be able to reclaim and
	// complete it -- proving the claim is never permanently stuck, even
	// though (as the design lock documents) a real crash after an already-
	// accepted send could still cause a duplicate physical push in this
	// scenario; this test proves the reclaim half of that accepted
	// trade-off, not exactly-once delivery.
	deviceID2 := insertFixtureTestModeDevice(t, ctx, tx, userID, "https://push.example.invalid/worker/live-two")
	entry2, err := outboxStore.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceID2), identity.UserID(userID), 5, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (entry2) failed: %v", err)
	}

	// Simulate "Worker A" claiming and then crashing: claim directly via the
	// store (bypassing the Worker entirely), never completing it.
	leaseDuration := w.leaseDuration()
	abandonedClaim, ok, err := outboxStore.ClaimNextDue(ctx, currentNow, leaseDuration)
	if err != nil || !ok || abandonedClaim.ID != entry2.ID {
		t.Fatalf("expected to claim entry2 as the abandoning worker, got ok=%v err=%v entry=%+v", ok, err, abandonedClaim)
	}

	// Advance the clock past the lease and run a fresh cycle ("Worker B").
	currentNow = currentNow.Add(leaseDuration).Add(time.Second)
	processed2, err := w.RunOnce(ctx)
	if err != nil || !processed2 {
		t.Fatalf("expected the reclaim cycle to process entry2, got processed=%v err=%v", processed2, err)
	}
	reclaimed, ok, err := outboxStore.Get(ctx, entry2.ID)
	if err != nil || !ok || reclaimed.State != notifyoutbox.StateSent {
		t.Fatalf("expected entry2 to be successfully reclaimed and sent, got ok=%v err=%v entry=%+v", ok, err, reclaimed)
	}
	if len(sender.Calls()) != 2 {
		t.Fatalf("expected exactly one additional real send for the reclaimed entry, total=%d", len(sender.Calls()))
	}

	// 3. Unauthorized, single-cycle, atomic proof (Step 8D-B Part 14B): a
	// third entry's device is reported unauthorized by the provider. One
	// RunOnce call must atomically dead-letter the outbox row, write exactly
	// one OutcomeUnauthorized delivery row, AND revoke the device -- all
	// durably together, with no second cycle needed at all.
	deviceID3 := insertFixtureTestModeDevice(t, ctx, tx, userID, "https://push.example.invalid/worker/live-three")
	entry3, err := outboxStore.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceID3), identity.UserID(userID), 5, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue (entry3) failed: %v", err)
	}
	sender.SetResult(notifyrelay.OutboxID(entry3.ID), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})

	processed3, err := w.RunOnce(ctx)
	if err != nil || !processed3 {
		t.Fatalf("expected the unauthorized cycle to process entry3, got processed=%v err=%v", processed3, err)
	}
	deadLettered3, ok, err := outboxStore.Get(ctx, entry3.ID)
	if err != nil || !ok || deadLettered3.State != notifyoutbox.StateDeadLetter {
		t.Fatalf("expected entry3 to be immediately dead_letter after one cycle, got ok=%v err=%v entry=%+v", ok, err, deadLettered3)
	}
	var unauthorizedCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1 AND outcome = 'unauthorized'`, int64(entry3.ID)).Scan(&unauthorizedCount); err != nil {
		t.Fatal(err)
	}
	if unauthorizedCount != 1 {
		t.Fatalf("expected exactly one real 'unauthorized' delivery-audit row, got %d", unauthorizedCount)
	}
	var revokedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT revoked_at FROM notification_devices WHERE id = $1`, deviceID3).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil {
		t.Fatal("expected the device to be revoked by the SAME single cycle, with no next-cycle cleanup required")
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

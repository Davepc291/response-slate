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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydelivery"
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

// concurrencyLiveTestRunID returns a fresh random suffix so this test's
// fixture identifiers (email, endpoint, event_id) never collide with a
// previous run's own leftover alert_events/detection_audit rows, which
// (being hard immutable by design) can never be deleted -- mirroring
// notifyoutboxstore/concurrency_test.go's own identically-reasoned helper
// exactly, duplicated locally since it is unexported in that package.
func concurrencyLiveTestRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// TestLiveNotifyWorkerConcurrentOwnershipRollsBack proves, with two
// genuinely independent *pgxpool.Pool connections (never nested savepoints
// on one shared connection), that worker-level composition inherits Part
// 10's own proven store-level fencing correctly: one outbox row can never
// be successfully owned by two workers at once, a stale claim token's
// completion attempt always fails closed with zero additional mutation, an
// unauthorized outcome completes atomically with exactly one audit row and
// one device revocation, and two genuinely simultaneously-due rows are
// still processed independently.
//
// Determinism note: rather than relying on goroutine scheduling (which
// cannot GUARANTEE two real network round trips overlap a sub-millisecond
// autocommitted UPDATE's own lock-held window -- the actual anti-pattern
// this test deliberately avoids), Scenario 1 below instead holds one real,
// uncommitted PostgreSQL transaction open across an explicit point in this
// test's own control flow, and issues the second, fully independent
// connection's claim attempt only once the first transaction's own claim
// statement has already executed. This makes the lock-contention outcome
// structurally guaranteed by Postgres's own MVCC semantics, not
// probabilistically likely from OS thread timing: once txA's own
// ClaimNextDue call has returned without being committed, its row lock is
// certainly still held at the exact moment the second connection's own
// claim attempt runs -- there is no sleep, no channel race, and no
// dependency on which goroutine "happens to run first," because there are
// no goroutines involved at all.
//
// Cleanup: this cannot be a rollback-only test -- two independently
// committing connections need their own writes to actually be durable and
// cross-visible to each other, exactly as
// notifyoutboxstore/concurrency_test.go's own already-approved precedent
// requires for the identical reason. notification_deliveries/
// notification_outbox/notification_devices/users fixture rows are
// therefore explicitly deleted at the end; alert_events/detection_audit/
// radio_transmissions are hard immutable by design and are left behind
// exactly as that same precedent already accepts, using a fresh run id to
// avoid any collision with a previous run's own leftovers.
func TestLiveNotifyWorkerConcurrentOwnershipRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_WORKER_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_WORKER_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dbURL := os.Getenv("GFR_DATABASE_URL")

	outboxDBA, closeA, err := notifyoutboxstore.Open(ctx, dbURL)
	if err != nil {
		t.Fatal("local notifyoutboxstore database unavailable (connection A)")
	}
	defer closeA()
	poolA := outboxDBA.DB.(*pgxpool.Pool)

	outboxDBB, closeB, err := notifyoutboxstore.Open(ctx, dbURL)
	if err != nil {
		t.Fatal("local notifyoutboxstore database unavailable (connection B)")
	}
	defer closeB()
	poolB := outboxDBB.DB.(*pgxpool.Pool)

	runID := concurrencyLiveTestRunID()
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	eventID := "evt_" + hex64("notifyworker-concurrency-"+runID)

	// Fixture setup: committed for real (not rolled back) -- see this
	// test's own doc comment for why.
	setupTx, err := poolA.Begin(ctx)
	if err != nil {
		t.Fatal("fixture setup transaction failed")
	}
	userID := insertFixtureUser(t, ctx, setupTx, "notifyworker-concurrency-"+runID+"@example.com")
	deviceOne := insertFixtureTestModeDevice(t, ctx, setupTx, userID, "https://push.example.invalid/worker/concurrency-one-"+runID)
	deviceTwo := insertFixtureTestModeDevice(t, ctx, setupTx, userID, "https://push.example.invalid/worker/concurrency-two-"+runID)
	insertFixtureAlertEvent(t, ctx, setupTx, eventID, base, base.Add(24*time.Hour))

	outboxSetup := &notifyoutboxstore.Postgres{DB: setupTx}
	entryOne, err := outboxSetup.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceOne), identity.UserID(userID), 5, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("fixture Enqueue (entryOne) failed: %v", err)
	}
	entryTwo, err := outboxSetup.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceTwo), identity.UserID(userID), 5, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("fixture Enqueue (entryTwo) failed: %v", err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("fixture setup commit failed: %v", err)
	}

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = poolA.Exec(cleanupCtx, `DELETE FROM notification_deliveries WHERE outbox_id IN ($1, $2)`, int64(entryOne.ID), int64(entryTwo.ID))
		_, _ = poolA.Exec(cleanupCtx, `DELETE FROM notification_outbox WHERE id IN ($1, $2)`, int64(entryOne.ID), int64(entryTwo.ID))
		_, _ = poolA.Exec(cleanupCtx, `DELETE FROM notification_devices WHERE id IN ($1, $2)`, deviceOne, deviceTwo)
		_, _ = poolA.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	}()

	outboxA := &notifyoutboxstore.Postgres{DB: poolA}
	outboxB := &notifyoutboxstore.Postgres{DB: poolB}
	lease := 5 * time.Minute
	now := base.Add(90 * time.Second)

	// --- Scenario 1: deterministic, genuinely cross-connection lock
	// contention, plus the "separate due rows processed independently"
	// proof (bullets 1 and 5). ---
	txA, err := poolA.Begin(ctx)
	if err != nil {
		t.Fatal("txA begin failed")
	}
	txAOpen := true
	defer func() {
		if txAOpen {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = txA.Rollback(cleanupCtx)
		}
	}()
	outboxTxA := &notifyoutboxstore.Postgres{DB: txA}

	claimedFirst, ok, err := outboxTxA.ClaimNextDue(ctx, now, lease)
	if err != nil || !ok {
		t.Fatalf("expected txA to claim a due row, got ok=%v err=%v", ok, err)
	}
	if claimedFirst.ID != entryOne.ID && claimedFirst.ID != entryTwo.ID {
		t.Fatalf("claimed an unexpected entry: %+v", claimedFirst)
	}
	// txA's row lock on claimedFirst is now certainly held -- txA has not
	// committed. The very next statement is connection B's own,
	// independent claim attempt, issued only now that txA's own UPDATE is
	// known to have already executed and acquired its lock.
	claimedOther, ok2, err := outboxB.ClaimNextDue(ctx, now, lease)
	if err != nil {
		t.Fatalf("connection B's claim attempt failed: %v", err)
	}
	if !ok2 {
		t.Fatal("expected connection B to successfully claim the OTHER due row despite the first being actively locked by an open transaction")
	}
	if claimedOther.ID == claimedFirst.ID {
		t.Fatal("two connections must never claim the same row")
	}
	wantOtherID := entryOne.ID
	if claimedFirst.ID == entryOne.ID {
		wantOtherID = entryTwo.ID
	}
	if claimedOther.ID != wantOtherID {
		t.Fatalf("expected connection B to land on the other due row (%d), got %+v", wantOtherID, claimedOther)
	}

	// A further claim attempt from B: both rows are now spoken for (one
	// locked by the still-open txA, the other already claimed by B) --
	// nothing left to claim.
	_, ok3, err := outboxB.ClaimNextDue(ctx, now, lease)
	if err != nil {
		t.Fatalf("connection B's second claim attempt failed: %v", err)
	}
	if ok3 {
		t.Fatal("expected nothing left to claim: one row is locked by the still-open txA, the other already claimed by B")
	}

	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("txA commit failed: %v", err)
	}
	txAOpen = false
	tokenFirst := *claimedFirst.ClaimToken
	tokenOther := *claimedOther.ClaimToken

	// Complete claimedOther immediately, independently of whatever happens
	// to claimedFirst below -- the second half of "separate due rows
	// processed independently." This also removes it from the pending
	// pool before Scenario 2's own lease-expiry reclaim, so that Worker B's
	// own ClaimNextDue in Scenario 2 has exactly one unambiguous candidate.
	outcomesB := &notifyoutcome.Store{DB: poolB}
	_, _, err = outcomesB.RecordSent(ctx, now, claimedOther.ID, tokenOther, notifydelivery.Delivery{
		OutboxID: claimedOther.ID, EventID: eventID, DeviceID: claimedOther.DeviceID,
		Outcome: notifydelivery.OutcomeSent, AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("expected claimedOther's own independent completion to succeed, got %v", err)
	}
	sentOther, ok, err := outboxA.Get(ctx, claimedOther.ID)
	if err != nil || !ok || sentOther.State != notifyoutbox.StateSent {
		t.Fatalf("expected the other row to be sent, independent of claimedFirst's own fate, got ok=%v err=%v entry=%+v", ok, err, sentOther)
	}

	// --- Scenario 2: lease-expiry reclaim, unauthorized atomic completion,
	// exactly one audit row, device revocation, and stale-token completion
	// producing zero additional mutation (bullets 1, 2, 3, 4). ---
	sender := notifyrelay.NewFakeSender()
	sender.SetResult(notifyrelay.OutboxID(claimedFirst.ID), notifyrelay.Result{Outcome: notifyrelay.OutcomeUnauthorized})
	evaluator := &fakeEvaluator{decision: eligibleDecision()}
	deviceStoreB := &notifydevicestore.Postgres{DB: poolB}

	workerB, err := New(Deps{
		Outbox:    outboxB,
		Evaluator: evaluator,
		Devices:   deviceStoreB,
		Sender:    sender,
		Outcomes:  outcomesB,
		Alerts:    fixedAlerts(validAlertData(), nil),
		// Deliberately past claimedFirst's own lease (now+lease): this is
		// the ONLY remaining pending+due row at this point (the other is
		// already sent), so this reclaim is unambiguous.
		Now: func() time.Time { return now.Add(lease).Add(time.Second) },
	}, Config{Env: notifywebpush.EnvDev, SendTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("New (workerB) failed: %v", err)
	}

	processed, err := workerB.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("expected Worker B to reclaim and process the abandoned row, got processed=%v err=%v", processed, err)
	}

	deadLettered, ok, err := outboxA.Get(ctx, claimedFirst.ID)
	if err != nil || !ok || deadLettered.State != notifyoutbox.StateDeadLetter {
		t.Fatalf("expected the reclaimed row to be dead_letter, got ok=%v err=%v entry=%+v", ok, err, deadLettered)
	}
	var unauthorizedCount int64
	if err := poolA.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1 AND outcome = 'unauthorized'`, int64(claimedFirst.ID)).Scan(&unauthorizedCount); err != nil {
		t.Fatal(err)
	}
	if unauthorizedCount != 1 {
		t.Fatalf("expected exactly one unauthorized delivery row, got %d", unauthorizedCount)
	}
	var revokedAt *time.Time
	if err := poolA.QueryRow(ctx, `SELECT revoked_at FROM notification_devices WHERE id = $1`, int64(claimedFirst.DeviceID)).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil {
		t.Fatal("expected the device to be revoked by Worker B's own single atomic completion")
	}

	// The original claimant ("Worker A"), unaware its token is now stale
	// (Worker B's own reclaim generated a fresh one), finally tries to
	// record its own outcome.
	outcomesA := &notifyoutcome.Store{DB: poolA}
	_, _, err = outcomesA.RecordUnauthorized(ctx, now, claimedFirst.ID, tokenFirst, notifydelivery.Delivery{
		OutboxID: claimedFirst.ID, EventID: eventID, DeviceID: claimedFirst.DeviceID,
		Outcome: notifydelivery.OutcomeUnauthorized, AttemptNumber: 1,
	}, claimedFirst.UserID, claimedFirst.DeviceID)
	if !errors.Is(err, notifyoutboxstore.ErrConflict) {
		t.Fatalf("expected the stale-token completion attempt to fail with ErrConflict, got %v", err)
	}

	// Zero additional mutation from the stale attempt.
	var unauthorizedCountAfter int64
	if err := poolA.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1 AND outcome = 'unauthorized'`, int64(claimedFirst.ID)).Scan(&unauthorizedCountAfter); err != nil {
		t.Fatal(err)
	}
	if unauthorizedCountAfter != 1 {
		t.Fatalf("expected still exactly one delivery row after the stale attempt, got %d", unauthorizedCountAfter)
	}
	stillDeadLettered, ok, err := outboxA.Get(ctx, claimedFirst.ID)
	if err != nil || !ok || stillDeadLettered.State != notifyoutbox.StateDeadLetter {
		t.Fatalf("expected the row to remain exactly as Worker B left it, got ok=%v err=%v entry=%+v", ok, err, stillDeadLettered)
	}
}

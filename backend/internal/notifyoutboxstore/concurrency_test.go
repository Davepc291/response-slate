package notifyoutboxstore

// This is the real, two-connection stale-worker fencing proof for Step
// 8D-B Part 10, gated behind the same GFR_NOTIFY_OUTBOX_LIVE_TEST opt-in as
// every other live test in this package. Unlike every other integration
// test here, this one cannot be rollback-only: proving that a SECOND,
// independent database connection (simulating a second worker process) can
// see and reclaim a row after the first connection's own claim/lease
// requires the first connection's writes to actually be committed and
// visible outside its own transaction. Every fixture row this test creates
// in a mutable table (notification_outbox, notification_devices, users) is
// therefore explicitly deleted at the end (not rolled back), and deletion
// is verified by a direct count query, exactly mirroring every other test's
// own "zero fixture leakage" discipline, just via DELETE instead of
// ROLLBACK. The one exception -- the alert_events/detection_audit fixture
// rows this test also needs -- is explained in this test's own cleanup
// comment: those two tables are hard immutable by design, and DELETE
// against them is rejected exactly as intended, even here.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

// concurrencyTestRunID returns a fresh random suffix so this test's fixture
// identifiers (email, endpoint, event_id) never collide with a previous
// run's own fixture -- including one whose alert_events/detection_audit
// rows are now permanently stuck in the database (see this test's own
// cleanup comment for why those two tables can never be deleted). A fixed,
// literal identifier would eventually collide with such a leftover row's
// unique constraints on any retry.
func concurrencyTestRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// TestLiveNotificationOutboxConcurrentStaleWorkerFencingProof proves the
// exact race Step 8D-B Part 10 exists to close: Worker A claims a row,
// becomes delayed/stale, Worker B later reclaims the same row after lease
// expiry, and Worker A eventually comes back and tries to apply a stale
// completion. The two workers are modeled as two fully independent
// *pgxpool.Pool connections (via two separate Open calls), each acting
// entirely outside any shared transaction -- exactly how two real,
// unrelated worker processes would actually behave.
func TestLiveNotificationOutboxConcurrentStaleWorkerFencingProof(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_OUTBOX_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_OUTBOX_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbURL := os.Getenv("GFR_DATABASE_URL")

	dbA, closeA, err := Open(ctx, dbURL)
	if err != nil {
		t.Fatal("local notification-outbox-store database unavailable (connection A)")
	}
	defer closeA()
	dbB, closeB, err := Open(ctx, dbURL)
	if err != nil {
		t.Fatal("local notification-outbox-store database unavailable (connection B)")
	}
	defer closeB()

	poolA := dbA.DB.(*pgxpool.Pool)
	poolB := dbB.DB.(*pgxpool.Pool)
	workerA := &Postgres{DB: poolA}
	workerB := &Postgres{DB: poolB}

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	runID := concurrencyTestRunID()
	email := fmt.Sprintf("notifyoutboxstore-concurrency-%s@example.com", runID)
	endpoint := fmt.Sprintf("https://push.example.invalid/outbox/concurrency-%s", runID)
	eventID := "evt_" + hex64("outbox-concurrency-race-"+runID)

	// --- Fixture setup: one real transaction, committed (not rolled
	// back), so its writes are visible to both independent connections
	// above. Reuses the exact same fixture helpers every rollback-only
	// test in this package already uses -- they only need a pgx.Tx, and a
	// real *pgxpool.Tx satisfies that identically.
	//
	// setupCommitted guards a deferred rollback exactly like every other
	// test's own rolledBack pattern: if any fixture helper below fails
	// (t.Fatalf), this transaction's connection must still be released
	// back to poolA before this test's own deferred closeA()/closeB()
	// calls run -- pgxpool.Pool.Close() blocks forever waiting for every
	// acquired connection to be returned, and t.Fatalf unwinds via
	// runtime.Goexit(), which runs every deferred function in this same
	// goroutine (including closeA/closeB, registered earlier and so
	// running later) whether or not the test body ever reaches this
	// function's own later statements. Without this safety net, a failed
	// fixture insert here would hang the whole test in Pool.Close()
	// instead of failing cleanly -- confirmed the hard way while writing
	// this test.
	setupCommitted := false
	setupTx, err := poolA.Begin(ctx)
	if err != nil {
		t.Fatal("fixture setup transaction failed")
	}
	defer func() {
		if !setupCommitted {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = setupTx.Rollback(cleanupCtx)
		}
	}()
	userID := insertFixtureUser(t, ctx, setupTx, email)
	deviceID := insertFixtureDevice(t, ctx, setupTx, userID, endpoint)
	insertFixtureAlertEvent(t, ctx, setupTx, eventID, "matched", base, base.Add(24*time.Hour))
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("fixture setup commit failed: %v", err)
	}
	setupCommitted = true

	// Cleanup runs regardless of pass/fail, deleting every fixture row
	// this test created that CAN be deleted, then verifying zero rows
	// remain in those tables -- the DELETE-based equivalent of every
	// other test's own rollback-based cleanup.
	//
	// alert_events and detection_audit are deliberately NOT included:
	// both are hard immutable (migration 000007's own ENABLE ALWAYS
	// triggers, reject_alert_audit_mutation()), and that trigger blocks
	// DELETE exactly as designed -- confirmed empirically while writing
	// this test (attempting it raises SQLSTATE 55000, "alert audit
	// history is append-only"). A ROLLBACK bypasses this entirely
	// (undoing the whole transaction, not issuing a DML statement the
	// trigger can intercept), which is exactly why every other test in
	// this package can stay rollback-only and never hits this at all --
	// this is the one test in this package that structurally cannot,
	// since proving cross-connection visibility requires a real commit.
	// A handful of clearly-labeled, harmless audit rows permanently
	// remaining in the local development database is the correct,
	// unavoidable cost of that -- not a leak this test failed to clean
	// up, but the append-only tables working exactly as intended even
	// against this test's own fixture data. notification_outbox,
	// notification_devices, and users have no such restriction and are
	// fully deleted below.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mustExec(t, cleanupCtx, poolA, `DELETE FROM notification_outbox WHERE event_id = $1`, eventID)
		mustExec(t, cleanupCtx, poolA, `DELETE FROM notification_devices WHERE id = $1`, deviceID)
		mustExec(t, cleanupCtx, poolA, `DELETE FROM users WHERE id = $1`, userID)

		var remaining int64
		if err := poolA.QueryRow(cleanupCtx, `SELECT count(*) FROM users WHERE normalized_email = $1`, email).Scan(&remaining); err != nil {
			t.Fatalf("cleanup verification query failed: %v", err)
		}
		if remaining != 0 {
			t.Fatalf("expected zero fixture rows to remain after cleanup, got %d leftover user row(s)", remaining)
		}
		var remainingOutbox int64
		if err := poolA.QueryRow(cleanupCtx, `SELECT count(*) FROM notification_outbox WHERE event_id = $1`, eventID).Scan(&remainingOutbox); err != nil {
			t.Fatalf("cleanup verification query failed: %v", err)
		}
		if remainingOutbox != 0 {
			t.Fatalf("expected zero leftover notification_outbox rows after cleanup, got %d", remainingOutbox)
		}
	}()

	entry, err := workerA.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceID), identity.UserID(userID), 5, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// 1. Worker A claims the row -> T1, a short lease.
	claimA, err := workerA.ClaimSpecific(ctx, base.Add(90*time.Second), entry.ID, 30*time.Second)
	if err != nil {
		t.Fatalf("Worker A ClaimSpecific failed: %v", err)
	}
	tokenA := *claimA.ClaimToken
	leaseA := base.Add(90 * time.Second).Add(30 * time.Second)

	// 2. Make A's lease expire (simulated by advancing the "now" a later
	// call supplies -- no wall-clock sleep is needed, since every method
	// here takes now as an explicit argument and never calls time.Now()
	// itself).
	pastLease := leaseA.Add(time.Second)

	// 3. Worker B, a fully independent connection, reclaims the SAME row
	// -> T2, a fresh, distinct token.
	claimB, err := workerB.ClaimSpecific(ctx, pastLease, entry.ID, 5*time.Minute)
	if err != nil {
		t.Fatalf("Worker B ClaimSpecific (reclaim) failed: %v", err)
	}
	tokenB := *claimB.ClaimToken
	if tokenB == tokenA {
		t.Fatal("expected Worker B's reclaim to produce a token distinct from Worker A's own, now-stale token")
	}

	// 4. Worker A, unaware it has been superseded, attempts to complete
	// using its own stale token T1.
	if _, err := workerA.RecordFailedAttempt(ctx, pastLease.Add(time.Second), entry.ID, pastLease.Add(time.Hour), tokenA); err != ErrConflict {
		t.Fatalf("expected Worker A's stale completion to fail with ErrConflict, got %v", err)
	}

	// 5. Verify zero mutation from Worker A's rejected attempt: the row
	// still belongs to Worker B's own token T2, and attempt_count is
	// still zero (no attempt was actually recorded by the rejected call).
	afterStaleAttempt, ok, err := workerA.Get(ctx, entry.ID)
	if err != nil || !ok {
		t.Fatalf("Get after Worker A's rejected attempt failed: ok=%v err=%v", ok, err)
	}
	if afterStaleAttempt.AttemptCount != 0 {
		t.Fatalf("expected Worker A's rejected stale attempt to leave attempt_count at 0, got %+v", afterStaleAttempt)
	}
	if afterStaleAttempt.ClaimToken == nil || *afterStaleAttempt.ClaimToken != tokenB {
		t.Fatalf("expected the row to still belong to Worker B's own token %q, got %+v", tokenB, afterStaleAttempt.ClaimToken)
	}
	if afterStaleAttempt.State != notifyoutbox.StatePending {
		t.Fatalf("expected the row to remain pending (still Worker B's live attempt in progress), got %+v", afterStaleAttempt)
	}

	// 6. Worker B completes successfully with its own, still-valid token.
	sent, err := workerB.MarkSent(ctx, pastLease.Add(2*time.Second), entry.ID, tokenB)
	if err != nil {
		t.Fatalf("Worker B MarkSent failed: %v", err)
	}
	if sent.State != notifyoutbox.StateSent {
		t.Fatalf("expected Worker B's completion to succeed, got %+v", sent)
	}

	// 7. Final state reflects only Worker B's own action: sent, exactly
	// one attempt recorded in total (zero from A's rejected attempt, none
	// needed for B's own successful send), claim cleared.
	final, ok, err := workerA.Get(ctx, entry.ID)
	if err != nil || !ok {
		t.Fatalf("final Get failed: ok=%v err=%v", ok, err)
	}
	if final.State != notifyoutbox.StateSent {
		t.Fatalf("expected the final state to be sent, got %+v", final)
	}
	if final.AttemptCount != 0 {
		t.Fatalf("expected attempt_count to reflect only real recorded attempts (zero, since Worker A's own was rejected and MarkSent does not increment it), got %+v", final)
	}
	if final.Claimed() {
		t.Fatalf("expected the claim to be cleared on the final, sent entry, got %+v", final)
	}
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("cleanup statement failed (%s): %v", sql, err)
	}
}

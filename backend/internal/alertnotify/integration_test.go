package alertnotify

// Optional real-database verification, mirroring the existing
// notifyoutcome convention exactly: skipped unless explicitly opted into a
// local rollback-only database. Everything here runs inside one outer
// transaction that is always rolled back; no fixture is left behind.
//
// This test proves the real end-to-end wiring: a genuine matched
// alert_events row (inserted directly, exactly mirroring
// notifyoutcome/integration_test.go's own insertFixtureAlertEvent helper,
// bypassing RecordDetection's own radio_transmissions/detection_audit
// plumbing which this package never touches anyway), real test_mode/
// non-test/revoked devices spanning multiple users, and a real
// EnqueueMatchedEvent fan-out producing exactly the eligible-device-count of
// notification_outbox rows with the correct expires_at -- plus a second
// invocation proving zero duplicate rows (Enqueue's own (event_id,
// device_id) idempotency, this package's sole dedup mechanism).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/alerts"
	"greenwich-fire-responder/backend/internal/notifydevicestore"
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
        VALUES ($1, 'AlertNotify Test', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}
	return id
}

func insertFixtureDevice(t *testing.T, ctx context.Context, tx pgx.Tx, userID int64, endpoint string, testMode bool, revoked bool) int64 {
	t.Helper()
	raw := make([]byte, 65)
	raw[0] = 0x04
	var revokedAt interface{}
	if revoked {
		revokedAt = time.Now().UTC()
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO notification_devices (user_id, endpoint, p256dh, auth, platform, created_at, updated_at, revoked_at, test_mode)
        VALUES ($1, $2, $3, $4, NULL, now(), now(), $5, $6) RETURNING id`,
		userID, endpoint, raw, make([]byte, 16), revokedAt, testMode).Scan(&id); err != nil {
		t.Fatalf("fixture device insert failed: %v", err)
	}
	return id
}

// insertFixtureMatchedAlertEvent inserts a real detection_audit + alert_events
// row directly (mirroring notifyoutcome/integration_test.go's own
// insertFixtureAlertEvent exactly), and returns the alerts.Event this
// package's own caller would hold after a real RecordDetection call.
func insertFixtureMatchedAlertEvent(t *testing.T, ctx context.Context, tx pgx.Tx, eventID string, createdAt, expiresAt time.Time) alerts.Event {
	t.Helper()
	audioFingerprint := "pcm-s16le-16000-mono-v1:sha256:" + hex64(eventID+"-audio")
	if _, err := tx.Exec(ctx, `INSERT INTO radio_transmissions (audio_fingerprint, source_filename) VALUES ($1, 'fixture.wav')`, audioFingerprint); err != nil {
		t.Fatalf("fixture radio_transmissions insert failed: %v", err)
	}
	dedupKey := hex64(eventID + "-dedup")
	displaySummary := "CH1A tone match: quick-call alert (synthetic). NOT LIVE CAD."
	var auditID int64
	if err := tx.QueryRow(ctx, `INSERT INTO detection_audit (created_at, source_kind, channel, tgid, detector_kind, config_id, state, reason, audio_fingerprint, dedup_key, event_id)
        VALUES ($1, 'synthetic', 'CH1A', 57201, 'tone', 'engine-1', 'matched', 'quick_call', $2, $3, $4) RETURNING id`,
		createdAt, audioFingerprint, dedupKey, eventID).Scan(&auditID); err != nil {
		t.Fatalf("fixture detection_audit insert failed: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO alert_events (event_id, schema_version, created_at, expires_at, source_kind, channel, tgid,
            detector_kind, tone_set_id, confidence, state, synthetic, shadow_only, dedup_key, display_summary, detection_audit_id)
        VALUES ($1, 'alert-event-v1', $2, $3, 'synthetic', 'CH1A', 57201, 'tone', 'engine-1', NULL, 'matched', true, true, $4, $5, $6)`,
		eventID, createdAt, expiresAt, dedupKey, displaySummary, auditID); err != nil {
		t.Fatalf("fixture alert_events insert failed: %v", err)
	}
	return alerts.Event{
		EventID:        eventID,
		SchemaVersion:  "alert-event-v1",
		CreatedAt:      createdAt,
		ExpiresAt:      expiresAt,
		SourceKind:     alerts.SourceSynthetic,
		Channel:        alerts.ChannelCH1A,
		TGID:           57201,
		DetectorKind:   alerts.DetectorTone,
		ToneSetID:      "engine-1",
		State:          alerts.StateMatched,
		Synthetic:      true,
		ShadowOnly:     true,
		DedupKey:       dedupKey,
		DisplaySummary: displaySummary,
	}
}

// outboxRowCountForDevice is deliberately scoped to one (event_id,
// device_id) pair, never the event's own total row count: ListActiveTestMode
// is a system-wide query (see notifydevicestore.ListActiveTestMode's own
// doc comment), so other pre-existing test_mode=true devices in this shared
// local dev database are also enumerated and enqueued against this test's
// own fixture event. A total-row-count assertion for the event would be
// contaminated by that pre-existing state; a per-device count is not.
func outboxRowCountForDevice(t *testing.T, ctx context.Context, tx pgx.Tx, eventID string, deviceID int64) int64 {
	t.Helper()
	var count int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_outbox WHERE event_id = $1 AND device_id = $2`, eventID, deviceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestLiveAlertNotifyEnqueueMatchedEventRollsBack is the single
// comprehensive live proof, run against the real schema inside one outer
// rollback-only transaction.
func TestLiveAlertNotifyEnqueueMatchedEventRollsBack(t *testing.T) {
	if os.Getenv("GFR_ALERTNOTIFY_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_ALERTNOTIFY_LIVE_TEST=true and GFR_DATABASE_URL)")
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

	userA := insertFixtureUser(t, ctx, tx, "alertnotify-live-a@example.com")
	userB := insertFixtureUser(t, ctx, tx, "alertnotify-live-b@example.com")

	// Eligible: active, test_mode=true, spanning two different users.
	devA := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/alertnotify/a", true, false)
	devB := insertFixtureDevice(t, ctx, tx, userB, "https://push.example.invalid/alertnotify/b", true, false)
	// Ineligible: non-test-mode device, must never receive an outbox entry.
	devNonTest := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/alertnotify/non-test", false, false)
	// Ineligible: revoked test-mode device, must never receive an outbox entry.
	devRevoked := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/alertnotify/revoked", true, true)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := base.Add(2 * time.Hour)
	eventID := "evt_" + hex64("alertnotify-live-1")
	ev := insertFixtureMatchedAlertEvent(t, ctx, tx, eventID, base, expiresAt)

	outboxStore := &notifyoutboxstore.Postgres{DB: tx}
	deviceStore := &notifydevicestore.Postgres{DB: tx}
	svc, err := NewService(outboxStore, deviceStore, Config{MaxAttempts: 5})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}

	now := base.Add(time.Minute)
	results, err := svc.EnqueueMatchedEvent(ctx, now, ev)
	if err != nil {
		t.Fatalf("EnqueueMatchedEvent failed: %v", err)
	}

	// Membership-based assertion, not an exact total count: ListActiveTestMode
	// is a system-wide query against a shared database (see
	// notifydevicestore.ListActiveTestMode's own doc comment), so other
	// pre-existing test_mode=true devices from earlier runs may also be
	// present. What this test can and must prove is that its OWN two
	// eligible fixtures are present exactly once each, with no error, and
	// that its own ineligible fixtures never appear.
	byDevice := map[int64]DeviceResult{}
	for _, r := range results {
		byDevice[int64(r.DeviceID)] = r
	}
	for _, id := range []int64{devA, devB} {
		r, ok := byDevice[id]
		if !ok {
			t.Fatalf("expected a result for eligible device %d, got none (results: %+v)", id, results)
		}
		if r.Err != nil {
			t.Fatalf("device %d: unexpected Err %v", id, r.Err)
		}
		if r.Entry.EventID != eventID {
			t.Fatalf("device %d: entry.EventID = %q, want %q", id, r.Entry.EventID, eventID)
		}
		if !r.Entry.ExpiresAt.Equal(expiresAt) {
			t.Fatalf("device %d: entry.ExpiresAt = %v, want %v", id, r.Entry.ExpiresAt, expiresAt)
		}
	}

	for _, id := range []int64{devA, devB} {
		if got := outboxRowCountForDevice(t, ctx, tx, eventID, id); got != 1 {
			t.Fatalf("notification_outbox row count for event %s, device %d = %d, want 1", eventID, id, got)
		}
	}
	if _, ok := byDevice[devNonTest]; ok {
		t.Fatalf("non-test-mode device %d must never appear in EnqueueMatchedEvent results", devNonTest)
	}
	if _, ok := byDevice[devRevoked]; ok {
		t.Fatalf("revoked device %d must never appear in EnqueueMatchedEvent results", devRevoked)
	}
	for _, id := range []int64{devNonTest, devRevoked} {
		if got := outboxRowCountForDevice(t, ctx, tx, eventID, id); got != 0 {
			t.Fatalf("notification_outbox row count for event %s, ineligible device %d = %d, want 0", eventID, id, got)
		}
	}

	// Second invocation: Enqueue's own (event_id, device_id) idempotency must
	// make this a no-op at the database layer -- zero new rows, no error,
	// same two devices reported with no Err.
	results2, err := svc.EnqueueMatchedEvent(ctx, now, ev)
	if err != nil {
		t.Fatalf("second EnqueueMatchedEvent failed: %v", err)
	}
	byDevice2 := map[int64]DeviceResult{}
	for _, r := range results2 {
		byDevice2[int64(r.DeviceID)] = r
	}
	for _, id := range []int64{devA, devB} {
		r, ok := byDevice2[id]
		if !ok {
			t.Fatalf("second call: expected a result for eligible device %d, got none", id)
		}
		if r.Err != nil {
			t.Fatalf("second call: device %d: unexpected Err %v", id, r.Err)
		}
	}
	for _, id := range []int64{devA, devB} {
		if got := outboxRowCountForDevice(t, ctx, tx, eventID, id); got != 1 {
			t.Fatalf("notification_outbox row count for event %s, device %d after second call = %d, want 1 (idempotent, no duplicates)", eventID, id, got)
		}
	}

	rolledBack = true
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
}

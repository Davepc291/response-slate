package notifydeliverystore

// Optional real-database verification, mirroring the existing
// notifyoutboxstore/notifyprefsstore/notifyconsentstore convention: skipped
// unless explicitly opted into a local rollback-only database, since this
// sandbox/CI environment cannot assume a running PostgreSQL instance.
// Everything here runs inside one transaction that is always rolled back;
// no fixture is left behind.
//
// This exercises the already-applied migration 000010 schema (the
// notification_deliveries table's composite foreign key, its outcome/
// attempt_number/error_code CHECK constraints, and its hard immutability
// trigger) through Record itself, the same behaviors a future
// database/tests/000010_schema_test.sql would check directly in psql.

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

// withSavepoint runs fn inside a Postgres SAVEPOINT and always rolls back
// to it afterward. A real constraint-violation error aborts the entire
// enclosing transaction at the Postgres level; this lets one test
// intentionally trigger such an error and then keep using the same shared,
// rollback-only outer transaction for the rest of its assertions --
// identical to the pattern every sibling store's own integration test
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

func hex64(label string) string {
	h := sha256.Sum256([]byte(label))
	return hex.EncodeToString(h[:])
}

func insertFixtureUser(t *testing.T, ctx context.Context, tx pgx.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Delivery Test', 'responder', 'active',
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

func insertFixtureTransmission(t *testing.T, ctx context.Context, tx pgx.Tx, audioFingerprint string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `INSERT INTO radio_transmissions (audio_fingerprint, source_filename) VALUES ($1, 'fixture.wav')`, audioFingerprint); err != nil {
		t.Fatalf("fixture radio_transmissions insert failed: %v", err)
	}
}

// insertFixtureAlertEvent inserts a fixture radio_transmissions row, a
// detection_audit row, and a matching alert_events row with the given
// event_id and state ('matched' for every use in this file), so this
// store's own tests have a real event_id to reference from
// notification_outbox/notification_deliveries' own foreign keys.
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

func TestLiveNotificationDeliveryRecordRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_DELIVERY_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_DELIVERY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-delivery-store database unavailable")
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

	userID := insertFixtureUser(t, ctx, tx, "notifydeliverystore-a@example.com")
	deviceID := insertFixtureDevice(t, ctx, tx, userID, "https://push.example.invalid/delivery/device-a")
	otherDeviceID := insertFixtureDevice(t, ctx, tx, userID, "https://push.example.invalid/delivery/device-other")

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	eventMatched := "evt_" + hex64("delivery-matched-1")
	insertFixtureAlertEvent(t, ctx, tx, eventMatched, "matched", base, base.Add(24*time.Hour))
	otherEvent := "evt_" + hex64("delivery-matched-other")
	insertFixtureAlertEvent(t, ctx, tx, otherEvent, "matched", base, base.Add(24*time.Hour))

	outboxStore := &notifyoutboxstore.Postgres{DB: tx}
	outboxEntry, err := outboxStore.Enqueue(ctx, base, eventMatched, notifydevices.DeviceID(deviceID), identity.UserID(userID), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("fixture outbox Enqueue failed: %v", err)
	}
	// A second, unrelated outbox row, for the mismatched-FK tests below.
	otherOutboxEntry, err := outboxStore.Enqueue(ctx, base, otherEvent, notifydevices.DeviceID(otherDeviceID), identity.UserID(userID), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("fixture second outbox Enqueue failed: %v", err)
	}

	p := &Postgres{DB: tx}

	// 1. A correct composite (outbox_id, event_id, device_id) triple
	// succeeds and returns a database-generated id/created_at.
	first, err := p.Record(ctx, notifydelivery.Delivery{
		OutboxID:      outboxEntry.ID,
		EventID:       eventMatched,
		DeviceID:      notifydevices.DeviceID(deviceID),
		Outcome:       notifydelivery.OutcomeFailed,
		AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("Record failed against a real database: %v", err)
	}
	if first.ID == 0 {
		t.Fatal("expected a database-generated, non-zero id")
	}
	if first.CreatedAt.IsZero() {
		t.Fatal("expected a database-generated, non-zero created_at")
	}
	if first.Outcome != notifydelivery.OutcomeFailed || first.AttemptNumber != 1 {
		t.Fatalf("unexpected recorded delivery: %+v", first)
	}

	// 2. Multiple delivery rows for the same outbox are allowed by the
	// current schema (no uniqueness constraint on (outbox_id,
	// attempt_number) -- see the Part 7 discovery report's own finding):
	// a second attempt against the same outbox succeeds too.
	second, err := p.Record(ctx, notifydelivery.Delivery{
		OutboxID:      outboxEntry.ID,
		EventID:       eventMatched,
		DeviceID:      notifydevices.DeviceID(deviceID),
		Outcome:       notifydelivery.OutcomeSent,
		AttemptNumber: 2,
	})
	if err != nil {
		t.Fatalf("second Record failed: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expected a distinct id for the second delivery row")
	}
	var deliveryCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1`, int64(outboxEntry.ID)).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 2 {
		t.Fatalf("expected exactly 2 delivery rows for this outbox entry, got %d", deliveryCount)
	}

	// 2b. The schema also does not prevent recording the SAME
	// attempt_number twice for the same outbox -- a direct demonstration
	// of the "no uniqueness/idempotency guarantee" finding, using this
	// package's own Record (not a raw-SQL bypass).
	duplicateAttempt, err := p.Record(ctx, notifydelivery.Delivery{
		OutboxID:      outboxEntry.ID,
		EventID:       eventMatched,
		DeviceID:      notifydevices.DeviceID(deviceID),
		Outcome:       notifydelivery.OutcomeUnauthorized,
		AttemptNumber: 2, // reuses attempt_number=2, already used by "second" above
	})
	if err != nil {
		t.Fatalf("expected the schema to permit a repeated attempt_number, got %v", err)
	}
	if duplicateAttempt.ID == second.ID {
		t.Fatal("expected a distinct id even for a repeated attempt_number")
	}

	// 3. Every valid Outcome value is accepted.
	for i, o := range []notifydelivery.Outcome{
		notifydelivery.OutcomeSent, notifydelivery.OutcomeFailed, notifydelivery.OutcomeExpired,
		notifydelivery.OutcomeCanceled, notifydelivery.OutcomeDeadLetter, notifydelivery.OutcomeUnauthorized,
	} {
		if _, err := p.Record(ctx, notifydelivery.Delivery{
			OutboxID:      outboxEntry.ID,
			EventID:       eventMatched,
			DeviceID:      notifydevices.DeviceID(deviceID),
			Outcome:       o,
			AttemptNumber: 10 + i,
		}); err != nil {
			t.Fatalf("expected outcome %q to be accepted, got %v", o, err)
		}
	}

	// 4. A valid error_code round-trips.
	code := "quiet_hours_suppressed"
	withCode, err := p.Record(ctx, notifydelivery.Delivery{
		OutboxID:      outboxEntry.ID,
		EventID:       eventMatched,
		DeviceID:      notifydevices.DeviceID(deviceID),
		Outcome:       notifydelivery.OutcomeCanceled,
		AttemptNumber: 20,
		ErrorCode:     &code,
	})
	if err != nil {
		t.Fatalf("Record with a valid error_code failed: %v", err)
	}
	if withCode.ErrorCode == nil || *withCode.ErrorCode != code {
		t.Fatalf("expected error_code to round-trip, got %+v", withCode.ErrorCode)
	}

	// 5. Mismatched composite FK: a real outbox_id paired with a device_id
	// that does not match that outbox row's own device_id is rejected.
	// A real constraint violation aborts the transaction, so this runs
	// inside its own SAVEPOINT.
	withSavepoint(t, ctx, tx, "mismatched_device", func() {
		if _, err := p.Record(ctx, notifydelivery.Delivery{
			OutboxID:      outboxEntry.ID,
			EventID:       eventMatched,
			DeviceID:      notifydevices.DeviceID(otherDeviceID), // wrong device for this outbox row
			Outcome:       notifydelivery.OutcomeSent,
			AttemptNumber: 1,
		}); err != ErrInput {
			t.Fatalf("expected ErrInput for a mismatched device_id, got %v", err)
		}
	})

	// 6. Mismatched composite FK: a real outbox_id paired with a real, but
	// wrong, event_id (belonging to a different outbox row entirely) is
	// also rejected.
	withSavepoint(t, ctx, tx, "mismatched_event", func() {
		if _, err := p.Record(ctx, notifydelivery.Delivery{
			OutboxID:      outboxEntry.ID,
			EventID:       otherEvent, // wrong event for this outbox row
			DeviceID:      notifydevices.DeviceID(deviceID),
			Outcome:       notifydelivery.OutcomeSent,
			AttemptNumber: 1,
		}); err != ErrInput {
			t.Fatalf("expected ErrInput for a mismatched event_id, got %v", err)
		}
	})

	// 7. A nonexistent outbox_id is rejected the same way.
	withSavepoint(t, ctx, tx, "nonexistent_outbox", func() {
		if _, err := p.Record(ctx, notifydelivery.Delivery{
			OutboxID:      notifyoutbox.OutboxID(999999999),
			EventID:       eventMatched,
			DeviceID:      notifydevices.DeviceID(deviceID),
			Outcome:       notifydelivery.OutcomeSent,
			AttemptNumber: 1,
		}); err != ErrInput {
			t.Fatalf("expected ErrInput for a nonexistent outbox_id, got %v", err)
		}
	})

	// Confirm none of the rejected attempts left any row behind, and the
	// second, unrelated outbox row's own delivery history remains empty.
	var otherOutboxDeliveryCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1`, int64(otherOutboxEntry.ID)).Scan(&otherOutboxDeliveryCount); err != nil {
		t.Fatal(err)
	}
	if otherOutboxDeliveryCount != 0 {
		t.Fatalf("expected zero delivery rows for the unrelated outbox entry, got %d", otherOutboxDeliveryCount)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

// TestLiveNotificationDeliveryConstraintsAndImmutabilityRollsBack proves the
// migration's own CHECK constraints and immutability trigger still
// independently reject malformed data and any mutation attempt, bypassing
// this package's own Delivery.Validate() and Record entirely -- direct-SQL
// defense-in-depth, mirroring every sibling store's own constraint tests.
func TestLiveNotificationDeliveryConstraintsAndImmutabilityRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_DELIVERY_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_DELIVERY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-delivery-store database unavailable")
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

	userID := insertFixtureUser(t, ctx, tx, "notifydeliverystore-constraints@example.com")
	deviceID := insertFixtureDevice(t, ctx, tx, userID, "https://push.example.invalid/delivery/device-constraints")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	eventID := "evt_" + hex64("delivery-constraints-1")
	insertFixtureAlertEvent(t, ctx, tx, eventID, "matched", base, base.Add(24*time.Hour))

	outboxStore := &notifyoutboxstore.Postgres{DB: tx}
	outboxEntry, err := outboxStore.Enqueue(ctx, base, eventID, notifydevices.DeviceID(deviceID), identity.UserID(userID), 3, base.Add(time.Minute), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("fixture outbox Enqueue failed: %v", err)
	}

	// 1. Invalid outcome, bypassing Go entirely.
	withSavepoint(t, ctx, tx, "invalid_outcome", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (outbox_id, event_id, device_id, outcome, attempt_number)
            VALUES ($1, $2, $3, 'pending', 1)`, int64(outboxEntry.ID), eventID, deviceID); err == nil {
			t.Fatal("expected the database's own outcome CHECK constraint to reject an unlisted value")
		}
	})

	// 2. attempt_number <= 0, bypassing Go entirely.
	withSavepoint(t, ctx, tx, "invalid_attempt_number", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (outbox_id, event_id, device_id, outcome, attempt_number)
            VALUES ($1, $2, $3, 'sent', 0)`, int64(outboxEntry.ID), eventID, deviceID); err == nil {
			t.Fatal("expected the database's own attempt_number CHECK constraint to reject a non-positive value")
		}
	})
	withSavepoint(t, ctx, tx, "negative_attempt_number", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (outbox_id, event_id, device_id, outcome, attempt_number)
            VALUES ($1, $2, $3, 'sent', -1)`, int64(outboxEntry.ID), eventID, deviceID); err == nil {
			t.Fatal("expected the database's own attempt_number CHECK constraint to reject a negative value")
		}
	})

	// 3. Malformed error_code, bypassing Go entirely.
	withSavepoint(t, ctx, tx, "malformed_error_code", func() {
		if _, err := tx.Exec(ctx, `INSERT INTO notification_deliveries (outbox_id, event_id, device_id, outcome, attempt_number, error_code)
            VALUES ($1, $2, $3, 'failed', 1, 'BAD CODE')`, int64(outboxEntry.ID), eventID, deviceID); err == nil {
			t.Fatal("expected the database's own error_code CHECK constraint to reject an uppercase/spaced value")
		}
	})

	var count int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE outbox_id = $1`, int64(outboxEntry.ID)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected zero rows after every constraint-violating attempt was rejected, got %d", count)
	}

	// 4. Immutability: insert one real row via this package's own Record,
	// then confirm UPDATE, DELETE, and TRUNCATE are all rejected by the
	// migration's own trigger.
	p := &Postgres{DB: tx}
	recorded, err := p.Record(ctx, notifydelivery.Delivery{
		OutboxID:      outboxEntry.ID,
		EventID:       eventID,
		DeviceID:      notifydevices.DeviceID(deviceID),
		Outcome:       notifydelivery.OutcomeSent,
		AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("fixture Record failed: %v", err)
	}

	withSavepoint(t, ctx, tx, "immutable_update", func() {
		if _, err := tx.Exec(ctx, `UPDATE notification_deliveries SET outcome = 'failed' WHERE id = $1`, int64(recorded.ID)); err == nil {
			t.Fatal("expected a direct UPDATE against notification_deliveries to be rejected by its immutability trigger")
		}
	})
	withSavepoint(t, ctx, tx, "immutable_delete", func() {
		if _, err := tx.Exec(ctx, `DELETE FROM notification_deliveries WHERE id = $1`, int64(recorded.ID)); err == nil {
			t.Fatal("expected a direct DELETE against notification_deliveries to be rejected by its immutability trigger")
		}
	})
	withSavepoint(t, ctx, tx, "immutable_truncate", func() {
		if _, err := tx.Exec(ctx, `TRUNCATE notification_deliveries`); err == nil {
			t.Fatal("expected a direct TRUNCATE against notification_deliveries to be rejected by its immutability trigger")
		}
	})

	// Confirm the row recorded in step 4 still exists, completely
	// untouched, after every rejected mutation attempt.
	var stillOutcome string
	if err := tx.QueryRow(ctx, `SELECT outcome FROM notification_deliveries WHERE id = $1`, int64(recorded.ID)).Scan(&stillOutcome); err != nil {
		t.Fatalf("expected the row to still exist after every rejected mutation attempt: %v", err)
	}
	if stillOutcome != "sent" {
		t.Fatalf("expected the row's outcome to remain unchanged, got %q", stillOutcome)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

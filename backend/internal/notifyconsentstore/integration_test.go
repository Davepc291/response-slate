package notifyconsentstore

// Optional real-database verification, mirroring the existing
// notifydevicestore/alertstore/identitystore convention: skipped unless
// explicitly opted into a local rollback-only database, since this
// sandbox/CI environment cannot assume a running PostgreSQL instance.
// Everything here runs inside one transaction that is always rolled back;
// no fixture is left behind.
//
// This exercises the already-applied migration 000010 schema (the
// notification_consents table's composite ownership foreign key and its
// hard immutability trigger) through Record/Current themselves, the same
// behaviors a future database/tests/000010_schema_test.sql would check
// directly in psql.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifyconsent"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

// insertFixtureDevice inserts a bare notification_devices row directly via
// SQL (rather than going through notifydevicestore, which this package must
// not depend on) so these tests have a real device id to reference from
// notification_consents' own composite ownership foreign key.
func insertFixtureDevice(t *testing.T, ctx context.Context, tx pgx.Tx, userID int64, endpoint string, revokedAt *time.Time) int64 {
	t.Helper()
	raw := make([]byte, 65)
	raw[0] = 0x04
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO notification_devices (user_id, endpoint, p256dh, auth, platform, created_at, updated_at, revoked_at)
        VALUES ($1, $2, $3, $4, NULL, now(), now(), $5) RETURNING id`,
		userID, endpoint, raw, make([]byte, 16), revokedAt).Scan(&id); err != nil {
		t.Fatalf("fixture device insert failed: %v", err)
	}
	return id
}

// withSavepoint runs fn inside a Postgres SAVEPOINT and always rolls back
// to it afterward. A real constraint-violation error (as opposed to
// ErrNotFound/ErrForbidden values this package's sibling notifydevicestore
// derives from an ordinary zero-row SELECT) aborts the entire enclosing
// transaction at the Postgres level; this lets one test intentionally
// trigger such an error and then keep using the same shared, rollback-only
// outer transaction for the rest of its assertions.
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

func TestLiveNotificationConsentRecordAndCurrentRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_CONSENT_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_CONSENT_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-consent-store database unavailable")
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

	var userA, userB int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ('notifyconsentstore-a@example.com', 'Consent Test A', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userA); err != nil {
		t.Fatalf("fixture user A insert failed: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ('notifyconsentstore-b@example.com', 'Consent Test B', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userB); err != nil {
		t.Fatalf("fixture user B insert failed: %v", err)
	}

	deviceA := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/consent/device-a", nil)
	deviceB := insertFixtureDevice(t, ctx, tx, userB, "https://push.example.invalid/consent/device-b", nil)

	p := &Postgres{DB: tx}
	now := time.Now().UTC().Truncate(time.Microsecond)

	// 1. No consent history yet: Current is (false, false, nil).
	granted, ok, err := p.Current(ctx, notifydevices.DeviceID(deviceA))
	if err != nil {
		t.Fatalf("unexpected error on empty Current: %v", err)
	}
	if granted || ok {
		t.Fatalf("expected (false, false, nil) with no consent history, got (%v, %v)", granted, ok)
	}

	// 2. Grant.
	c1, err := p.Record(ctx, now, identity.UserID(userA), notifydevices.DeviceID(deviceA), notifyconsent.EventGranted)
	if err != nil {
		t.Fatalf("Record (grant) failed against a real database: %v", err)
	}
	if c1.Event != notifyconsent.EventGranted || c1.UserID != identity.UserID(userA) || c1.DeviceID != notifydevices.DeviceID(deviceA) {
		t.Fatalf("unexpected recorded consent: %+v", c1)
	}
	granted, ok, err = p.Current(ctx, notifydevices.DeviceID(deviceA))
	if err != nil || !ok || !granted {
		t.Fatalf("expected (true, true, nil) after a grant, got (%v, %v, %v)", granted, ok, err)
	}

	// 3. Revoke.
	revokeAt := now.Add(time.Minute)
	if _, err := p.Record(ctx, revokeAt, identity.UserID(userA), notifydevices.DeviceID(deviceA), notifyconsent.EventRevoked); err != nil {
		t.Fatalf("Record (revoke) failed: %v", err)
	}
	granted, ok, err = p.Current(ctx, notifydevices.DeviceID(deviceA))
	if err != nil || !ok || granted {
		t.Fatalf("expected (false, true, nil) after a revoke, got (%v, %v, %v)", granted, ok, err)
	}

	// 4. Grant again (grant/revoke/grant sequence complete).
	regrantAt := now.Add(2 * time.Minute)
	if _, err := p.Record(ctx, regrantAt, identity.UserID(userA), notifydevices.DeviceID(deviceA), notifyconsent.EventGranted); err != nil {
		t.Fatalf("Record (re-grant) failed: %v", err)
	}
	granted, ok, err = p.Current(ctx, notifydevices.DeviceID(deviceA))
	if err != nil || !ok || !granted {
		t.Fatalf("expected (true, true, nil) after grant/revoke/grant, got (%v, %v, %v)", granted, ok, err)
	}

	// 5. Repeated consecutive event (granted -> granted at the SAME
	// timestamp) is not deduplicated: both rows persist, and Current's own
	// "id DESC" tiebreaker (not just created_at DESC) determines the
	// latest one.
	if _, err := p.Record(ctx, regrantAt, identity.UserID(userA), notifydevices.DeviceID(deviceA), notifyconsent.EventGranted); err != nil {
		t.Fatalf("Record (repeated grant at identical timestamp) failed: %v", err)
	}
	var deviceARows int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_consents WHERE device_id = $1`, deviceA).Scan(&deviceARows); err != nil {
		t.Fatal(err)
	}
	if deviceARows != 4 {
		t.Fatalf("expected 4 undeduplicated consent rows for deviceA (grant, revoke, grant, grant), got %d", deviceARows)
	}
	granted, ok, err = p.Current(ctx, notifydevices.DeviceID(deviceA))
	if err != nil || !ok || !granted {
		t.Fatalf("expected (true, true, nil) after a repeated grant, got (%v, %v, %v)", granted, ok, err)
	}

	// 6. Wrong ownership: deviceB belongs to userB, not userA. The
	// composite FK must reject this, and nothing may be inserted. A real
	// FK-violation error (unlike ErrNotFound/ErrForbidden derived from an
	// ordinary zero-row SELECT elsewhere in this package's sibling
	// notifydevicestore) aborts the current Postgres transaction, so this
	// expected-failure statement runs inside its own SAVEPOINT: rolling
	// back to it afterward lets the shared, otherwise-rollback-only outer
	// transaction keep running the rest of this test.
	withSavepoint(t, ctx, tx, "wrong_ownership", func() {
		if _, err := p.Record(ctx, now, identity.UserID(userA), notifydevices.DeviceID(deviceB), notifyconsent.EventGranted); err != ErrForbidden {
			t.Fatalf("expected ErrForbidden for a cross-user (deviceID, userID) pair, got %v", err)
		}
	})
	var deviceBRows int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_consents WHERE device_id = $1`, deviceB).Scan(&deviceBRows); err != nil {
		t.Fatal(err)
	}
	if deviceBRows != 0 {
		t.Fatalf("expected zero consent rows for deviceB after a rejected wrong-ownership attempt, got %d", deviceBRows)
	}

	// 7. A nonexistent device id is likewise rejected by the same FK path.
	withSavepoint(t, ctx, tx, "nonexistent_device", func() {
		if _, err := p.Record(ctx, now, identity.UserID(userA), notifydevices.DeviceID(999999999), notifyconsent.EventGranted); err != ErrForbidden {
			t.Fatalf("expected ErrForbidden for a nonexistent device id, got %v", err)
		}
	})

	// 8. Consent recorded against an already-revoked device must still
	// succeed: the composite FK has no revoked_at IS NULL condition, so a
	// revoked device is not required to be currently active.
	revokedDeviceTime := now
	revokedDevice := insertFixtureDevice(t, ctx, tx, userA, "https://push.example.invalid/consent/device-revoked", &revokedDeviceTime)
	revokedConsent, err := p.Record(ctx, now.Add(3*time.Minute), identity.UserID(userA), notifydevices.DeviceID(revokedDevice), notifyconsent.EventRevoked)
	if err != nil {
		t.Fatalf("expected recording consent against an already-revoked device to succeed, got %v", err)
	}
	if revokedConsent.DeviceID != notifydevices.DeviceID(revokedDevice) {
		t.Fatalf("unexpected recorded consent: %+v", revokedConsent)
	}
	grantedForRevokedDevice, ok, err := p.Current(ctx, notifydevices.DeviceID(revokedDevice))
	if err != nil || !ok || grantedForRevokedDevice {
		t.Fatalf("expected (false, true, nil) for the revoked device's own recorded consent, got (%v, %v, %v)", grantedForRevokedDevice, ok, err)
	}

	// 9. userB's own device still has no consent history: the wrong-owner
	// and nonexistent-device rejections above never leaked a row onto it.
	grantedB, okB, err := p.Current(ctx, notifydevices.DeviceID(deviceB))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grantedB || okB {
		t.Fatalf("expected deviceB to still have no consent history, got (%v, %v)", grantedB, okB)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

// TestLiveNotificationConsentImmutableTriggerRollsBack re-confirms migration
// 000010's own notification_consents_immutable trigger (shared with
// notification_deliveries via the same reject_alert_audit_mutation()
// function) still rejects a direct UPDATE or DELETE against a row this
// package itself just wrote through Record -- proving append-only holds
// end to end, not merely that this package never issues such a statement
// itself.
func TestLiveNotificationConsentImmutableTriggerRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_CONSENT_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_CONSENT_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-consent-store database unavailable")
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

	var userID int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ('notifyconsentstore-immutable@example.com', 'Consent Test Immutable', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}
	device := insertFixtureDevice(t, ctx, tx, userID, "https://push.example.invalid/consent/immutable", nil)

	p := &Postgres{DB: tx}
	c, err := p.Record(ctx, time.Now().UTC().Truncate(time.Microsecond), identity.UserID(userID), notifydevices.DeviceID(device), notifyconsent.EventGranted)
	if err != nil {
		t.Fatalf("fixture Record failed: %v", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE notification_consents SET event = 'revoked' WHERE id = $1`, c.ID); err == nil {
		t.Fatal("expected a direct UPDATE against notification_consents to be rejected by its immutability trigger")
	}

	// The rejected UPDATE aborts this transaction at the Postgres level
	// (a statement-level error poisons the rest of the transaction), so it
	// can only be rolled back now, not reused for the DELETE case below --
	// exactly the same "rollback-only, no fixture left behind" outcome
	// either way, just reached one statement sooner than usual.
	cleanup, c2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer c2()
	_ = tx.Rollback(cleanup)
	rolledBack = true

	// A fresh transaction verifies the DELETE case independently.
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("second test transaction failed")
	}
	rolledBack2 := false
	defer func() {
		if !rolledBack2 {
			cleanup, cc := context.WithTimeout(context.Background(), 5*time.Second)
			defer cc()
			_ = tx2.Rollback(cleanup)
		}
	}()

	var userID2 int64
	if err := tx2.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ('notifyconsentstore-immutable-delete@example.com', 'Consent Test Immutable Delete', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userID2); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}
	device2 := insertFixtureDevice(t, ctx, tx2, userID2, "https://push.example.invalid/consent/immutable-delete", nil)
	p2 := &Postgres{DB: tx2}
	c2Consent, err := p2.Record(ctx, time.Now().UTC().Truncate(time.Microsecond), identity.UserID(userID2), notifydevices.DeviceID(device2), notifyconsent.EventGranted)
	if err != nil {
		t.Fatalf("fixture Record failed: %v", err)
	}

	if _, err := tx2.Exec(ctx, `DELETE FROM notification_consents WHERE id = $1`, c2Consent.ID); err == nil {
		t.Fatal("expected a direct DELETE against notification_consents to be rejected by its immutability trigger")
	}

	cleanup2, cc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cc()
	_ = tx2.Rollback(cleanup2)
	rolledBack2 = true
}

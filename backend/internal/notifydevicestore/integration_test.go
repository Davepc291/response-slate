package notifydevicestore

// Optional real-database verification, mirroring the existing
// alertstore/identitystore/transcriptreview convention: skipped unless
// explicitly opted into a local rollback-only database, since this
// sandbox/CI environment cannot assume a running PostgreSQL instance.
// Everything here runs inside one transaction that is always rolled back;
// no fixture is left behind.
//
// This exercises the already-applied, already-fixed migration 000010
// schema (endpoint-uniqueness partial index, the p256dh/auth bytea CHECKs,
// idempotent re-registration, and cross-user rejection) through Register
// itself, the same behaviors a future database/tests/000010_schema_test.sql
// would check directly in psql.

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
)

func TestLiveNotificationDeviceRegisterRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_DEVICES_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_DEVICES_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-device-store database unavailable")
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
        VALUES ('notifydevicestore-a@example.com', 'Store Test A', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userA); err != nil {
		t.Fatalf("fixture user A insert failed: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ('notifydevicestore-b@example.com', 'Store Test B', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userB); err != nil {
		t.Fatalf("fixture user B insert failed: %v", err)
	}

	p := &Postgres{DB: tx}
	now := time.Now().UTC().Truncate(time.Microsecond)

	rawP256dh := make([]byte, 65)
	rawP256dh[0] = 0x04
	rawAuth := make([]byte, 16)
	rawAuth[15] = 0x01 // distinguishable from an all-zero fixture elsewhere
	sub := notifydevices.Subscription{
		Endpoint: "https://push.example.invalid/send/live-test-one",
		Keys: notifydevices.Keys{
			P256dh: base64.RawURLEncoding.EncodeToString(rawP256dh),
			Auth:   base64.RawURLEncoding.EncodeToString(rawAuth),
		},
	}

	// 1. Fresh registration.
	first, err := p.Register(ctx, now, identity.UserID(userA), sub, "android-chrome")
	if err != nil {
		t.Fatalf("Register failed against a real database: %v", err)
	}
	if !first.Active() {
		t.Fatal("expected a freshly registered device to be active")
	}

	// 2. base64url -> bytea storage is correct: read the raw column back
	// directly (bypassing Register's own re-encoding) and compare bytes.
	var storedP256dh, storedAuth []byte
	if err := tx.QueryRow(ctx, `SELECT p256dh, auth FROM notification_devices WHERE id = $1`, int64(first.ID)).
		Scan(&storedP256dh, &storedAuth); err != nil {
		t.Fatalf("reading back raw bytea columns failed: %v", err)
	}
	if string(storedP256dh) != string(rawP256dh) {
		t.Fatalf("stored p256dh bytes do not match the original decoded subscription key")
	}
	if string(storedAuth) != string(rawAuth) {
		t.Fatalf("stored auth bytes do not match the original decoded subscription key")
	}

	// 3. Same-user re-registration is idempotent: same DeviceID, no second row.
	later := now.Add(time.Minute)
	second, err := p.Register(ctx, later, identity.UserID(userA), sub, "android-chrome-updated")
	if err != nil {
		t.Fatalf("idempotent re-registration failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected re-registering the same endpoint for the same user to update the existing row, got a new id %v vs %v", second.ID, first.ID)
	}
	if second.Platform != "android-chrome-updated" {
		t.Fatalf("expected the platform hint to refresh, got %q", second.Platform)
	}
	var activeCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_devices WHERE endpoint = $1 AND revoked_at IS NULL`, sub.Endpoint).
		Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active registration for this endpoint, got %d", activeCount)
	}

	// 4. Cross-user registration of an active endpoint is rejected.
	if _, err := p.Register(ctx, later, identity.UserID(userB), sub, ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for a cross-user registration attempt against a real database, got %v", err)
	}
	// Confirm nothing was mutated by the rejected attempt.
	var stillOwner int64
	if err := tx.QueryRow(ctx, `SELECT user_id FROM notification_devices WHERE endpoint = $1 AND revoked_at IS NULL`, sub.Endpoint).
		Scan(&stillOwner); err != nil {
		t.Fatal(err)
	}
	if stillOwner != userA {
		t.Fatalf("expected the endpoint to remain owned by user A after a rejected cross-user attempt, owner is now %d", stillOwner)
	}

	// 5. Revoke the row directly (Revoke itself is not implemented by this
	// package yet), then confirm a fresh registration of the same endpoint
	// is accepted as a brand-new active row, not blocked or resurrected.
	if _, err := tx.Exec(ctx, `UPDATE notification_devices SET revoked_at = $1 WHERE id = $2`, later, int64(first.ID)); err != nil {
		t.Fatalf("fixture revoke failed: %v", err)
	}
	third, err := p.Register(ctx, later.Add(time.Minute), identity.UserID(userB), sub, "")
	if err != nil {
		t.Fatalf("expected registering a previously revoked endpoint to succeed, got %v", err)
	}
	if third.ID == first.ID {
		t.Fatal("expected a fresh DeviceID distinct from the revoked one")
	}
	if third.UserID != identity.UserID(userB) {
		t.Fatalf("expected the fresh registration to belong to user B, got %v", third.UserID)
	}
	if !third.Active() {
		t.Fatal("expected the fresh registration to be active")
	}

	// 6. Get round-trips an active registration end to end, including the
	// exact canonical base64url keys, against the real schema (not just the
	// fake row Register's own unit tests use).
	gotThird, ok, err := p.Get(ctx, third.ID)
	if err != nil {
		t.Fatalf("Get failed against a real database: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for an existing, active registration")
	}
	if gotThird.ID != third.ID || gotThird.UserID != identity.UserID(userB) || gotThird.Subscription.Endpoint != sub.Endpoint {
		t.Fatalf("unexpected identity/endpoint fields: %+v", gotThird)
	}
	if gotThird.Subscription.Keys.P256dh != sub.Keys.P256dh || gotThird.Subscription.Keys.Auth != sub.Keys.Auth {
		t.Fatalf("expected Get's keys to canonically match the original subscription: %+v", gotThird.Subscription.Keys)
	}
	if !gotThird.Active() || gotThird.RevokedAt != nil || gotThird.SupersededBy != 0 {
		t.Fatalf("expected an active, non-superseded registration, got %+v", gotThird)
	}

	// 7. Get does not hide a revoked registration (first was revoked in step
	// 5). Also set superseded_by directly via SQL (Replace itself is not
	// implemented yet) to prove that field round-trips too.
	if _, err := tx.Exec(ctx, `UPDATE notification_devices SET superseded_by = $1 WHERE id = $2`, int64(third.ID), int64(first.ID)); err != nil {
		t.Fatalf("fixture superseded_by update failed: %v", err)
	}
	gotFirst, ok, err := p.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get failed against a real database: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true: a revoked registration must still be retrievable by id, not hidden as not-found")
	}
	if gotFirst.Active() {
		t.Fatal("expected the returned registration to reflect its revoked state")
	}
	if gotFirst.RevokedAt == nil {
		t.Fatal("expected RevokedAt to be set")
	}
	if gotFirst.SupersededBy != third.ID {
		t.Fatalf("expected SupersededBy to round-trip as %v, got %v", third.ID, gotFirst.SupersededBy)
	}
	if gotFirst.Platform != "android-chrome-updated" {
		t.Fatalf("expected the platform hint from step 3 to round-trip, got %q", gotFirst.Platform)
	}

	// 8. A nonexistent DeviceID returns ok=false with a nil error.
	_, ok, err = p.Get(ctx, notifydevices.DeviceID(999999999))
	if err != nil {
		t.Fatalf("expected a nil error for a nonexistent id, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a nonexistent id")
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

// TestLiveNotificationDeviceRegisterAfterOwnRevokeRollsBack covers the one
// revoked-endpoint-reuse variant the main test above does not: the SAME
// user revoking their own registration and then re-registering that same
// endpoint, rather than a different user picking it up. Both should behave
// identically (the partial unique index simply does not see a revoked row,
// regardless of who revoked it or who registers next), but this proves that
// explicitly rather than only by code-path inspection.
func TestLiveNotificationDeviceRegisterAfterOwnRevokeRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_DEVICES_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_DEVICES_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-device-store database unavailable")
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
        VALUES ('notifydevicestore-selfrevoke@example.com', 'Store Test Self-Revoke', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("fixture user insert failed: %v", err)
	}

	p := &Postgres{DB: tx}
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := notifydevices.Subscription{
		Endpoint: "https://push.example.invalid/send/self-revoke",
		Keys: notifydevices.Keys{
			P256dh: base64.RawURLEncoding.EncodeToString(func() []byte { b := make([]byte, 65); b[0] = 0x04; return b }()),
			Auth:   base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		},
	}

	original, err := p.Register(ctx, now, identity.UserID(userID), sub, "")
	if err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}

	revokedAt := now.Add(time.Minute)
	if _, err := tx.Exec(ctx, `UPDATE notification_devices SET revoked_at = $1 WHERE id = $2`, revokedAt, int64(original.ID)); err != nil {
		t.Fatalf("fixture revoke failed: %v", err)
	}

	fresh, err := p.Register(ctx, revokedAt.Add(time.Minute), identity.UserID(userID), sub, "")
	if err != nil {
		t.Fatalf("expected the same user re-registering their own revoked endpoint to succeed, got %v", err)
	}
	if fresh.ID == original.ID {
		t.Fatal("expected a fresh DeviceID distinct from the revoked one, not the same row resurrected")
	}
	if fresh.UserID != identity.UserID(userID) {
		t.Fatalf("expected the fresh registration to still belong to the same user, got %v", fresh.UserID)
	}
	if !fresh.Active() {
		t.Fatal("expected the fresh registration to be active")
	}

	var activeCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_devices WHERE endpoint = $1 AND revoked_at IS NULL`, sub.Endpoint).
		Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active registration for this endpoint, got %d", activeCount)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

// TestLiveNotificationDeviceReplaceRollsBack proves the guarantees the fake
// pgx.Tx-based unit tests can't honestly stand in for: the real partial
// unique index, the real composite ownership foreign key, and real
// Postgres-level rollback leaving zero physical trace of a rejected
// replacement.
func TestLiveNotificationDeviceReplaceRollsBack(t *testing.T) {
	if os.Getenv("GFR_NOTIFY_DEVICES_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required (set GFR_NOTIFY_DEVICES_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatal("local notification-device-store database unavailable")
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
        VALUES ('notifydevicestore-replace-a@example.com', 'Replace Test A', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userA); err != nil {
		t.Fatalf("fixture user A insert failed: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ('notifydevicestore-replace-b@example.com', 'Replace Test B', 'responder', 'active',
        '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaHZhbHVl', now()) RETURNING id`).Scan(&userB); err != nil {
		t.Fatalf("fixture user B insert failed: %v", err)
	}

	p := &Postgres{DB: tx}
	now := time.Now().UTC().Truncate(time.Microsecond)

	newSub := func(path string) notifydevices.Subscription {
		raw := make([]byte, 65)
		raw[0] = 0x04
		return notifydevices.Subscription{
			Endpoint: "https://push.example.invalid/replace/" + path,
			Keys: notifydevices.Keys{
				P256dh: base64.RawURLEncoding.EncodeToString(raw),
				Auth:   base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
			},
		}
	}

	// --- Fixtures for the whole test: A owns "orig" and "other-a"; B owns "other-b". ---
	orig, err := p.Register(ctx, now, identity.UserID(userA), newSub("orig"), "android-chrome")
	if err != nil {
		t.Fatalf("fixture registration (orig) failed: %v", err)
	}
	otherA, err := p.Register(ctx, now, identity.UserID(userA), newSub("other-a"), "")
	if err != nil {
		t.Fatalf("fixture registration (other-a) failed: %v", err)
	}
	otherB, err := p.Register(ctx, now, identity.UserID(userB), newSub("other-b"), "")
	if err != nil {
		t.Fatalf("fixture registration (other-b) failed: %v", err)
	}

	// 1. Nonexistent old id.
	if _, err := p.Replace(ctx, now.Add(time.Minute), identity.UserID(userA), notifydevices.DeviceID(999999999), newSub("nope"), ""); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for a nonexistent old id, got %v", err)
	}

	// 2. Wrong owner: userB attempts to replace userA's "orig" registration.
	if _, err := p.Replace(ctx, now.Add(time.Minute), identity.UserID(userB), orig.ID, newSub("stolen"), ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for a wrong-owner replace, got %v", err)
	}
	// Confirm zero mutation: orig is still active, untouched, and no
	// "stolen" endpoint row exists.
	unchanged, ok, err := p.Get(ctx, orig.ID)
	if err != nil || !ok {
		t.Fatalf("expected orig to still exist and be readable, ok=%v err=%v", ok, err)
	}
	if !unchanged.Active() || !unchanged.UpdatedAt.Equal(orig.UpdatedAt) {
		t.Fatalf("expected orig to be completely unchanged after a rejected wrong-owner replace, got %+v", unchanged)
	}
	var stolenCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_devices WHERE endpoint = $1`, newSub("stolen").Endpoint).Scan(&stolenCount); err != nil {
		t.Fatal(err)
	}
	if stolenCount != 0 {
		t.Fatal("expected no row to have been created for a rejected wrong-owner replace")
	}

	// 3. New endpoint already active for another device of the SAME user
	// (otherA, owned by userA) must be rejected exactly like a different
	// user's endpoint would be.
	if _, err := p.Replace(ctx, now.Add(time.Minute), identity.UserID(userA), orig.ID, otherA.Subscription, ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden when replacing onto the caller's own other active endpoint, got %v", err)
	}
	unchanged, ok, err = p.Get(ctx, orig.ID)
	if err != nil || !ok || !unchanged.Active() {
		t.Fatalf("expected orig to remain active and untouched, ok=%v err=%v reg=%+v", ok, err, unchanged)
	}
	otherAUnchanged, ok, err := p.Get(ctx, otherA.ID)
	if err != nil || !ok || !otherAUnchanged.Active() {
		t.Fatalf("expected other-a to remain untouched, ok=%v err=%v reg=%+v", ok, err, otherAUnchanged)
	}

	// 4. New endpoint already active for another user (otherB) must also
	// be rejected.
	if _, err := p.Replace(ctx, now.Add(time.Minute), identity.UserID(userA), orig.ID, otherB.Subscription, ""); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden when replacing onto a different user's active endpoint, got %v", err)
	}

	// 5. Same-endpoint replace is an idempotent in-place update: same
	// DeviceID, no supersession.
	later := now.Add(2 * time.Minute)
	idempotent, err := p.Replace(ctx, later, identity.UserID(userA), orig.ID, orig.Subscription, "ios-safari")
	if err != nil {
		t.Fatalf("idempotent same-endpoint replace failed: %v", err)
	}
	if idempotent.ID != orig.ID {
		t.Fatalf("expected the same DeviceID for a same-endpoint replace, got %v vs %v", idempotent.ID, orig.ID)
	}
	if idempotent.Platform != "ios-safari" || idempotent.SupersededBy != 0 || !idempotent.Active() {
		t.Fatalf("expected an in-place, non-superseded update, got %+v", idempotent)
	}

	// 6. Successful real replacement: fresh active row, old row revoked and
	// superseded, correctly linked.
	replaceAt := now.Add(3 * time.Minute)
	fresh, err := p.Replace(ctx, replaceAt, identity.UserID(userA), orig.ID, newSub("rotated"), "android-chrome-rotated")
	if err != nil {
		t.Fatalf("real replacement failed: %v", err)
	}
	if fresh.ID == orig.ID {
		t.Fatal("expected a fresh DeviceID distinct from the old one")
	}
	if !fresh.Active() || fresh.Subscription.Endpoint != newSub("rotated").Endpoint || fresh.Platform != "android-chrome-rotated" {
		t.Fatalf("unexpected fresh registration: %+v", fresh)
	}
	oldAfter, ok, err := p.Get(ctx, orig.ID)
	if err != nil || !ok {
		t.Fatalf("expected the old row to still exist (revoked, not deleted), ok=%v err=%v", ok, err)
	}
	if oldAfter.Active() {
		t.Fatal("expected the old row to no longer be active")
	}
	if oldAfter.RevokedAt == nil || !oldAfter.RevokedAt.Equal(replaceAt) {
		t.Fatalf("expected RevokedAt to equal %v, got %v", replaceAt, oldAfter.RevokedAt)
	}
	if oldAfter.SupersededBy != fresh.ID {
		t.Fatalf("expected SupersededBy to reference %v, got %v", fresh.ID, oldAfter.SupersededBy)
	}
	var activeForOrigEndpoint int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_devices WHERE endpoint = $1 AND revoked_at IS NULL`, orig.Subscription.Endpoint).
		Scan(&activeForOrigEndpoint); err != nil {
		t.Fatal(err)
	}
	if activeForOrigEndpoint != 0 {
		t.Fatal("expected the old endpoint to have no active registration left")
	}

	// 7. Replacing an already-superseded/revoked old id (orig, just revoked
	// in step 6) now fails as not-found, same as any other revoked row.
	if _, err := p.Replace(ctx, replaceAt.Add(time.Minute), identity.UserID(userA), orig.ID, newSub("too-late"), ""); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound when replacing an already-revoked/superseded old id, got %v", err)
	}

	cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err := tx.Rollback(cleanup); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	rolledBack = true
}

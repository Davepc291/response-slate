package identitystore

// Optional real-database verification, mirroring the existing
// alertstore/transcriptreview convention: skipped unless explicitly opted
// into a local database (set GFR_IDENTITY_LIVE_TEST=true and
// GFR_DATABASE_URL), since this sandbox/CI environment cannot assume a
// running PostgreSQL instance. Every fixture is synthetic; no real name,
// email, or credential appears anywhere in this file.
//
// Most tests here wrap their work in one transaction that is always rolled
// back, exactly like alertstore's live test. pgx.Tx itself satisfies this
// package's Pool interface (Begin/QueryRow/Query/Exec), so a *Postgres can
// be backed directly by a transaction for these tests. The one exception is
// TestLiveConcurrentInvitationRedemption, which genuinely needs two
// independent, separately committing transactions racing the same row to
// exercise real cross-connection locking — a single shared transaction
// cannot demonstrate that. That test creates its own fixture rows outside
// any wrapping transaction and removes them explicitly afterward, in
// dependency order, instead of relying on rollback.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/invitation"
	"greenwich-fire-responder/backend/internal/mfa"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
	"greenwich-fire-responder/backend/internal/passwordreset"
	"greenwich-fire-responder/backend/internal/session"
)

func liveTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("GFR_IDENTITY_LIVE_TEST") != "true" {
		t.Skip("explicit local database opt-in required (set GFR_IDENTITY_LIVE_TEST=true and GFR_DATABASE_URL)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, _, err := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if err != nil {
		t.Fatalf("local identity database unavailable: %v", err)
	}
	pool, ok := db.DB.(*pgxpool.Pool)
	if !ok {
		t.Fatal("expected Open to return a *pgxpool.Pool-backed Postgres")
	}
	return pool
}

func beginRollback(t *testing.T, pool *pgxpool.Pool) (context.Context, pgx.Tx, *Postgres) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	})
	return ctx, tx, &Postgres{DB: tx}
}

var testFixtureCounter int

func uniqueEmail(t *testing.T) string {
	t.Helper()
	testFixtureCounter++
	return fmt.Sprintf("synthetic-9b-%d-%d@example.test", os.Getpid(), testFixtureCounter)
}

func hashFor(t *testing.T, password string) string {
	t.Helper()
	params := passwordpolicy.DefaultParams()
	params.Memory, params.Iterations = 8*1024, 1 // fast for tests
	h, err := passwordpolicy.Hash(password, params)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// insertBootstrapAdmin inserts a synthetic already-active administrator
// directly (Step 9B's application code has no such seeding path; this
// mirrors production reality that the very first administrator is
// provisioned once, out of band, exactly as migration 000008's own schema
// test bootstrap fixture does).
func insertBootstrapAdmin(ctx context.Context, t *testing.T, conn Conn) identity.UserID {
	t.Helper()
	testFixtureCounter++
	email := fmt.Sprintf("synthetic-9b-admin-%d-%d@example.test", os.Getpid(), testFixtureCounter)
	var id int64
	row := conn.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, status, password_hash, password_updated_at)
        VALUES ($1, 'Synthetic Bootstrap Admin', 'system_administrator', 'active', $2, now()) RETURNING id`,
		email, "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g")
	if err := row.Scan(&id); err != nil {
		t.Fatalf("bootstrap admin fixture insert failed: %v", err)
	}
	return identity.UserID(id)
}

func TestLiveInvitationAndPasswordEstablishmentRollsBack(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)

	var migrated bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = '000008')`).Scan(&migrated); err != nil || !migrated {
		t.Fatal("migration 000008 is not applied to this database")
	}

	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// 1. Create a dispatcher_operator (no MFA required) and issue an invitation.
	rawToken, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Dispatcher", Role: identity.RoleDispatcherOperator,
		Scope: "engine-2", CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawToken), InvitationExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateUserAndInvite: %v", err)
	}
	u, err := p.GetByID(ctx, userID)
	if err != nil || u.Status != identity.StateInvited {
		t.Fatalf("expected new user to be invited, got %+v err=%v", u, err)
	}

	// 2. Redeeming with the wrong token digest fails generically.
	if _, err := p.RedeemInvitation(ctx, invitation.Digest("wrong-token"), now); err != ErrTokenInvalid {
		t.Fatalf("expected ErrTokenInvalid for an unknown token, got %v", err)
	}

	// 3. Redeeming the real token succeeds and advances the account.
	redeemedID, err := p.RedeemInvitation(ctx, invitation.Digest(rawToken), now)
	if err != nil || redeemedID != userID {
		t.Fatalf("RedeemInvitation: id=%v err=%v", redeemedID, err)
	}
	u, _ = p.GetByID(ctx, userID)
	if u.Status != identity.StatePasswordChangeRequired {
		t.Fatalf("expected password_change_required after redemption, got %s", u.Status)
	}

	// 4. A second redemption of the same token fails (single-use, AAX-06).
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawToken), now); err != ErrTokenInvalid {
		t.Fatalf("expected ErrTokenInvalid on reuse, got %v", err)
	}

	// 5. Establishing a password for a non-MFA role reaches active directly.
	status, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-1"), now)
	if err != nil || status != identity.StateActive {
		t.Fatalf("expected active status, got %s err=%v", status, err)
	}
	u, _ = p.GetByID(ctx, userID)
	if !u.HasPassword() || u.Status != identity.StateActive {
		t.Fatalf("expected active user with password set, got %+v", u)
	}

	// 6. A session can now be created; ResolveSession authorizes it.
	rawSession, _ := session.GenerateToken()
	sid, err := p.CreateSession(ctx, userID, session.Digest(rawSession), "synthetic-device", now, now.Add(time.Hour))
	if err != nil || sid == 0 {
		t.Fatalf("CreateSession: %v", err)
	}
	cfg := session.Config{IdleTimeout: 30 * time.Minute, AbsoluteLifetime: time.Hour}
	authz, err := p.ResolveSession(ctx, session.Digest(rawSession), cfg, now.Add(time.Minute))
	if err != nil || authz.Account.ID != userID {
		t.Fatalf("ResolveSession: %+v err=%v", authz, err)
	}

	// 7. Suspending revokes the session; a suspended account cannot resolve it.
	if err := p.Suspend(ctx, userID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawSession), cfg, now.Add(3*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected suspended account's session to be unresolvable, got %v", err)
	}

	// 8. Restore returns an already-passworded account to active.
	target, err := p.Restore(ctx, userID, now.Add(4*time.Minute))
	if err != nil || target != identity.StateActive {
		t.Fatalf("Restore: %s err=%v", target, err)
	}
}

func TestLiveAdminMFAGateBlocksActivation(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rawToken, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Admin Invitee", Role: identity.RoleDepartmentAdministrator,
		Scope: "engine-2", CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawToken), InvitationExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawToken), now); err != nil {
		t.Fatal(err)
	}
	status, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-2"), now)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.StatePasswordChangeRequired {
		t.Fatalf("expected an administrator role to remain password_change_required without MFA enrolled, got %s", status)
	}
	u, _ := p.GetByID(ctx, userID)
	if !u.HasPassword() {
		t.Fatal("expected the permanent password to already be set even though MFA is still pending")
	}

	// Enroll a synthetic MFA credential directly (Step 9B provides no
	// enrollment path; this simulates a future phase having done so) and
	// confirm re-attempting activation now succeeds.
	if _, err := tx.Exec(ctx, `INSERT INTO mfa_credentials (user_id, credential_type, credential_data)
        VALUES ($1, 'passkey', '\x0102030405')`, int64(userID)); err != nil {
		t.Fatal(err)
	}
	status, err = p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-2"), now.Add(time.Minute))
	if err != nil || status != identity.StateActive {
		t.Fatalf("expected activation to succeed once MFA is enrolled, got %s err=%v", status, err)
	}
}

func TestLiveResendInvalidatesPriorInvitation(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	oldToken, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Resend", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(oldToken), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	newToken, _ := invitation.GenerateToken()
	if err := p.ResendInvitation(ctx, userID, adminID, invitation.Digest(newToken), now.Add(48*time.Hour)); err != nil {
		t.Fatalf("ResendInvitation: %v", err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(oldToken), now); err != ErrTokenInvalid {
		t.Fatalf("expected the superseded old token to be invalid, got %v", err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(newToken), now); err != nil {
		t.Fatalf("expected the newly issued token to redeem successfully, got %v", err)
	}
}

func TestLiveInvitationExpiration(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	issuedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rawToken, _ := invitation.GenerateToken()
	_, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Expiring", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawToken), InvitationExpiresAt: issuedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	afterExpiry := issuedAt.Add(2 * time.Hour)
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawToken), afterExpiry); err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
	// A repeated attempt against the now explicitly-expired row still
	// reports expired, not the generic invalid message.
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawToken), afterExpiry); err != ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired on second attempt too, got %v", err)
	}
}

func TestLivePasswordResetRevokesSessions(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	email := uniqueEmail(t)

	rawInv, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: email, DisplayName: "Synthetic Reset", Role: identity.RoleResponder,
		CreatedBy: adminID, InvitationTokenDigest: invitation.Digest(rawInv), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawInv), now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-3"), now); err != nil {
		t.Fatal(err)
	}
	rawSession, _ := session.GenerateToken()
	if _, err := p.CreateSession(ctx, userID, session.Digest(rawSession), "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The generic response (true) is only returned for a matching, eligible account.
	matched, matchedID, err := p.RequestPasswordReset(ctx, email, 0, passwordreset.Digest("raw-reset-1"), now.Add(24*time.Hour))
	if err != nil || !matched || matchedID != userID {
		t.Fatalf("RequestPasswordReset: matched=%v id=%v err=%v", matched, matchedID, err)
	}
	unmatched, _, err := p.RequestPasswordReset(ctx, "no-such-account@example.test", 0, passwordreset.Digest("raw-reset-2"), now.Add(24*time.Hour))
	if err != nil || unmatched {
		t.Fatalf("expected no error and no match for an unknown email, got matched=%v err=%v", unmatched, err)
	}

	if _, err := p.CompletePasswordReset(ctx, passwordreset.Digest("raw-reset-1"), hashFor(t, "synthetic-test-password-4"), now.Add(2*time.Hour)); err != nil {
		t.Fatalf("CompletePasswordReset: %v", err)
	}
	cfg := session.Config{IdleTimeout: time.Hour, AbsoluteLifetime: 24 * time.Hour}
	if _, err := p.ResolveSession(ctx, session.Digest(rawSession), cfg, now.Add(3*time.Hour)); err != ErrTokenInvalid {
		t.Fatalf("expected the pre-existing session to be revoked by password reset, got %v", err)
	}
}

func TestLiveAdminResetReissuesInvitationAndClearsPassword(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rawInv, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Compromised", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawInv), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawInv), now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-5"), now); err != nil {
		t.Fatal(err)
	}
	rawSession, _ := session.GenerateToken()
	if _, err := p.CreateSession(ctx, userID, session.Digest(rawSession), "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	newRaw, _ := invitation.GenerateToken()
	if err := p.AdminReset(ctx, userID, adminID, invitation.Digest(newRaw), now.Add(time.Hour), now.Add(5*time.Minute)); err != nil {
		t.Fatalf("AdminReset: %v", err)
	}
	u, _ := p.GetByID(ctx, userID)
	if u.Status != identity.StatePasswordChangeRequired || u.HasPassword() {
		t.Fatalf("expected password cleared and status password_change_required, got %+v", u)
	}
	cfg := session.Config{IdleTimeout: time.Hour, AbsoluteLifetime: 24 * time.Hour}
	if _, err := p.ResolveSession(ctx, session.Digest(rawSession), cfg, now.Add(10*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected AdminReset to revoke existing sessions, got %v", err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(newRaw), now.Add(6*time.Minute)); err != nil {
		t.Fatalf("expected the newly issued reset invitation to redeem, got %v", err)
	}
}

// TestLiveAdminResetReissuesWhenAlreadyPasswordChangeRequired covers the
// account-recovery gap where an administrator's first reset code is lost or
// never retained before the recipient uses it: AdminReset must be callable
// again on an account that is already password_change_required (not just
// from active/suspended), it must invalidate the still-pending first code so
// only the newest one ever redeems, and it must leave the account in
// password_change_required throughout.
func TestLiveAdminResetReissuesWhenAlreadyPasswordChangeRequired(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rawInv, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Lost Code", Role: identity.RoleReadOnlyAuditor, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawInv), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawInv), now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-7"), now); err != nil {
		t.Fatal(err)
	}

	// Step 2/3: the administrator's first reset (account is active).
	firstRaw, _ := invitation.GenerateToken()
	if err := p.AdminReset(ctx, userID, adminID, invitation.Digest(firstRaw), now.Add(time.Hour), now.Add(5*time.Minute)); err != nil {
		t.Fatalf("first AdminReset: %v", err)
	}
	u, _ := p.GetByID(ctx, userID)
	if u.Status != identity.StatePasswordChangeRequired {
		t.Fatalf("expected password_change_required after the first reset, got %s", u.Status)
	}

	// While password_change_required (simulating time passing after the
	// first reset, before the lost code is ever used), CreateSession must
	// fail closed rather than create a session: no session exists here for
	// the reissue below to revoke.
	rawSession, _ := session.GenerateToken()
	if _, err := p.CreateSession(ctx, userID, session.Digest(rawSession), "", now.Add(6*time.Minute), now.Add(time.Hour)); err != ErrAccountNotActive {
		t.Fatalf("expected ErrAccountNotActive while password_change_required, got %v", err)
	}

	// Step 4/5: the first code was never retained; the administrator must be
	// able to reissue without first forcing the account through any other
	// state. Before the fix, this returned ErrInvalidTransition because the
	// state-transition matrix had no password_change_required ->
	// password_change_required entry, even though AdminReset's own switch
	// and doc comment already treated it as a supported starting state.
	secondRaw, _ := invitation.GenerateToken()
	if err := p.AdminReset(ctx, userID, adminID, invitation.Digest(secondRaw), now.Add(2*time.Hour), now.Add(10*time.Minute)); err != nil {
		t.Fatalf("reissue AdminReset from password_change_required: %v", err)
	}

	u, _ = p.GetByID(ctx, userID)
	if u.Status != identity.StatePasswordChangeRequired || u.HasPassword() {
		t.Fatalf("expected password_change_required with no password after reissue, got %+v", u)
	}

	// The old (first) code must be invalidated by the reissue: it must never
	// redeem, even though it had not expired.
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(firstRaw), now.Add(15*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected the superseded first reset code to be rejected, got %v", err)
	}
	// The newest code must redeem successfully.
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(secondRaw), now.Add(15*time.Minute)); err != nil {
		t.Fatalf("expected the newest reset code to redeem, got %v", err)
	}
}

func TestLiveSessionLifecycleOperations(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A generous idle timeout for the revoke-one/revoke-all/Revoke
	// assertions below, which are not about idle expiration; idle
	// expiration itself is exercised separately (tightCfg, and the
	// dedicated unit tests in session_test.go).
	cfg := session.Config{IdleTimeout: 24 * time.Hour, AbsoluteLifetime: 24 * time.Hour}
	tightCfg := session.Config{IdleTimeout: time.Hour, AbsoluteLifetime: 24 * time.Hour}

	rawInv, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Sessions", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawInv), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	// A session cannot be created, and none can be resolved, while the
	// account is still password_change_required (Section 1, Section 8).
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawInv), now); err != nil {
		t.Fatal(err)
	}
	rawPending, _ := session.GenerateToken()
	if _, err := p.CreateSession(ctx, userID, session.Digest(rawPending), "", now, now.Add(time.Hour)); err != ErrAccountNotActive {
		t.Fatalf("expected ErrAccountNotActive for a password_change_required account, got %v", err)
	}

	if _, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-6"), now); err != nil {
		t.Fatal(err)
	}

	rawA, _ := session.GenerateToken()
	rawB, _ := session.GenerateToken()
	idA, err := p.CreateSession(ctx, userID, session.Digest(rawA), "device-a", now, now.Add(200*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateSession(ctx, userID, session.Digest(rawB), "device-b", now, now.Add(200*time.Minute)); err != nil {
		t.Fatal(err)
	}

	active, err := p.ListActiveSessions(ctx, userID)
	if err != nil || len(active) != 2 {
		t.Fatalf("expected 2 active sessions, got %d err=%v", len(active), err)
	}

	if err := p.TouchSession(ctx, idA, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawA), tightCfg, now.Add(89*time.Minute)); err != nil {
		t.Fatalf("expected touched session to still be usable just under its idle window: %v", err)
	}
	// Session B was never touched: under the same tight idle config and
	// elapsed time, it must already be idle-expired, proving TouchSession
	// is what kept session A alive above.
	if _, err := p.ResolveSession(ctx, session.Digest(rawB), tightCfg, now.Add(89*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected untouched session B to be idle-expired under the tight config, got %v", err)
	}

	// Revoke-one: session A only.
	if err := p.RevokeSession(ctx, idA, session.RevokedLogout, now.Add(90*time.Minute)); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawA), cfg, now.Add(91*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected revoked session A to be unresolvable, got %v", err)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawB), cfg, now.Add(91*time.Minute)); err != nil {
		t.Fatalf("expected session B to remain usable after revoking only session A, got %v", err)
	}
	// Revoking an already-revoked session is an idempotent no-op success.
	if err := p.RevokeSession(ctx, idA, session.RevokedLogout, now.Add(92*time.Minute)); err != nil {
		t.Fatalf("expected idempotent re-revocation to succeed, got %v", err)
	}

	// Revoke-all-for-user: session B goes away too.
	count, err := p.RevokeAllSessionsForUser(ctx, userID, session.RevokedLogoutAll, now.Add(93*time.Minute))
	if err != nil || count != 1 {
		t.Fatalf("RevokeAllSessionsForUser: count=%d err=%v", count, err)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawB), cfg, now.Add(94*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected session B to be revoked by revoke-all, got %v", err)
	}

	// Revoke (Section 7's narrower "kill current access now" action) on a
	// fresh session, without changing the account's enabled/disabled state.
	rawC, _ := session.GenerateToken()
	if _, err := p.CreateSession(ctx, userID, session.Digest(rawC), "device-c", now.Add(95*time.Minute), now.Add(200*time.Minute)); err != nil {
		t.Fatal(err)
	}
	revokedCount, err := p.Revoke(ctx, userID, now.Add(96*time.Minute))
	if err != nil || revokedCount != 1 {
		t.Fatalf("Revoke: count=%d err=%v", revokedCount, err)
	}
	u, _ := p.GetByID(ctx, userID)
	if u.Status != identity.StateActive {
		t.Fatalf("expected Revoke to leave account status unchanged (active), got %s", u.Status)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawC), cfg, now.Add(97*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected Revoke to invalidate the session, got %v", err)
	}
}

func TestLiveDuplicateEmailConflict(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	email := uniqueEmail(t)

	tok1, _ := invitation.GenerateToken()
	if _, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: email, DisplayName: "First", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(tok1), InvitationExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	tok2, _ := invitation.GenerateToken()
	if _, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: email, DisplayName: "Second", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(tok2), InvitationExpiresAt: now.Add(time.Hour),
	}); err != ErrConflict {
		t.Fatalf("expected ErrConflict for a duplicate normalized email, got %v", err)
	}
}

// TestLiveConcurrentInvitationRedemption exercises genuine cross-connection
// locking: two independently committing transactions race the same
// invitation token digest. See the package-level comment for why this test
// cannot use the shared rollback-only pattern above.
func TestLiveConcurrentInvitationRedemption(t *testing.T) {
	pool := liveTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := &Postgres{DB: pool}

	setupTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminID := insertBootstrapAdmin(ctx, t, setupTx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rawToken, _ := invitation.GenerateToken()
	txp := &Postgres{DB: setupTx}
	userID, err := txp.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Concurrent", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawToken), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cctx, `DELETE FROM sessions WHERE user_id = $1`, int64(userID))
		_, _ = pool.Exec(cctx, `DELETE FROM invitations WHERE user_id = $1`, int64(userID))
		_, _ = pool.Exec(cctx, `DELETE FROM mfa_credentials WHERE user_id = $1`, int64(userID))
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, int64(userID))
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, int64(adminID))
	})

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = p.RedeemInvitation(ctx, invitation.Digest(rawToken), now)
		}(i)
	}
	wg.Wait()

	successes, invalids := 0, 0
	for _, err := range results {
		switch err {
		case nil:
			successes++
		case ErrTokenInvalid:
			invalids++
		default:
			t.Fatalf("unexpected error from concurrent redemption: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful redemption out of %d concurrent attempts, got %d", attempts, successes)
	}
	if invalids != attempts-1 {
		t.Fatalf("expected every other concurrent attempt to fail as invalid, got %d", invalids)
	}
}

// TestLiveListUsers exercises ListUsers (Step 9E) against a real database:
// scope filtering, role filtering, status filtering, bounded search, and
// limit/offset pagination all round-trip correctly through actual SQL,
// which the fake stores used by adminservice's/authhttp's own unit tests
// cannot verify.
func TestLiveListUsers(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	makeUser := func(displayName string, role identity.Role, scope identity.Scope, status identity.AccountState) identity.UserID {
		tok, _ := invitation.GenerateToken()
		id, err := p.CreateUserAndInvite(ctx, NewUserParams{
			Email: uniqueEmail(t), DisplayName: displayName, Role: role, Scope: scope, CreatedBy: adminID,
			InvitationTokenDigest: invitation.Digest(tok), InvitationExpiresAt: now.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("fixture creation failed: %v", err)
		}
		if status == identity.StateActive || status == identity.StateSuspended {
			// Both require a permanent password on this fixture path: active
			// per the users_active_requires_password CHECK constraint, and
			// suspended here only because these fixtures represent an
			// administrator suspending a previously-active account, not a
			// still-invited one.
			if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $1, password_updated_at = $2 WHERE id = $3`,
				"$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g", now, int64(id)); err != nil {
				t.Fatalf("fixture password update failed: %v", err)
			}
		}
		if status != identity.StateInvited {
			if _, err := tx.Exec(ctx, `UPDATE users SET status = $1 WHERE id = $2`, string(status), int64(id)); err != nil {
				t.Fatalf("fixture status update failed: %v", err)
			}
		}
		return id
	}

	engine1 := identity.Scope("live-list-engine-1")
	engine2 := identity.Scope("live-list-engine-2")
	makeUser("Live List Alpha", identity.RoleResponder, engine1, identity.StateActive)
	makeUser("Live List Bravo", identity.RoleResponder, engine2, identity.StateActive)
	c := makeUser("Live List Charlie Searchable", identity.RoleDispatcherOperator, engine1, identity.StateSuspended)

	// Scope filtering.
	scoped, err := p.ListUsers(ctx, ListUsersFilter{Scope: &engine1})
	if err != nil {
		t.Fatalf("ListUsers by scope failed: %v", err)
	}
	if len(scoped) != 2 {
		t.Fatalf("expected 2 users in %s, got %d", engine1, len(scoped))
	}

	// Role filtering combined with scope.
	role := identity.RoleDispatcherOperator
	byRole, err := p.ListUsers(ctx, ListUsersFilter{Scope: &engine1, Role: &role})
	if err != nil {
		t.Fatalf("ListUsers by role failed: %v", err)
	}
	if len(byRole) != 1 || byRole[0].ID != c {
		t.Fatalf("expected exactly Charlie for role filter, got %+v", byRole)
	}

	// Status filtering.
	status := identity.StateSuspended
	byStatus, err := p.ListUsers(ctx, ListUsersFilter{Status: &status})
	if err != nil {
		t.Fatalf("ListUsers by status failed: %v", err)
	}
	for _, u := range byStatus {
		if u.Status != identity.StateSuspended {
			t.Fatalf("expected only suspended accounts, got %+v", u)
		}
	}

	// Bounded, LIKE-escaped search on display name.
	bySearch, err := p.ListUsers(ctx, ListUsersFilter{Search: "Searchable"})
	if err != nil {
		t.Fatalf("ListUsers by search failed: %v", err)
	}
	if len(bySearch) != 1 || bySearch[0].ID != c {
		t.Fatalf("expected exactly Charlie for search, got %+v", bySearch)
	}

	// A search string containing raw LIKE metacharacters must not change
	// matching behavior (escaped, not interpreted as a wildcard).
	literalPercent, err := p.ListUsers(ctx, ListUsersFilter{Search: "%"})
	if err != nil {
		t.Fatalf("ListUsers with a literal '%%' search failed: %v", err)
	}
	if len(literalPercent) != 0 {
		t.Fatalf("expected a literal '%%' search to match nothing, got %+v", literalPercent)
	}

	// Pagination: limit 1, offset 1 within engine1 returns the second row
	// in id order.
	page, err := p.ListUsers(ctx, ListUsersFilter{Scope: &engine1, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("ListUsers pagination failed: %v", err)
	}
	if len(page) != 1 || page[0].ID != c {
		t.Fatalf("expected the second engine1 row (Charlie) at offset 1, got %+v", page)
	}

	// An out-of-bound limit is silently clamped, not rejected.
	clamped, err := p.ListUsers(ctx, ListUsersFilter{Scope: &engine1, Limit: 100000})
	if err != nil {
		t.Fatalf("ListUsers with an oversized limit failed: %v", err)
	}
	if len(clamped) != 2 {
		t.Fatalf("expected the oversized limit to be clamped to still return both engine1 rows, got %d", len(clamped))
	}
}

// TestLiveMFAEnrollmentCreatesCredentialAndActivates exercises Step 9F's
// enrollment plumbing end to end: EnrollMFACredential persists exactly one
// non-revoked passkey credential (round-trippable through the mfa
// package's Marshal/UnmarshalCredential), and — because this administrator
// already has a permanent password and MFA was the only remaining Section
// 5 requirement — the same call activates the account, mirroring
// TestLiveAdminMFAGateBlocksActivation's "before enrollment" half from the
// other direction.
func TestLiveMFAEnrollmentCreatesCredentialAndActivates(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rawToken, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Passkey Admin", Role: identity.RoleSystemAdministrator,
		CreatedBy:             adminID,
		InvitationTokenDigest: invitation.Digest(rawToken), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawToken), now); err != nil {
		t.Fatal(err)
	}
	status, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-mfa-1"), now)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.StatePasswordChangeRequired {
		t.Fatalf("expected password_change_required pending MFA, got %s", status)
	}

	credData, err := mfa.MarshalCredential(&webauthn.Credential{
		ID:        []byte("synthetic-credential-id-1"),
		PublicKey: []byte("synthetic-public-key-bytes-1"),
	})
	if err != nil {
		t.Fatalf("MarshalCredential: %v", err)
	}
	status, err = p.EnrollMFACredential(ctx, userID, mfa.CredentialTypePasskey, credData, "Synthetic Passkey")
	if err != nil {
		t.Fatalf("EnrollMFACredential: %v", err)
	}
	if status != identity.StateActive {
		t.Fatalf("expected activation once MFA is enrolled, got %s", status)
	}
	u, _ := p.GetByID(ctx, userID)
	if u.Status != identity.StateActive {
		t.Fatalf("expected the persisted account status to be active, got %s", u.Status)
	}

	creds, err := p.ListMFACredentials(ctx, userID)
	if err != nil {
		t.Fatalf("ListMFACredentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected exactly one non-revoked credential, got %d", len(creds))
	}
	if creds[0].CredentialType != mfa.CredentialTypePasskey {
		t.Fatalf("expected credential_type passkey, got %s", creds[0].CredentialType)
	}
	if creds[0].Label != "Synthetic Passkey" {
		t.Fatalf("expected the enrollment label to round-trip, got %q", creds[0].Label)
	}
	decoded, err := mfa.UnmarshalCredential(creds[0].CredentialData)
	if err != nil {
		t.Fatalf("UnmarshalCredential: %v", err)
	}
	if string(decoded.ID) != "synthetic-credential-id-1" {
		t.Fatalf("expected round-tripped credential ID, got %q", decoded.ID)
	}
}

// TestLiveMFAEnrollmentDuplicateAndInvalidFailSafely covers Step 9F's
// "duplicate/invalid enrollment fails safely": re-enrolling the identical
// credential is rejected as a conflict, an unsupported credential type and
// empty credential data are rejected as invalid input, and none of these
// rejected attempts leaves behind a spurious credential row.
func TestLiveMFAEnrollmentDuplicateAndInvalidFailSafely(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rawToken, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic Duplicate Admin", Role: identity.RoleDepartmentAdministrator,
		CreatedBy:             adminID,
		InvitationTokenDigest: invitation.Digest(rawToken), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawToken), now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-mfa-2"), now); err != nil {
		t.Fatal(err)
	}

	credData, err := mfa.MarshalCredential(&webauthn.Credential{
		ID:        []byte("synthetic-credential-id-2"),
		PublicKey: []byte("synthetic-public-key-bytes-2"),
	})
	if err != nil {
		t.Fatalf("MarshalCredential: %v", err)
	}
	if _, err := p.EnrollMFACredential(ctx, userID, mfa.CredentialTypePasskey, credData, ""); err != nil {
		t.Fatalf("first enrollment: %v", err)
	}

	// The identical credential a second time is a conflict, not a silent
	// success and not a second row.
	if _, err := p.EnrollMFACredential(ctx, userID, mfa.CredentialTypePasskey, credData, ""); err != ErrConflict {
		t.Fatalf("expected ErrConflict enrolling the same credential twice, got %v", err)
	}
	// TOTP is a separately authorized, not-yet-implemented credential type;
	// this package must not accept it as a side door.
	if _, err := p.EnrollMFACredential(ctx, userID, "totp", []byte("irrelevant"), ""); err != ErrInput {
		t.Fatalf("expected ErrInput for an unsupported credential type, got %v", err)
	}
	// Empty credential data is always invalid input.
	if _, err := p.EnrollMFACredential(ctx, userID, mfa.CredentialTypePasskey, nil, ""); err != ErrInput {
		t.Fatalf("expected ErrInput for empty credential data, got %v", err)
	}

	creds, err := p.ListMFACredentials(ctx, userID)
	if err != nil {
		t.Fatalf("ListMFACredentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("expected exactly one surviving credential after the rejected attempts, got %d", len(creds))
	}
}

// TestLiveSessionMFAVerification exercises Step 9F-4 against a real
// database (requires migration 000009_mfa_session_verification.sql to
// already be applied): a freshly created session resolves as
// MFA-unverified by default, MarkSessionMFAVerified upgrades exactly that
// session and no other session for the same user, and a subsequently
// revoked session can never resolve at all — verification never survives,
// let alone "expires separately from," the session's own revocation.
func TestLiveSessionMFAVerification(t *testing.T) {
	pool := liveTestPool(t)
	ctx, tx, p := beginRollback(t, pool)
	adminID := insertBootstrapAdmin(ctx, t, tx)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rawInv, _ := invitation.GenerateToken()
	userID, err := p.CreateUserAndInvite(ctx, NewUserParams{
		Email: uniqueEmail(t), DisplayName: "Synthetic MFA Session", Role: identity.RoleResponder, CreatedBy: adminID,
		InvitationTokenDigest: invitation.Digest(rawInv), InvitationExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RedeemInvitation(ctx, invitation.Digest(rawInv), now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EstablishPassword(ctx, userID, hashFor(t, "synthetic-test-password-mfasession"), now); err != nil {
		t.Fatal(err)
	}

	cfg := session.Config{IdleTimeout: time.Hour, AbsoluteLifetime: 24 * time.Hour}

	rawA, _ := session.GenerateToken()
	idA, err := p.CreateSession(ctx, userID, session.Digest(rawA), "device-a", now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	rawB, _ := session.GenerateToken()
	if _, err := p.CreateSession(ctx, userID, session.Digest(rawB), "device-b", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// 1. A newly created session defaults to MFA-unverified.
	authA, err := p.ResolveSession(ctx, session.Digest(rawA), cfg, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ResolveSession A: %v", err)
	}
	if authA.Session.MFAVerified() {
		t.Fatal("expected a freshly created session to default to MFA-unverified")
	}

	// 2. Marking session A verified affects only session A.
	if err := p.MarkSessionMFAVerified(ctx, idA, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkSessionMFAVerified: %v", err)
	}
	authA, err = p.ResolveSession(ctx, session.Digest(rawA), cfg, now.Add(3*time.Minute))
	if err != nil || !authA.Session.MFAVerified() {
		t.Fatalf("expected session A to resolve MFA-verified, got %+v err=%v", authA, err)
	}
	authB, err := p.ResolveSession(ctx, session.Digest(rawB), cfg, now.Add(3*time.Minute))
	if err != nil || authB.Session.MFAVerified() {
		t.Fatalf("expected session B to remain MFA-unverified, got %+v err=%v", authB, err)
	}

	// 3. Revoking the verified session removes it from resolution
	// entirely: verification never outlives, or is checked independently
	// of, the session's own lifecycle.
	if err := p.RevokeSession(ctx, idA, session.RevokedLogout, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawA), cfg, now.Add(5*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected the revoked, previously-verified session to be unresolvable, got %v", err)
	}

	// 4. Marking an already-revoked session verified fails closed (9F-7K):
	// zero rows are affected, so this is ErrNotFound, never a silent
	// success, and it does not resurrect the session.
	if err := p.MarkSessionMFAVerified(ctx, idA, now.Add(6*time.Minute)); err != ErrNotFound {
		t.Fatalf("expected MarkSessionMFAVerified on a revoked session to fail with ErrNotFound, got %v", err)
	}
	if _, err := p.ResolveSession(ctx, session.Digest(rawA), cfg, now.Add(7*time.Minute)); err != ErrTokenInvalid {
		t.Fatalf("expected the revoked session to remain unresolvable, got %v", err)
	}

	// 5. An unrevoked but absolutely-expired session also fails closed,
	// never marked verified past its own lifetime ceiling.
	rawC, _ := session.GenerateToken()
	idC, err := p.CreateSession(ctx, userID, session.Digest(rawC), "device-c", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.MarkSessionMFAVerified(ctx, idC, now.Add(time.Hour)); err != ErrNotFound {
		t.Fatalf("expected MarkSessionMFAVerified on an expired session to fail with ErrNotFound, got %v", err)
	}

	// 6. An unknown session id fails identically, never a silent success.
	if err := p.MarkSessionMFAVerified(ctx, idC+9999, now.Add(8*time.Minute)); err != ErrNotFound {
		t.Fatalf("expected MarkSessionMFAVerified on an unknown session id to fail with ErrNotFound, got %v", err)
	}
}

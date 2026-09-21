package identityservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/invitation"
	"greenwich-fire-responder/backend/internal/passwordreset"
)

func seedInvitedUser(t *testing.T, store *fakeStore, email string, role identity.Role, ttl time.Duration) (identity.UserID, string) {
	t.Helper()
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	id := store.seedUser(identity.User{NormalizedEmail: normalized, DisplayName: "Synthetic Invitee", Role: role, Status: identity.StateInvited})
	rawToken, err := invitation.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	store.seedInvitation(id, invitation.Digest(rawToken), fixedNow.Add(ttl))
	return id, rawToken
}

func TestRedeemInvitationAndSetPasswordSuccess(t *testing.T) {
	svc, store, audit := newTestService(t)
	_, rawToken := seedInvitedUser(t, store, "invitee@example.test", identity.RoleResponder, time.Hour)

	result, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawToken, "a-brand-new-passphrase", fixedNow)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.Status != identity.StateActive {
		t.Fatalf("expected active status for a non-administrator role, got %s", result.Status)
	}

	var sawRedeemed bool
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.InvitationRedeemed {
			sawRedeemed = true
			if ev.AccountID != result.UserID {
				t.Errorf("expected audit account id to match redeemed user")
			}
		}
	}
	if !sawRedeemed {
		t.Fatal("expected an invitation_redeemed audit event")
	}

	// The user can now log in normally.
	if _, err := svc.Login(context.Background(), "invitee@example.test", "a-brand-new-passphrase", "", "", fixedNow); err != nil {
		t.Fatalf("expected login to succeed after redemption, got %v", err)
	}
}

func TestRedeemInvitationAdministratorStaysPasswordChangeRequired(t *testing.T) {
	svc, store, _ := newTestService(t)
	_, rawToken := seedInvitedUser(t, store, "admin-invitee@example.test", identity.RoleSystemAdministrator, time.Hour)

	result, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawToken, "a-brand-new-passphrase", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != identity.StatePasswordChangeRequired {
		t.Fatalf("expected an administrator to remain password_change_required without MFA, got %s", result.Status)
	}
	if _, err := svc.Login(context.Background(), "admin-invitee@example.test", "a-brand-new-passphrase", "", "", fixedNow); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected login to still be blocked pending MFA, got %v", err)
	}
}

func TestRedeemInvitationRejectsReuse(t *testing.T) {
	svc, store, audit := newTestService(t)
	_, rawToken := seedInvitedUser(t, store, "reuse@example.test", identity.RoleResponder, time.Hour)

	if _, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawToken, "first-passphrase-value", fixedNow); err != nil {
		t.Fatal(err)
	}
	_, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawToken, "second-passphrase-value", fixedNow)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid on reuse, got %v", err)
	}

	var sawFailure bool
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.InvitationRedemptionFailed {
			sawFailure = true
			if ev.AccountID != 0 {
				t.Error("expected no resolvable account on a reused-token failure")
			}
		}
	}
	if !sawFailure {
		t.Fatal("expected an invitation_redemption_failed audit event, never a silent failure (AAX-06)")
	}
}

func TestRedeemInvitationRejectsExpired(t *testing.T) {
	svc, store, _ := newTestService(t)
	_, rawToken := seedInvitedUser(t, store, "expired@example.test", identity.RoleResponder, -time.Minute)

	_, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawToken, "some-passphrase-value", fixedNow)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestRedeemInvitationRejectsWeakPassword(t *testing.T) {
	svc, store, _ := newTestService(t)
	_, rawToken := seedInvitedUser(t, store, "weak@example.test", identity.RoleResponder, time.Hour)

	_, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawToken, "short", fixedNow)
	if !errors.Is(err, ErrPasswordTooWeak) {
		t.Fatalf("expected ErrPasswordTooWeak, got %v", err)
	}
}

func TestPasswordResetEnumerationResistance(t *testing.T) {
	svc, store, audit := newTestService(t)
	seedActiveUser(t, store, "hasaccount@example.test", identity.RoleResponder)

	_, matchedKnown, err := svc.RequestPasswordReset(context.Background(), "HasAccount@Example.test", fixedNow)
	if err != nil || !matchedKnown {
		t.Fatalf("expected match for a known account, err=%v matched=%v", err, matchedKnown)
	}
	rawTokenUnknown, matchedUnknown, err := svc.RequestPasswordReset(context.Background(), "no-such-account@example.test", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if matchedUnknown {
		t.Fatal("expected no match for an unknown account")
	}
	if rawTokenUnknown != "" {
		t.Fatal("expected no token for an unmatched request")
	}

	var sawRequested int
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.PasswordResetRequested {
			sawRequested++
		}
	}
	if sawRequested != 1 {
		t.Fatalf("expected exactly one password_reset_requested audit event (only for the match), got %d", sawRequested)
	}
}

func TestCompletePasswordResetRevokesExistingSessions(t *testing.T) {
	svc, store, audit := newTestService(t)
	userID, login := loginTestUser(t, svc, store, "resetme@example.test", identity.RoleResponder)

	rawToken, matched, err := svc.RequestPasswordReset(context.Background(), "resetme@example.test", fixedNow)
	if err != nil || !matched {
		t.Fatalf("expected a matched reset request, err=%v matched=%v", err, matched)
	}

	newUserID, err := svc.CompletePasswordReset(context.Background(), rawToken, "a-completely-new-passphrase", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if newUserID != userID {
		t.Fatalf("expected user id %d, got %d", userID, newUserID)
	}

	if _, err := svc.ResolveSession(context.Background(), login.RawToken, fixedNow); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("expected the pre-reset session to be revoked")
	}
	if _, err := svc.Login(context.Background(), "resetme@example.test", "a-completely-new-passphrase", "", "", fixedNow); err != nil {
		t.Fatalf("expected login with the new password to succeed, got %v", err)
	}

	var sawCompleted bool
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.PasswordResetCompleted {
			sawCompleted = true
		}
	}
	if !sawCompleted {
		t.Fatal("expected a password_reset_completed audit event")
	}
}

func TestCompletePasswordResetRejectsExpiredAndReusedTokens(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedActiveUser(t, store, "resettoken@example.test", identity.RoleResponder)

	rawToken, matched, err := svc.RequestPasswordReset(context.Background(), "resettoken@example.test", fixedNow)
	if err != nil || !matched {
		t.Fatal(err)
	}

	// Expired: the same store fixture, evaluated far enough in the future.
	if _, err := svc.CompletePasswordReset(context.Background(), rawToken, "a-new-passphrase-value", fixedNow.Add(2*time.Hour)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}

	// A fresh, valid token, redeemed once, then reused.
	rawToken2, matched2, err := svc.RequestPasswordReset(context.Background(), "resettoken@example.test", fixedNow)
	if err != nil || !matched2 {
		t.Fatal(err)
	}
	if _, err := svc.CompletePasswordReset(context.Background(), rawToken2, "a-new-passphrase-value", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompletePasswordReset(context.Background(), rawToken2, "yet-another-passphrase", fixedNow); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid on reuse, got %v", err)
	}
}

func TestCompletePasswordResetRejectsUnknownToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	unknown, err := passwordreset.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompletePasswordReset(context.Background(), unknown, "a-new-passphrase-value", fixedNow); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

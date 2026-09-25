package identityservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/mfa"
	"greenwich-fire-responder/backend/internal/session"
)

// fakeMFAProvider fakes the cryptographic WebAuthn ceremony so this
// package's own orchestration logic (eligibility, ceremony binding,
// persistence, audit, activation) can be tested without a real
// authenticator. A responseJSON exactly equal to validResponseMarker is
// treated as a successful ceremony; anything else is a verification
// failure — mirroring what a real Provider does for a well-formed vs.
// malformed/forged response. The underlying cryptography is
// backend/internal/mfa's own, separately tested responsibility.
type fakeMFAProvider struct {
	mu      sync.Mutex
	counter int
}

const validResponseMarker = "synthetic-valid-webauthn-response"

func (f *fakeMFAProvider) BeginEnrollment(user mfa.EnrollmentUser) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return &protocol.CredentialCreation{}, &webauthn.SessionData{Challenge: "synthetic-challenge", UserID: user.WebAuthnID()}, nil
}

func (f *fakeMFAProvider) FinishEnrollment(user mfa.EnrollmentUser, sessionData webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error) {
	if string(responseJSON) != validResponseMarker {
		return nil, errors.New("fake: malformed or unverifiable response")
	}
	f.mu.Lock()
	f.counter++
	id := f.counter
	f.mu.Unlock()
	return &webauthn.Credential{
		ID:        []byte(fmt.Sprintf("synthetic-credential-%d", id)),
		PublicKey: []byte(fmt.Sprintf("synthetic-public-key-%d", id)),
	}, nil
}

func (f *fakeMFAProvider) BeginLogin(user mfa.LoginUser) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	if len(user.Credentials) == 0 {
		return nil, nil, errors.New("fake: no credentials to verify possession of")
	}
	return &protocol.CredentialAssertion{}, &webauthn.SessionData{Challenge: "synthetic-login-challenge", UserID: user.WebAuthnID()}, nil
}

func (f *fakeMFAProvider) FinishLogin(user mfa.LoginUser, sessionData webauthn.SessionData, responseJSON []byte) (*webauthn.Credential, error) {
	if string(responseJSON) != validResponseMarker {
		return nil, errors.New("fake: malformed or unverifiable response")
	}
	if len(user.Credentials) == 0 {
		return nil, errors.New("fake: no credentials to verify possession of")
	}
	cred := user.Credentials[0]
	return &cred, nil
}

func newMFATestService(t *testing.T) (*Service, *fakeStore, *fakeAudit, *fakeMFAProvider) {
	t.Helper()
	svc, store, audit := newTestService(t)
	provider := &fakeMFAProvider{}
	svc.SetMFAProvider(provider)
	return svc, store, audit, provider
}

func TestBeginMFAEnrollmentRequiresProvider(t *testing.T) {
	svc, store, _ := newTestService(t)
	id := seedActiveUser(t, store, "noprovider@example.test", identity.RoleResponder)
	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "tok", fixedNow); !errors.Is(err, ErrMFAUnavailable) {
		t.Fatalf("expected ErrMFAUnavailable, got %v", err)
	}
}

func TestBeginMFAEnrollmentRejectsIneligibleAccountState(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "ineligible@example.test", identity.RoleResponder)
	store.mu.Lock()
	store.users[id].Status = identity.StateSuspended
	store.mu.Unlock()

	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "tok", fixedNow); !errors.Is(err, ErrMFANotEligible) {
		t.Fatalf("expected ErrMFANotEligible, got %v", err)
	}
}

// TestBeginMFAEnrollmentReplacesPriorCeremonyForSameBinding demonstrates
// the ceremony store's bound: exactly one in-flight ceremony is ever live
// per authenticated context, never accumulating across repeated Begin
// calls.
func TestBeginMFAEnrollmentReplacesPriorCeremonyForSameBinding(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "replace@example.test", identity.RoleResponder)

	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "same-token", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "same-token", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "same-token", []byte(validResponseMarker), fixedNow); err != nil {
		t.Fatalf("expected the latest ceremony to finish successfully: %v", err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "same-token", []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected the ceremony to already be consumed, got %v", err)
	}
}

func TestFinishMFAEnrollmentWithoutBeginFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "nobegin@example.test", identity.RoleResponder)
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "never-begun-token", []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid, got %v", err)
	}
}

func TestFinishMFAEnrollmentExpiredCeremonyFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "expiredcer@example.test", identity.RoleResponder)
	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "expiring-token", fixedNow); err != nil {
		t.Fatal(err)
	}
	later := fixedNow.Add(mfaCeremonyTTL + time.Minute)
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "expiring-token", []byte(validResponseMarker), later); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid for an expired ceremony, got %v", err)
	}
}

func TestFinishMFAEnrollmentDifferentBindingFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "diffbinding@example.test", identity.RoleResponder)
	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "session-a-token", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "session-b-token", []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid when finishing under a different authenticated context, got %v", err)
	}
}

func TestFinishMFAEnrollmentWrongUserFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	idA := seedActiveUser(t, store, "usera@example.test", identity.RoleResponder)
	idB := seedActiveUser(t, store, "userb@example.test", identity.RoleResponder)
	if _, err := svc.BeginMFAEnrollment(context.Background(), idA, "shared-token", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), idB, "shared-token", []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid for a mismatched user, got %v", err)
	}
}

func TestFinishMFAEnrollmentMalformedResponseFailsSafely(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "malformed@example.test", identity.RoleResponder)
	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "raw-token-1", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "raw-token-1", []byte("garbage"), fixedNow); !errors.Is(err, ErrMFAResponseInvalid) {
		t.Fatalf("expected ErrMFAResponseInvalid, got %v", err)
	}
	creds, _ := store.ListMFACredentials(context.Background(), id)
	if len(creds) != 0 {
		t.Fatal("expected no credential to be persisted for a malformed response")
	}
}

func TestFinishMFAEnrollmentReplayFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "replay@example.test", identity.RoleResponder)
	if _, err := svc.BeginMFAEnrollment(context.Background(), id, "raw-token-2", fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "raw-token-2", []byte(validResponseMarker), fixedNow); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, "raw-token-2", []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid on replay, got %v", err)
	}
}

// TestFinishMFAEnrollmentStoresOneCredentialAndActivatesAdministrator
// drives the full bridging flow: an invited administrator redeems their
// invitation and sets a password (remaining password_change_required,
// receiving a bridging credential), then completes MFA enrollment through
// it, which must store exactly one credential, record exactly one
// MFAEnrollment audit event with only safe metadata, activate the account,
// and revoke the now-fulfilled bridging credential.
func TestFinishMFAEnrollmentStoresOneCredentialAndActivatesAdministrator(t *testing.T) {
	svc, store, audit, _ := newMFATestService(t)
	_, rawInvite := seedInvitedUser(t, store, "pending-admin@example.test", identity.RoleSystemAdministrator, time.Hour)

	result, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawInvite, "a-brand-new-passphrase", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != identity.StatePasswordChangeRequired {
		t.Fatalf("expected password_change_required pending MFA, got %s", result.Status)
	}
	if result.MFAEnrollmentToken == "" {
		t.Fatal("expected an MFA enrollment bridging credential to be issued")
	}

	if _, err := svc.BeginMFAEnrollment(context.Background(), result.UserID, result.MFAEnrollmentToken, fixedNow); err != nil {
		t.Fatalf("BeginMFAEnrollment: %v", err)
	}
	newStatus, err := svc.FinishMFAEnrollment(context.Background(), result.UserID, result.MFAEnrollmentToken, []byte(validResponseMarker), fixedNow)
	if err != nil {
		t.Fatalf("FinishMFAEnrollment: %v", err)
	}
	if newStatus != identity.StateActive {
		t.Fatalf("expected activation once MFA is enrolled, got %s", newStatus)
	}

	creds, err := store.ListMFACredentials(context.Background(), result.UserID)
	if err != nil || len(creds) != 1 {
		t.Fatalf("expected exactly one credential, got %d err=%v", len(creds), err)
	}

	var mfaEvents int
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.MFAEnrollment {
			mfaEvents++
			if len(ev.Metadata) != 1 || ev.Metadata["method"] != "passkey" {
				t.Fatalf("expected exactly {method: passkey} metadata, got %+v", ev.Metadata)
			}
		}
	}
	if mfaEvents != 1 {
		t.Fatalf("expected exactly one MFAEnrollment audit event, got %d", mfaEvents)
	}

	if _, err := svc.ResolveMFAEnrollmentCredential(context.Background(), result.MFAEnrollmentToken, fixedNow); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("expected the bridging credential to be revoked after successful enrollment")
	}
}

// TestFinishMFAEnrollmentAuditMetadataNeverLeaksCeremonySecrets scans every
// audit event recorded across a full bridging enrollment flow for any of
// the ceremony's own secret-shaped values, confirming none of them ever
// appear — identityaudit's own allow-list already structurally prevents
// this (see backend/internal/mfa's TestEnrollmentAuditMetadataIsSafe), but
// this test additionally confirms no value coincidentally leaks through an
// allowed key at this orchestration layer.
func TestFinishMFAEnrollmentAuditMetadataNeverLeaksCeremonySecrets(t *testing.T) {
	svc, store, audit, _ := newMFATestService(t)
	_, rawInvite := seedInvitedUser(t, store, "no-leak-admin@example.test", identity.RoleDepartmentAdministrator, time.Hour)
	result, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawInvite, "another-new-passphrase", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BeginMFAEnrollment(context.Background(), result.UserID, result.MFAEnrollmentToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), result.UserID, result.MFAEnrollmentToken, []byte(validResponseMarker), fixedNow); err != nil {
		t.Fatal(err)
	}

	secrets := []string{validResponseMarker, "synthetic-challenge", result.MFAEnrollmentToken}
	for _, ev := range audit.snapshot() {
		for key, value := range ev.Metadata {
			lowerKey := strings.ToLower(key)
			for _, bad := range []string{"password", "token", "secret", "cookie", "hash", "credential", "challenge", "attestation"} {
				if strings.Contains(lowerKey, bad) {
					t.Errorf("event %s has a forbidden-shaped metadata key %q", ev.Type, key)
				}
			}
			for _, secret := range secrets {
				if secret != "" && strings.Contains(value, secret) {
					t.Errorf("event %s metadata key %q leaked a ceremony secret", ev.Type, key)
				}
			}
		}
	}
}

// TestRedeemInvitationIssuesMFAEnrollmentCredentialForPendingAdmin confirms
// the bridging credential is issued exactly when needed: never for a
// non-administrator role (which reaches active immediately), and always
// for an administrator left pending MFA.
func TestRedeemInvitationIssuesMFAEnrollmentCredentialForPendingAdmin(t *testing.T) {
	svc, store, _ := newTestService(t)

	_, rawResponderToken := seedInvitedUser(t, store, "responder-noenroll@example.test", identity.RoleResponder, time.Hour)
	responderResult, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawResponderToken, "a-passphrase-value-1", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if responderResult.MFAEnrollmentToken != "" {
		t.Fatal("expected no bridging credential for a role that does not require MFA")
	}

	_, rawAdminToken := seedInvitedUser(t, store, "admin-needsenroll@example.test", identity.RoleDepartmentAdministrator, time.Hour)
	adminResult, err := svc.RedeemInvitationAndSetPassword(context.Background(), rawAdminToken, "a-passphrase-value-2", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if adminResult.MFAEnrollmentToken == "" {
		t.Fatal("expected a bridging credential for an administrator still pending MFA")
	}
	if !adminResult.MFAEnrollmentExpiresAt.After(fixedNow) {
		t.Fatal("expected the bridging credential's expiry to be in the future")
	}
}

// createRealSession creates a genuine session row for userID via the fake
// store (CreateSession requires the account to already be active,
// mirroring the real identitystore invariant) and returns both the
// session id and its raw token, for use as a BeginMFALogin/FinishMFALogin
// bindingRawToken.
func createRealSession(t *testing.T, store *fakeStore, userID identity.UserID) (session.ID, string) {
	t.Helper()
	rawToken, err := session.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.CreateSession(context.Background(), userID, session.Digest(rawToken), "", fixedNow, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return id, rawToken
}

func TestBeginMFALoginRequiresProvider(t *testing.T) {
	svc, store, _ := newTestService(t)
	id := seedActiveUser(t, store, "login-noprovider@example.test", identity.RoleResponder)
	_, rawToken := createRealSession(t, store, id)
	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); !errors.Is(err, ErrMFAUnavailable) {
		t.Fatalf("expected ErrMFAUnavailable, got %v", err)
	}
}

func TestBeginMFALoginRejectsNonActiveAccount(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-inactive@example.test", identity.RoleResponder)
	_, rawToken := createRealSession(t, store, id)
	store.mu.Lock()
	store.users[id].Status = identity.StateSuspended
	store.mu.Unlock()

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); !errors.Is(err, ErrMFANotEligible) {
		t.Fatalf("expected ErrMFANotEligible, got %v", err)
	}
}

func TestBeginMFALoginRejectsNoEnrolledCredential(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-nocred@example.test", identity.RoleResponder)
	_, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); !errors.Is(err, ErrMFANotEligible) {
		t.Fatalf("expected ErrMFANotEligible with no enrolled credential, got %v", err)
	}
}

// enrollSyntheticCredentialDirect inserts a non-revoked credential for
// userID directly through the fake store, bypassing the enrollment
// ceremony (already covered by the TestFinishMFAEnrollment* tests above):
// these login-ceremony tests only need SOME enrolled credential to exist.
func enrollSyntheticCredentialDirect(t *testing.T, store *fakeStore, userID identity.UserID) {
	t.Helper()
	data, err := mfa.MarshalCredential(&webauthn.Credential{ID: []byte("direct-credential-id"), PublicKey: []byte("direct-public-key")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnrollMFACredential(context.Background(), userID, mfa.CredentialTypePasskey, data, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFinishMFALoginWithoutBeginFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-nobegin@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionID, rawToken := createRealSession(t, store, id)

	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid, got %v", err)
	}
}

func TestFinishMFALoginExpiredCeremonyFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-expired@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionID, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	later := fixedNow.Add(mfaCeremonyTTL + time.Minute)
	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte(validResponseMarker), later); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid for an expired ceremony, got %v", err)
	}
}

func TestFinishMFALoginDifferentBindingFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-diffbinding@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionA, rawTokenA := createRealSession(t, store, id)
	_, rawTokenB := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawTokenA, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishMFALogin(context.Background(), id, sessionA, rawTokenB, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid when finishing under a different session's token, got %v", err)
	}
}

func TestFinishMFALoginWrongUserFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	idA := seedActiveUser(t, store, "login-usera@example.test", identity.RoleResponder)
	idB := seedActiveUser(t, store, "login-userb@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, idA)
	enrollSyntheticCredentialDirect(t, store, idB)
	sessionB, rawToken := createRealSession(t, store, idB)

	if _, err := svc.BeginMFALogin(context.Background(), idA, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishMFALogin(context.Background(), idB, sessionB, rawToken, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid for a mismatched user, got %v", err)
	}
}

func TestFinishMFALoginMalformedResponseFailsSafely(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-malformed@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionID, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte("garbage"), fixedNow); !errors.Is(err, ErrMFAResponseInvalid) {
		t.Fatalf("expected ErrMFAResponseInvalid, got %v", err)
	}
}

func TestFinishMFALoginReplayFails(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-replay@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionID, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte(validResponseMarker), fixedNow); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected ErrMFACeremonyInvalid on replay, got %v", err)
	}
}

// TestFinishMFALoginMarksOnlyThatSessionVerified confirms Step 9F-4's
// central invariant directly at this layer: verification is a per-session
// fact recorded via MarkSessionMFAVerified, and a second, independent
// session for the identical user is left untouched.
func TestFinishMFALoginMarksOnlyThatSessionVerified(t *testing.T) {
	svc, store, audit, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-marks@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionA, rawTokenA := createRealSession(t, store, id)
	sessionB, _ := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawTokenA, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishMFALogin(context.Background(), id, sessionA, rawTokenA, []byte(validResponseMarker), fixedNow); err != nil {
		t.Fatalf("FinishMFALogin: %v", err)
	}

	store.mu.Lock()
	verifiedA := store.sessions[sessionA].MFAVerified()
	verifiedB := store.sessions[sessionB].MFAVerified()
	store.mu.Unlock()
	if !verifiedA {
		t.Fatal("expected session A to be marked MFA-verified")
	}
	if verifiedB {
		t.Fatal("expected session B, belonging to the same user, to remain unverified")
	}

	var verifyEvents int
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.MFAVerification {
			verifyEvents++
			if len(ev.Metadata) != 1 || ev.Metadata["method"] != "passkey" {
				t.Fatalf("expected exactly {method: passkey} metadata, got %+v", ev.Metadata)
			}
		}
	}
	if verifyEvents != 1 {
		t.Fatalf("expected exactly one MFAVerification audit event, got %d", verifyEvents)
	}
}

// TestFinishMFALoginFailsClosedWhenSessionRevokedConcurrently is the 9F-7K
// regression test: MarkSessionMFAVerified must verify exactly one
// valid/non-expired session was updated, not merely that the SQL statement
// executed without error. If sessionID is revoked between BeginMFALogin and
// FinishMFALogin — a real race an administrator's concurrent
// revoke-sessions action could trigger — FinishMFALogin must fail with
// ErrSessionInvalid, must never mark the session verified, and must never
// record an MFAVerification audit event: a correct WebAuthn response is not
// by itself enough to report success once the session it was bound to is no
// longer valid.
func TestFinishMFALoginFailsClosedWhenSessionRevokedConcurrently(t *testing.T) {
	svc, store, audit, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-revoked-race@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionID, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeSession(context.Background(), sessionID, session.RevokedAdminRevoke, fixedNow); err != nil {
		t.Fatal(err)
	}

	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected ErrSessionInvalid for a session revoked mid-ceremony, got %v", err)
	}

	store.mu.Lock()
	verified := store.sessions[sessionID].MFAVerified()
	store.mu.Unlock()
	if verified {
		t.Fatal("expected the revoked session to remain unverified")
	}
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.MFAVerification {
			t.Fatalf("expected no MFAVerification audit event when verification failed, got %+v", ev)
		}
	}
}

// TestFinishMFALoginFailsClosedWhenSessionExpired mirrors the revoked-race
// test for the other half of MarkSessionMFAVerified's new WHERE clause:
// absolute expiry. The session's ExpiresAt is moved into the past directly
// (rather than advancing FinishMFALogin's own `now`) so the still-short-lived
// in-memory WebAuthn ceremony itself does not expire first and mask the
// session-expiry check this test targets.
func TestFinishMFALoginFailsClosedWhenSessionExpired(t *testing.T) {
	svc, store, audit, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-expired-race@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionID, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.sessions[sessionID].ExpiresAt = fixedNow.Add(-time.Minute)
	store.mu.Unlock()

	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte(validResponseMarker), fixedNow.Add(time.Minute)); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected ErrSessionInvalid for an expired session, got %v", err)
	}

	store.mu.Lock()
	verified := store.sessions[sessionID].MFAVerified()
	store.mu.Unlock()
	if verified {
		t.Fatal("expected the expired session to remain unverified")
	}
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.MFAVerification {
			t.Fatalf("expected no MFAVerification audit event when verification failed, got %+v", ev)
		}
	}
}

// TestFinishMFALoginFailsClosedForUnknownSessionID confirms the same
// fail-closed contract for a session id that never existed at all — the
// third case MarkSessionMFAVerified's affected-row check must catch,
// alongside revoked and expired.
func TestFinishMFALoginFailsClosedForUnknownSessionID(t *testing.T) {
	svc, store, audit, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "login-unknown-session@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	_, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}

	const bogusSessionID session.ID = 999999
	if err := svc.FinishMFALogin(context.Background(), id, bogusSessionID, rawToken, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected ErrSessionInvalid for an unknown session id, got %v", err)
	}
	for _, ev := range audit.snapshot() {
		if ev.Type == identityaudit.MFAVerification {
			t.Fatalf("expected no MFAVerification audit event when the target session does not exist, got %+v", ev)
		}
	}
}

// TestMFACeremoniesDoNotCrossContaminateAcrossPurposes confirms an
// enrollment ceremony begun under one authenticated context can never be
// finished as a login-verification ceremony, and vice versa, even when
// both happen to share the identical binding token (an already-active
// user could in principle call both endpoints from the same session).
func TestMFACeremoniesDoNotCrossContaminateAcrossPurposes(t *testing.T) {
	svc, store, _, _ := newMFATestService(t)
	id := seedActiveUser(t, store, "cross-contaminate@example.test", identity.RoleResponder)
	enrollSyntheticCredentialDirect(t, store, id)
	sessionID, rawToken := createRealSession(t, store, id)

	if _, err := svc.BeginMFAEnrollment(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishMFALogin(context.Background(), id, sessionID, rawToken, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected an enrollment ceremony to be rejected by FinishMFALogin, got %v", err)
	}

	if _, err := svc.BeginMFALogin(context.Background(), id, rawToken, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FinishMFAEnrollment(context.Background(), id, rawToken, []byte(validResponseMarker), fixedNow); !errors.Is(err, ErrMFACeremonyInvalid) {
		t.Fatalf("expected a login ceremony to be rejected by FinishMFAEnrollment, got %v", err)
	}
}

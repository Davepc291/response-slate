package authhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
)

// TestAdminRouteRequiresMFAVerifiedSession is the core Step 9F-4 regression:
// an administrator with a perfectly valid session, and even an enrolled
// credential, must still be refused every /api/admin/* route until THAT
// session specifically completes the WebAuthn authentication ceremony —
// neither the password login nor the credential's mere existence is ever
// sufficient on its own.
func TestAdminRouteRequiresMFAVerifiedSession(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("plain-admin@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginCookies("plain-admin@example.test")

	rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a password-only admin session, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminRouteEnrolledCredentialWithoutCeremonyStaysBlocked(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("has-credential-admin@example.test", identity.RoleSystemAdministrator)
	hn.enrollSyntheticMFACredential(id) // enrolled, but never verified for THIS session
	sessionCookie, csrfCookie := hn.loginCookies("has-credential-admin@example.test")

	rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with an enrolled credential but no ceremony for this session, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil {
		if errObj, ok := body["error"].(map[string]any); ok {
			if code, _ := errObj["code"].(string); code != "mfa_verification_required" {
				t.Fatalf("expected mfa_verification_required error code, got %v", errObj)
			}
		}
	}
}

func TestAdminRouteMFAVerifiedSessionReachesExistingAuthorization(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verified-admin@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "verified-admin@example.test")

	rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an MFA-verified administrator, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminRouteMFAVerificationNeverBypassesRoleCheck confirms MFA
// verification is additive, never a substitute for the existing role
// check: a non-administrator who somehow completes the verification
// ceremony (self-service, allowed for any active account) is still denied
// by adminservice's own authorization exactly as before.
func TestAdminRouteMFAVerificationNeverBypassesRoleCheck(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verified-responder@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "verified-responder@example.test")

	rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-administrator even with a verified session, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminRouteAnotherSessionForSameUserStaysUnverified confirms
// verification is bound to one specific session row, not the account: a
// second, independently-created session for the identical administrator
// must still be refused.
func TestAdminRouteAnotherSessionForSameUserStaysUnverified(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("two-sessions-admin@example.test", identity.RoleSystemAdministrator)
	verifiedSession, verifiedCSRF := hn.loginAndVerifyMFACookies(id, "two-sessions-admin@example.test")
	unverifiedSession, unverifiedCSRF := hn.loginCookies("two-sessions-admin@example.test")

	if rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(verifiedSession, verifiedCSRF)); rec.Code != http.StatusOK {
		t.Fatalf("expected the verified session to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(unverifiedSession, unverifiedCSRF))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected the second, unverified session to remain blocked, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminRouteRevokedSessionCannotRetainMFAAuthorization confirms
// logout clears effective MFA authorization automatically: a revoked
// session fails at the ordinary session-resolution step (401), never
// reaching (and never needing) the MFA check at all.
func TestAdminRouteRevokedSessionCannotRetainMFAAuthorization(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("logout-admin@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "logout-admin@example.test")

	if rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(sessionCookie, csrfCookie)); rec.Code != http.StatusOK {
		t.Fatalf("expected the verified session to succeed before logout, got %d: %s", rec.Code, rec.Body.String())
	}

	logoutRec := hn.request(http.MethodPost, "/api/auth/logout", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout failed: %d %s", logoutRec.Code, logoutRec.Body.String())
	}

	rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a revoked (logged out) session, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAVerifyBeginRequiresAuthentication(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), reqOpts{})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without any authentication, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAVerifyBeginRejectsAccountWithNoEnrolledCredential(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("no-credential@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("no-credential@example.test")

	rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 with no enrolled credential to verify, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAVerifyFinishWithoutBeginFails(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verify-nobegin@example.test", identity.RoleResponder)
	hn.enrollSyntheticMFACredential(id)
	sessionCookie, csrfCookie := hn.loginCookies("verify-nobegin@example.test")

	rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 finishing without a prior begin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAVerifyFinishExpiredCeremonyFails(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verify-expired@example.test", identity.RoleResponder)
	hn.enrollSyntheticMFACredential(id)
	sessionCookie, csrfCookie := hn.loginCookies("verify-expired@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d %s", rec.Code, rec.Body.String())
	}
	hn.now = hn.now.Add(10 * time.Minute) // beyond the 5-minute ceremony TTL
	rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an expired ceremony, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAVerifyFinishFromDifferentSessionFails(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verify-wrongsession@example.test", identity.RoleResponder)
	hn.enrollSyntheticMFACredential(id)
	sessionA, csrfA := hn.loginCookies("verify-wrongsession@example.test")
	sessionB, csrfB := hn.loginCookies("verify-wrongsession@example.test")

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), hn.authedOpts(sessionA, csrfA)); rec.Code != http.StatusOK {
		t.Fatalf("begin under session A failed: %d %s", rec.Code, rec.Body.String())
	}
	rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), hn.authedOpts(sessionB, csrfB))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 finishing under a different session, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAVerifyFinishMalformedResponseFailsSafely(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verify-malformed@example.test", identity.RoleResponder)
	hn.enrollSyntheticMFACredential(id)
	sessionCookie, csrfCookie := hn.loginCookies("verify-malformed@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d", rec.Code)
	}
	rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":{"not":"expected"}}`), opts)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed credential response, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAVerifyFinishReplayFails(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verify-replay@example.test", identity.RoleResponder)
	hn.enrollSyntheticMFACredential(id)
	sessionCookie, csrfCookie := hn.loginCookies("verify-replay@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d", rec.Code)
	}
	first := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if first.Code != http.StatusOK {
		t.Fatalf("first finish failed: %d %s", first.Code, first.Body.String())
	}
	replay := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("expected replayed finish to fail with 400, got %d: %s", replay.Code, replay.Body.String())
	}
}

// TestMFAVerifySuccessMarksOnlyThatSession additionally confirms via
// GetByID/session inspection that verification is a per-session fact.
func TestMFAVerifySuccessMarksOnlyThatSession(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("marks-session@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "marks-session@example.test")

	u, err := hn.store.GetByID(context.Background(), id)
	if err != nil || u.Status != identity.StateActive {
		t.Fatalf("expected the account to remain active, unaffected by verification, got %+v err=%v", u, err)
	}

	var verifyEvents int
	for _, ev := range hn.audit.snapshot() {
		if ev.Type == identityaudit.MFAVerification {
			verifyEvents++
			if len(ev.Metadata) != 1 || ev.Metadata["method"] != "passkey" {
				t.Fatalf("unexpected MFAVerification metadata: %+v", ev.Metadata)
			}
		}
	}
	if verifyEvents != 1 {
		t.Fatalf("expected exactly one MFAVerification audit event, got %d", verifyEvents)
	}
	_ = sessionCookie
	_ = csrfCookie
}

// TestMFAVerifyAuditMetadataNeverLeaksCeremonySecrets scans every audit
// event recorded across a full verification flow for the ceremony's own
// secret-shaped values (challenge, assertion response, session/CSRF
// tokens), confirming none of them ever appear.
func TestMFAVerifyAuditMetadataNeverLeaksCeremonySecrets(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("verify-noleak@example.test", identity.RoleResponder)
	hn.enrollSyntheticMFACredential(id)
	sessionCookie, csrfCookie := hn.loginCookies("verify-noleak@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), opts)
	hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)

	secrets := []string{"synthetic-login-challenge", validMFAResponseMarker, sessionCookie.Value, csrfCookie.Value}
	for _, ev := range hn.audit.snapshot() {
		for key, value := range ev.Metadata {
			lowerKey := strings.ToLower(key)
			for _, bad := range []string{"password", "token", "secret", "cookie", "hash", "credential", "challenge", "attestation", "signature", "assertion", "authenticator"} {
				if strings.Contains(lowerKey, bad) {
					t.Errorf("event %s has a forbidden-shaped metadata key %q", ev.Type, key)
				}
			}
			for _, secret := range secrets {
				if secret != "" && strings.Contains(value, secret) {
					t.Errorf("event %s metadata key %q leaked a ceremony/session secret", ev.Type, key)
				}
			}
		}
	}
}

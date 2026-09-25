package authhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/authcookie"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/invitation"
)

// seedPendingMFAAdmin seeds an invited administrator, redeems the
// invitation over HTTP (first-time-login), and returns the resulting user
// id plus the MFA-enrollment and CSRF cookies the response set. The
// account remains password_change_required: MFA is the only remaining
// requirement.
func (hn *harness) seedPendingMFAAdmin(email string) (identity.UserID, *http.Cookie, *http.Cookie) {
	hn.t.Helper()
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		hn.t.Fatal(err)
	}
	userID := hn.store.seedUser(identity.User{
		NormalizedEmail: normalized, DisplayName: "Synthetic Pending Admin", Role: identity.RoleSystemAdministrator,
		Status: identity.StateInvited,
	})
	rawToken, err := invitation.GenerateToken()
	if err != nil {
		hn.t.Fatal(err)
	}
	hn.store.seedInvitation(userID, invitation.Digest(rawToken), hn.now.Add(time.Hour))

	body := []byte(`{"token":"` + rawToken + `","password":"` + testPassword + `"}`)
	rec := hn.request(http.MethodPost, "/api/auth/first-time-login", body, reqOpts{})
	if rec.Code != http.StatusOK {
		hn.t.Fatalf("first-time-login failed: %d %s", rec.Code, rec.Body.String())
	}
	var enrollCookie, csrfCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case authcookie.MFAEnrollCookieName:
			enrollCookie = c
		case authcookie.CSRFCookieName:
			csrfCookie = c
		}
	}
	if enrollCookie == nil || csrfCookie == nil {
		hn.t.Fatal("expected both mfa-enrollment and csrf cookies to be set")
	}
	return userID, enrollCookie, csrfCookie
}

func TestMFAEnrollBeginRequiresAuthentication(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), reqOpts{})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without any authentication, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAEnrollBeginRejectsSuspendedPendingAdmin(t *testing.T) {
	hn := newHarness(t)
	userID, enrollCookie, csrfCookie := hn.seedPendingMFAAdmin("suspended-pending@example.test")
	hn.store.mu.Lock()
	hn.store.users[userID].Status = identity.StateSuspended
	hn.store.mu.Unlock()

	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), hn.authedOpts(enrollCookie, csrfCookie))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a suspended pending-MFA account, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAEnrollBeginCreatesServerSideCeremonyState(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("bounded-mfa@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("bounded-mfa@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts)
	if rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["publicKey"]; !ok {
		t.Fatalf("expected publicKey creation options in the response, got %s", rec.Body.String())
	}

	// A second begin under the same session replaces the first (bounded,
	// single in-flight ceremony per authenticated context); the resulting
	// ceremony still finishes successfully.
	rec2 := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second begin failed: %d", rec2.Code)
	}
	finish := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if finish.Code != http.StatusOK {
		t.Fatalf("finish after a replaced begin failed: %d %s", finish.Code, finish.Body.String())
	}
}

func TestMFAEnrollFinishWithoutBeginFails(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("nobegin-mfa@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("nobegin-mfa@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 finishing without a prior begin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAEnrollFinishExpiredCeremonyFails(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("expired-mfa@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("expired-mfa@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d", rec.Code)
	}
	hn.now = hn.now.Add(10 * time.Minute) // beyond the 5-minute ceremony TTL, within the 15-minute session idle window
	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an expired ceremony, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAEnrollFinishFromDifferentSessionFails(t *testing.T) {
	hn := newHarness(t)
	_, enrollA, csrfA := hn.seedPendingMFAAdmin("pending-a@example.test")
	_, enrollB, csrfB := hn.seedPendingMFAAdmin("pending-b@example.test")

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), hn.authedOpts(enrollA, csrfA)); rec.Code != http.StatusOK {
		t.Fatalf("begin under admin A failed: %d %s", rec.Code, rec.Body.String())
	}
	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), hn.authedOpts(enrollB, csrfB))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 finishing under a different authenticated context, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAEnrollFinishMalformedResponseFailsSafely(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("malformed-mfa@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("malformed-mfa@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d", rec.Code)
	}
	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":{"not":"expected"}}`), opts)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed credential response, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMFAEnrollBeginAndFinishViaActiveSessionThenReplayFails(t *testing.T) {
	hn := newHarness(t)
	userID := hn.seedActiveUser("active-mfa@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("active-mfa@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d %s", rec.Code, rec.Body.String())
	}

	finishRec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if finishRec.Code != http.StatusOK {
		t.Fatalf("finish failed: %d %s", finishRec.Code, finishRec.Body.String())
	}
	var finishBody map[string]string
	if err := json.Unmarshal(finishRec.Body.Bytes(), &finishBody); err != nil {
		t.Fatal(err)
	}
	if finishBody["status"] != string(identity.StateActive) {
		t.Fatalf("expected status active (already-active user adding a passkey), got %q", finishBody["status"])
	}

	creds, err := hn.store.ListMFACredentials(context.Background(), userID)
	if err != nil || len(creds) != 1 {
		t.Fatalf("expected exactly one credential, got %d err=%v", len(creds), err)
	}

	var mfaEvents int
	for _, ev := range hn.audit.snapshot() {
		if ev.Type == identityaudit.MFAEnrollment {
			mfaEvents++
			if len(ev.Metadata) != 1 || ev.Metadata["method"] != "passkey" {
				t.Fatalf("unexpected MFAEnrollment metadata: %+v", ev.Metadata)
			}
		}
	}
	if mfaEvents != 1 {
		t.Fatalf("expected exactly one MFAEnrollment audit event, got %d", mfaEvents)
	}

	// The ceremony was already consumed by the successful finish above; the
	// session itself remains fully valid, so this is specifically a
	// ceremony-replay failure, not an authentication failure.
	replayRec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if replayRec.Code != http.StatusBadRequest {
		t.Fatalf("expected replayed finish to fail with 400, got %d: %s", replayRec.Code, replayRec.Body.String())
	}
}

func TestMFAEnrollFinishActivatesAdministratorWhenMFAWasLastRequirement(t *testing.T) {
	hn := newHarness(t)
	userID, enrollCookie, csrfCookie := hn.seedPendingMFAAdmin("activates-admin@example.test")
	opts := hn.authedOpts(enrollCookie, csrfCookie)

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin failed: %d %s", rec.Code, rec.Body.String())
	}
	rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if rec.Code != http.StatusOK {
		t.Fatalf("finish failed: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != string(identity.StateActive) {
		t.Fatalf("expected activation, got %q", body["status"])
	}
	u, err := hn.store.GetByID(context.Background(), userID)
	if err != nil || u.Status != identity.StateActive {
		t.Fatalf("expected persisted status active, got %+v err=%v", u, err)
	}

	var sawCleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == authcookie.MFAEnrollCookieName && c.MaxAge < 0 {
			sawCleared = true
		}
	}
	if !sawCleared {
		t.Fatal("expected the response to clear the MFA-enrollment cookie")
	}

	// The bridging credential is revoked server-side on success; presenting
	// it again (even though the test's http.Cookie value itself was never
	// mutated) must fail authentication outright now.
	replay := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("expected the now-consumed enrollment cookie to be rejected, got %d", replay.Code)
	}
}

func TestMFAEnrollAuditMetadataNeverLeaksCeremonySecrets(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("noleak-mfa@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("noleak-mfa@example.test")
	opts := hn.authedOpts(sessionCookie, csrfCookie)

	hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts)
	hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)

	secrets := []string{"synthetic-challenge", validMFAResponseMarker, sessionCookie.Value, csrfCookie.Value}
	for _, ev := range hn.audit.snapshot() {
		for key, value := range ev.Metadata {
			lowerKey := strings.ToLower(key)
			for _, bad := range []string{"password", "token", "secret", "cookie", "hash", "credential", "challenge", "attestation"} {
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

// TestMFAEnrollPrefersBridgeCredentialOverCoexistingSession is a Step 9F-6
// live-validation regression test: a browser that still holds a valid
// normal session cookie (for example, an administrator who redeemed a
// second account's invitation without signing out of their own session
// first) also presents a valid __Host-gfr_mfa_enroll bridge cookie for a
// DIFFERENT, pending account in the same request. Before this fix,
// requireSessionOrMFAEnrollment checked the session cookie first,
// unconditionally — authenticating as the unrelated session's account and
// producing a CSRF digest mismatch (the shared __Host-gfr_csrf cookie is
// derived from the bridge token) that surfaced as a generic "session
// expired" error. The bridge credential must now win: begin and finish
// both succeed, and the resulting passkey is recorded against the BRIDGE
// account, never the coexisting session's account.
func TestMFAEnrollPrefersBridgeCredentialOverCoexistingSession(t *testing.T) {
	hn := newHarness(t)
	activeUserID := hn.seedActiveUser("already-active-bystander@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("already-active-bystander@example.test")

	pendingUserID, enrollCookie, csrfCookie := hn.seedPendingMFAAdmin("pending-bridge-winner@example.test")

	// Exactly what a real browser sends when both credentials happen to be
	// present: the coexisting session cookie, the bridge cookie, and the
	// ONE shared CSRF cookie the bridge redemption most recently set (see
	// setMFAEnrollCookies).
	opts := reqOpts{
		cookies: []*http.Cookie{sessionCookie, enrollCookie, csrfCookie},
		headers: map[string]string{"X-CSRF-Token": csrfCookie.Value},
	}

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("begin with both a coexisting session and a valid bridge cookie failed: %d %s", rec.Code, rec.Body.String())
	}
	finish := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if finish.Code != http.StatusOK {
		t.Fatalf("finish with both a coexisting session and a valid bridge cookie failed: %d %s", finish.Code, finish.Body.String())
	}

	pendingCreds, err := hn.store.ListMFACredentials(context.Background(), pendingUserID)
	if err != nil || len(pendingCreds) != 1 {
		t.Fatalf("expected the bridge account to receive exactly one credential, got %d err=%v", len(pendingCreds), err)
	}
	activeCreds, err := hn.store.ListMFACredentials(context.Background(), activeUserID)
	if err != nil || len(activeCreds) != 0 {
		t.Fatalf("expected the coexisting session's account to remain untouched, got %d err=%v", len(activeCreds), err)
	}
}

// TestMFAEnrollFallsBackToSessionWhenBridgeCookieInvalid is the negative
// counterpart: an invalid/unresolvable __Host-gfr_mfa_enroll cookie (for
// example, a stale leftover from an unrelated, already-consumed or expired
// bridge) must never block the ordinary, already-active
// "add another passkey" enrollment path via a valid normal session —
// ResolveMFAEnrollmentCredential's failure is expected to fall through to
// the session branch, not fail the request outright.
func TestMFAEnrollFallsBackToSessionWhenBridgeCookieInvalid(t *testing.T) {
	hn := newHarness(t)
	userID := hn.seedActiveUser("fallback-mfa@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("fallback-mfa@example.test")

	invalidEnrollCookie := &http.Cookie{Name: authcookie.MFAEnrollCookieName, Value: "not-a-real-bridge-token"}
	opts := reqOpts{
		cookies: []*http.Cookie{invalidEnrollCookie, sessionCookie, csrfCookie},
		headers: map[string]string{"X-CSRF-Token": csrfCookie.Value},
	}

	if rec := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		t.Fatalf("expected fallback to the valid session to succeed despite an invalid bridge cookie, got %d: %s", rec.Code, rec.Body.String())
	}
	finish := hn.request(http.MethodPost, "/api/auth/mfa/enroll", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if finish.Code != http.StatusOK {
		t.Fatalf("expected finish via session fallback to succeed, got %d: %s", finish.Code, finish.Body.String())
	}

	creds, err := hn.store.ListMFACredentials(context.Background(), userID)
	if err != nil || len(creds) != 1 {
		t.Fatalf("expected the session account to receive the new passkey via fallback, got %d err=%v", len(creds), err)
	}
}

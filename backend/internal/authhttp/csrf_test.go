package authhttp

import (
	"net/http"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

func TestCSRFMissingTokenRejected(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf1@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("csrf1@example.test")

	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing CSRF token, got %d", rec.Code)
	}
}

func TestCSRFMalformedTokenRejected(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf2@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("csrf2@example.test")

	opts := reqOpts{
		cookies: []*http.Cookie{sessionCookie, csrfCookie},
		headers: map[string]string{"X-CSRF-Token": "not-a-real-token"},
	}
	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, opts)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a malformed CSRF token, got %d", rec.Code)
	}
}

func TestCSRFMismatchedTokenRejected(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf3a@example.test", identity.RoleResponder)
	hn.seedActiveUser("csrf3b@example.test", identity.RoleResponder)
	sessionA, _ := hn.loginCookies("csrf3a@example.test")
	_, csrfB := hn.loginCookies("csrf3b@example.test")

	// Session cookie from user A, CSRF header/cookie derived for user B's
	// session: must not verify against A's session.
	opts := reqOpts{
		cookies: []*http.Cookie{sessionA, csrfB},
		headers: map[string]string{"X-CSRF-Token": csrfB.Value},
	}
	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, opts)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a mismatched-session CSRF token, got %d", rec.Code)
	}
}

func TestCSRFCrossOriginRejected(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf4@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("csrf4@example.test")

	opts := reqOpts{
		cookies: []*http.Cookie{sessionCookie, csrfCookie},
		headers: map[string]string{"X-CSRF-Token": csrfCookie.Value},
		origin:  "https://evil.example.net",
	}
	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, opts)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a cross-origin request, got %d", rec.Code)
	}
}

func TestCSRFMissingOriginRejected(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf5@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("csrf5@example.test")

	opts := reqOpts{
		cookies:  []*http.Cookie{sessionCookie, csrfCookie},
		headers:  map[string]string{"X-CSRF-Token": csrfCookie.Value},
		noOrigin: true,
	}
	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, opts)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a missing Origin header, got %d", rec.Code)
	}
}

func TestCSRFValidTokenAccepted(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf6@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("csrf6@example.test")

	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a valid CSRF token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCSRFRejectedForExpiredSession(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf7@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("csrf7@example.test")

	hn.now = hn.now.Add(13 * time.Hour) // beyond the 12h absolute lifetime
	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an expired session before CSRF is even relevant, got %d", rec.Code)
	}
}

func TestCSRFRejectedForRevokedSession(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf8@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("csrf8@example.test")

	// Revoke the session out from under the cookie (as an admin action or a
	// second logout would).
	first := hn.request(http.MethodPost, "/api/auth/logout", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if first.Code != http.StatusOK {
		t.Fatalf("expected the first logout to succeed, got %d", first.Code)
	}
	second := hn.request(http.MethodPost, "/api/auth/logout", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a revoked session, got %d", second.Code)
	}
}

func TestSafeReadRequestsNeverRequireCSRF(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("csrf9@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("csrf9@example.test")

	rec := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected GET /api/auth/me to succeed without a CSRF token, got %d", rec.Code)
	}
}

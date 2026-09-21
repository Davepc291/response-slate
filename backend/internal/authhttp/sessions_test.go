package authhttp

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/session"
)

func TestMeReturnsCurrentAuthenticatedUser(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("me@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("me@example.test")

	rec := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Email != "me@example.test" {
		t.Fatalf("unexpected email: %s", resp.Email)
	}
}

func TestMeRejectsNoCookie(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMeRejectsIdleExpiredSession(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("idle@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("idle@example.test")

	hn.now = hn.now.Add(20 * time.Minute) // beyond the 15m idle timeout
	rec := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an idle-expired session, got %d", rec.Code)
	}
}

func TestMeRejectsAbsoluteExpiredSession(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("absolute@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("absolute@example.test")

	hn.now = hn.now.Add(13 * time.Hour)
	rec := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an absolute-expired session, got %d", rec.Code)
	}
}

func TestMeRejectsSuspendedAccount(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("suspendme@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("suspendme@example.test")

	hn.store.mu.Lock()
	hn.store.users[id].Status = identity.StateSuspended
	hn.store.mu.Unlock()

	rec := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 once the underlying account is suspended, got %d", rec.Code)
	}
}

func TestMeRejectsDisabledAccount(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("disableme@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("disableme@example.test")

	hn.store.mu.Lock()
	hn.store.users[id].Status = identity.StateDisabled
	hn.store.mu.Unlock()

	rec := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 once the underlying account is disabled, got %d", rec.Code)
	}
}

func TestLogoutClearsSecureCookies(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("logoutcookie@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("logoutcookie@example.test")

	rec := hn.request(http.MethodPost, "/api/auth/logout", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var clearedSession, clearedCSRF bool
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 {
			continue
		}
		switch c.Name {
		case "__Host-gfr_session":
			clearedSession = true
		case "__Host-gfr_csrf":
			clearedCSRF = true
		}
	}
	if !clearedSession || !clearedCSRF {
		t.Fatal("expected both cookies to be cleared on logout")
	}
}

func TestLogoutAllRevokesEverySessionAndClearsCookies(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("logoutall@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("logoutall@example.test")
	second, _ := hn.loginCookies("logoutall@example.test")

	rec := hn.request(http.MethodPost, "/api/auth/logout-all", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	for _, cookie := range []*http.Cookie{sessionCookie, second} {
		check := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{cookie}})
		if check.Code != http.StatusUnauthorized {
			t.Fatalf("expected every session to be revoked, got %d for one of them", check.Code)
		}
	}
}

func TestListSessionsReturnsOnlyOwnSessions(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("listmine@example.test", identity.RoleResponder)
	hn.seedActiveUser("listother@example.test", identity.RoleResponder)
	sessionCookie, _ := hn.loginCookies("listmine@example.test")
	hn.loginCookies("listother@example.test")

	rec := hn.request(http.MethodGet, "/api/auth/sessions", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var sessions []sessionView
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected exactly one session, got %d", len(sessions))
	}
	if !sessions[0].Current {
		t.Fatal("expected the sole session to be marked current")
	}
}

func TestCannotRevokeAnotherUsersSession(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("victim@example.test", identity.RoleResponder)
	hn.seedActiveUser("attacker@example.test", identity.RoleResponder)
	victimSession, _ := hn.loginCookies("victim@example.test")
	attackerSession, attackerCSRF := hn.loginCookies("attacker@example.test")

	victimSessions, err := hn.svcListSessions("victim@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(victimSessions) != 1 {
		t.Fatalf("expected the victim to have exactly one session, got %d", len(victimSessions))
	}
	targetID := strconv.FormatInt(int64(victimSessions[0].ID), 10)

	opts := hn.authedOpts(attackerSession, attackerCSRF)
	rec := hn.request(http.MethodDelete, "/api/auth/sessions/"+targetID, nil, opts)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (not owned, indistinguishable from unknown), got %d", rec.Code)
	}

	// The victim's session must remain valid.
	check := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{victimSession}})
	if check.Code != http.StatusOK {
		t.Fatalf("expected the victim's session to remain valid, got %d", check.Code)
	}
}

func TestRevokeOwnSessionSucceeds(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("revokeself@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("revokeself@example.test")

	sessions, err := hn.svcListSessions("revokeself@example.test")
	if err != nil {
		t.Fatal(err)
	}
	targetID := strconv.FormatInt(int64(sessions[0].ID), 10)

	rec := hn.request(http.MethodDelete, "/api/auth/sessions/"+targetID, nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// svcListSessions is a small test-only convenience wrapping the fake store
// directly (bypassing HTTP), used only to discover a real session id to
// exercise the DELETE handler against.
func (hn *harness) svcListSessions(email string) ([]session.Session, error) {
	hn.store.mu.Lock()
	defer hn.store.mu.Unlock()
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	var userID identity.UserID
	for _, u := range hn.store.users {
		if u.NormalizedEmail == normalized {
			userID = u.ID
		}
	}
	var out []session.Session
	for _, s := range hn.store.sessions {
		if identity.UserID(s.UserID) == userID && s.RevokedAt == nil {
			out = append(out, s.Session)
		}
	}
	return out, nil
}

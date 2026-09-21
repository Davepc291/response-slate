package authhttp

import (
	"encoding/json"
	"net/http"
	"testing"

	"greenwich-fire-responder/backend/internal/authcookie"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
)

func TestLoginSuccessSetsSecureCookies(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("responder@example.test", identity.RoleResponder)

	body := []byte(`{"email":"responder@example.test","password":"` + testPassword + `"}`)
	rec := hn.request(http.MethodPost, "/api/auth/login", body, reqOpts{})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("expected Cache-Control: no-store")
	}

	var sawSession, sawCSRF bool
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case authcookie.SessionCookieName:
			sawSession = true
			if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
				t.Errorf("session cookie attributes unsafe: %+v", c)
			}
		case authcookie.CSRFCookieName:
			sawCSRF = true
			if !c.Secure || c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Errorf("csrf cookie attributes unexpected: %+v", c)
			}
		}
	}
	if !sawSession || !sawCSRF {
		t.Fatal("expected both cookies to be set")
	}

	// No JWT / user data in the cookie: the session cookie value must be an
	// opaque token, not a dot-separated JWT and not the email/role/etc.
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == authcookie.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected a session cookie")
	}
	if containsAny(sessionCookie.Value, ".", "responder@example.test", "role", "eyJ") {
		t.Errorf("session cookie value looks structured/JWT-like or leaks account data: %q", sessionCookie.Value)
	}

	var resp meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Email != "responder@example.test" || resp.Status != "active" {
		t.Fatalf("unexpected login response: %+v", resp)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && contains(s, sub) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestLoginUnknownAndWrongPasswordAreIndistinguishable(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("known@example.test", identity.RoleResponder)

	unknown := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"unknown@example.test","password":"whatever-value-1"}`), reqOpts{})
	wrong := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"known@example.test","password":"definitely-wrong-value"}`), reqOpts{})

	if unknown.Code != http.StatusUnauthorized || wrong.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401/401, got %d/%d", unknown.Code, wrong.Code)
	}
	if unknown.Body.String() != wrong.Body.String() {
		t.Fatalf("expected identical bodies, got %q vs %q", unknown.Body.String(), wrong.Body.String())
	}
}

func TestLoginNonActiveAccountDenied(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("suspended@example.test", identity.RoleResponder)
	hn.store.mu.Lock()
	hn.store.users[id].Status = identity.StateSuspended
	hn.store.mu.Unlock()

	rec := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"suspended@example.test","password":"`+testPassword+`"}`), reqOpts{})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for suspended account, got %d", rec.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("ratelimited@example.test", identity.RoleResponder)

	var lastCode int
	for i := 0; i < 25; i++ {
		rec := hn.request(http.MethodPost, "/api/auth/login",
			[]byte(`{"email":"ratelimited@example.test","password":"wrong-password-value"}`), reqOpts{})
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected eventual 429, got %d", lastCode)
	}
}

func TestLoginAuditNeverContainsSecrets(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("auditcheck@example.test", identity.RoleResponder)
	hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"auditcheck@example.test","password":"`+testPassword+`"}`), reqOpts{})

	for _, ev := range hn.audit.snapshot() {
		for k, v := range ev.Metadata {
			if v == testPassword {
				t.Fatalf("audit metadata key %q leaked the password", k)
			}
		}
		if ev.Type == identityaudit.LoginSuccess || ev.Type == identityaudit.SessionCreated {
			continue
		}
	}
}

func TestNoPublicSelfRegistrationRoute(t *testing.T) {
	hn := newHarness(t)
	for _, path := range []string{"/api/auth/register", "/api/auth/signup", "/api/admin/users"} {
		rec := hn.request(http.MethodPost, path, []byte(`{}`), reqOpts{})
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			t.Fatalf("expected no self-registration route to succeed at %s, got %d", path, rec.Code)
		}
	}
}

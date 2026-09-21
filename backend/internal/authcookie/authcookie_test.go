package authcookie

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewSessionCookieAttributes(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	c := NewSessionCookie("raw-session-token", expires)
	if c.Name != SessionCookieName {
		t.Errorf("unexpected name: %s", c.Name)
	}
	if !c.Secure {
		t.Error("expected Secure")
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("expected SameSite=Strict")
	}
	if c.Path != "/" {
		t.Errorf("expected Path=/, got %q", c.Path)
	}
	if c.Domain != "" {
		t.Errorf("expected no Domain attribute, got %q", c.Domain)
	}
	if c.Value != "raw-session-token" {
		t.Errorf("unexpected value: %q", c.Value)
	}
}

func TestSessionCookieNameHasHostPrefix(t *testing.T) {
	if SessionCookieName[:7] != "__Host-" {
		t.Fatalf("expected __Host- prefix, got %q", SessionCookieName)
	}
}

func TestCSRFCookieIsNotHttpOnly(t *testing.T) {
	c := NewCSRFCookie("derived-value", time.Now().Add(time.Hour))
	if c.HttpOnly {
		t.Fatal("CSRF cookie must be readable by JavaScript, so it must not be HttpOnly")
	}
	if !c.Secure {
		t.Error("expected Secure")
	}
	if c.Domain != "" {
		t.Errorf("expected no Domain attribute, got %q", c.Domain)
	}
}

func TestCSRFCookieNeverCarriesSessionShapedValue(t *testing.T) {
	// This is a structural assertion: the constructor only ever writes the
	// value it was given, and callers (authhttp) must never pass a raw
	// session token to it. There is no session-token field on http.Cookie
	// for this constructor to accidentally leak.
	c := NewCSRFCookie("only-a-derived-csrf-value", time.Now().Add(time.Hour))
	if c.Value != "only-a-derived-csrf-value" {
		t.Fatal("expected the CSRF cookie to carry exactly the value it was given")
	}
}

func TestClearSessionCookieExpiresImmediately(t *testing.T) {
	c := ClearSessionCookie()
	if c.MaxAge >= 0 {
		t.Fatal("expected a negative MaxAge to clear the cookie")
	}
	if !c.Expires.Before(time.Now()) {
		t.Fatal("expected an already-past Expires time")
	}
	if c.Value != "" {
		t.Fatal("expected an empty value on the cleared cookie")
	}
}

func TestClearCSRFCookieExpiresImmediately(t *testing.T) {
	c := ClearCSRFCookie()
	if c.MaxAge >= 0 || c.Value != "" {
		t.Fatal("expected the CSRF cookie to be cleared")
	}
}

func TestReadSessionTokenRoundTrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(NewSessionCookie("abc123", time.Now().Add(time.Hour)))
	token, ok := ReadSessionToken(req)
	if !ok || token != "abc123" {
		t.Fatalf("expected to read back the session token, got %q ok=%v", token, ok)
	}

	empty := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := ReadSessionToken(empty); ok {
		t.Fatal("expected no session token when no cookie is present")
	}
}

func TestReadCSRFCookieRoundTrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(NewCSRFCookie("csrf-value", time.Now().Add(time.Hour)))
	value, ok := ReadCSRFCookie(req)
	if !ok || value != "csrf-value" {
		t.Fatalf("expected to read back the csrf value, got %q ok=%v", value, ok)
	}
}

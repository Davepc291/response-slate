// Package authcookie constructs and reads the Step 9C browser session and
// CSRF cookies approved by docs/authentication-authorization-v1.md Section 8
// (Sessions): a secure, HttpOnly, SameSite=Strict, path-scoped, Domain-less
// cookie carrying only an opaque random session token, never a JWT and
// never user information. A companion, JavaScript-readable CSRF cookie
// carries only a derived CSRF value (see authcsrf), never the session
// value. Every cookie name below uses the __Host- prefix, which the
// browser itself enforces (Secure, Path=/, no Domain) as a second,
// independent guarantee beyond this package's own attribute choices.
package authcookie

import (
	"net/http"
	"time"
)

// SessionCookieName is the browser session cookie. The __Host- prefix is
// used because the approved contract does not name a different cookie
// name, and this prefix is the strongest browser-enforced guarantee
// available for a same-origin, path-scoped, non-subdomain-shared cookie.
const SessionCookieName = "__Host-gfr_session"

// CSRFCookieName is the companion, JavaScript-readable CSRF cookie. It is
// never HttpOnly, by design: the client must be able to read it and echo
// its value back in a request header (see authcsrf) for the synchronizer
// check to work at all.
const CSRFCookieName = "__Host-gfr_csrf"

// MFAEnrollCookieName is the Step 9F-3 narrow, short-lived credential that
// bridges an administrator from "just set a permanent password" to
// "completed MFA enrollment" (see identityservice's mfa.go package doc
// comment for why this cannot be the normal session cookie: the existing
// session machinery never authenticates a non-active account, by design,
// and this cookie does not change that — it authorizes nothing beyond the
// MFA enrollment route). It shares the CSRF cookie above rather than
// defining its own, since only one of the two credentials is ever presented
// by a given browser at a time.
const MFAEnrollCookieName = "__Host-gfr_mfa_enroll"

// NewSessionCookie builds the Set-Cookie value for a freshly created
// session. rawToken is the one-time, plaintext opaque token (never logged,
// never stored anywhere but this cookie and, as a digest, the sessions
// table). expiresAt must never exceed the server-side session's own
// absolute expiration, so the cookie can never outlive the session record
// it names (Section 8: "Cookie expiration must not outlive the server-side
// session").
func NewSessionCookie(rawToken string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	}
}

// ClearSessionCookie builds a Set-Cookie value that immediately expires the
// browser's session cookie (Section 8: "Logout... the client-side state is
// cleared"). It must always be sent alongside the corresponding server-side
// session revocation, never as a substitute for it.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	}
}

// NewCSRFCookie builds the Set-Cookie value for the readable CSRF cookie.
// value must be a derived/random CSRF value only (see authcsrf.Derive),
// never the raw session token or any other session-identifying value.
// expiresAt should match the session cookie's own expiration so the two
// always rotate and expire together.
func NewCSRFCookie(value string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CSRFCookieName,
		Value:    value,
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	}
}

// ClearCSRFCookie expires the CSRF cookie alongside the session cookie
// (Section 8/CSRF requirement: "Rotate/remove CSRF state when the session
// changes or ends").
func ClearCSRFCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	}
}

// NewMFAEnrollCookie builds the Set-Cookie value for a freshly issued
// MFA-enrollment bridging credential (Step 9F-3). It carries the identical
// security attributes as the session cookie (Secure, HttpOnly,
// SameSite=Strict, __Host--prefixed) — it is just as sensitive as a session
// token — but under its own distinct name so it can never be confused with,
// or accepted in place of, a real session by any code that only knows to
// look for SessionCookieName.
func NewMFAEnrollCookie(rawToken string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     MFAEnrollCookieName,
		Value:    rawToken,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	}
}

// ClearMFAEnrollCookie expires the browser's MFA-enrollment cookie, mirroring
// ClearSessionCookie. Safe to send even when the client never had one set.
func ClearMFAEnrollCookie() *http.Cookie {
	return &http.Cookie{
		Name:     MFAEnrollCookieName,
		Value:    "",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	}
}

// ReadMFAEnrollToken extracts the raw MFA-enrollment bridging token from
// req, if present.
func ReadMFAEnrollToken(req *http.Request) (string, bool) {
	c, err := req.Cookie(MFAEnrollCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// ReadSessionToken extracts the raw session token from req, if present.
func ReadSessionToken(req *http.Request) (string, bool) {
	c, err := req.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// ReadCSRFCookie extracts the CSRF cookie's value from req, if present.
func ReadCSRFCookie(req *http.Request) (string, bool) {
	c, err := req.Cookie(CSRFCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

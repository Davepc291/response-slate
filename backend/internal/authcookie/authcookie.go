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

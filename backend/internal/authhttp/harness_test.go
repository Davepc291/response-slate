package authhttp

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"greenwich-fire-responder/backend/internal/adminservice"
	"greenwich-fire-responder/backend/internal/authconfig"
	"greenwich-fire-responder/backend/internal/authcookie"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityservice"
	"greenwich-fire-responder/backend/internal/mfa"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
	"greenwich-fire-responder/backend/internal/session"
)

const testOrigin = "https://app.example.test"
const testPassword = "correct-horse-battery-staple"

var fixedNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func testHashParams() passwordpolicy.Params {
	return passwordpolicy.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
}

func testAuthOptions() authconfig.Options {
	return authconfig.Options{
		Enabled:                        true,
		SessionIdleTimeout:             15 * time.Minute,
		SessionMaxLifetime:             12 * time.Hour,
		PasswordResetTTL:               time.Hour,
		InvitationTTL:                  24 * time.Hour,
		CSRFSecret:                     []byte("01234567890123456789012345678901"),
		AllowedOrigins:                 []string{testOrigin},
		LoginRateLimitPerAccount:       authconfig.RateLimit{MaxAttempts: 5, Window: 15 * time.Minute},
		LoginRateLimitPerIP:            authconfig.RateLimit{MaxAttempts: 20, Window: 15 * time.Minute},
		InvitationRateLimit:            authconfig.RateLimit{MaxAttempts: 10, Window: time.Hour},
		PasswordResetRequestRateLimit:  authconfig.RateLimit{MaxAttempts: 5, Window: time.Hour},
		PasswordResetCompleteRateLimit: authconfig.RateLimit{MaxAttempts: 5, Window: time.Hour},
	}
}

func testServiceConfig() identityservice.Config {
	return identityservice.Config{
		Session:          session.Config{IdleTimeout: 15 * time.Minute, AbsoluteLifetime: 12 * time.Hour},
		PasswordResetTTL: time.Hour,
		PasswordPolicy:   passwordpolicy.DefaultPolicy(),
		HashParams:       testHashParams(),
	}
}

type harness struct {
	t      *testing.T
	h      *Handlers
	store  *fakeStore
	audit  *fakeAudit
	mux    http.Handler
	now    time.Time
	remote string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := newFakeStore()
	audit := &fakeAudit{}
	svc := identityservice.New(store, audit, testServiceConfig(), nil)
	svc.SetMFAProvider(&fakeMFAProvider{})
	handlers, err := New(svc, testAuthOptions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	adminSvc := adminservice.New(store, audit, adminservice.Config{InvitationTTL: 24 * time.Hour}, nil)
	handlers.SetAdmin(adminSvc)
	hn := &harness{t: t, h: handlers, store: store, audit: audit, now: fixedNow, remote: "203.0.113.10:5555"}
	handlers.clock = func() time.Time { return hn.now }
	hn.mux = handlers.Mux()
	return hn
}

type reqOpts struct {
	origin        string
	noOrigin      bool
	contentType   string
	noContentType bool
	cookies       []*http.Cookie
	headers       map[string]string
	remoteAddr    string
}

func (hn *harness) newRawRequest(method, path string, body []byte, opts reqOpts) *http.Request {
	hn.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if !opts.noContentType {
		ct := opts.contentType
		if ct == "" && body != nil {
			ct = "application/json"
		}
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
	}
	if !opts.noOrigin {
		origin := opts.origin
		if origin == "" {
			origin = testOrigin
		}
		req.Header.Set("Origin", origin)
	}
	for k, v := range opts.headers {
		req.Header.Set(k, v)
	}
	for _, c := range opts.cookies {
		req.AddCookie(c)
	}
	req.RemoteAddr = hn.remote
	if opts.remoteAddr != "" {
		req.RemoteAddr = opts.remoteAddr
	}
	return req
}

func (hn *harness) recordOn(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	hn.t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func (hn *harness) request(method, path string, body []byte, opts reqOpts) *httptest.ResponseRecorder {
	hn.t.Helper()
	return hn.recordOn(hn.mux, hn.newRawRequest(method, path, body, opts))
}

func (hn *harness) seedActiveUser(email string, role identity.Role) identity.UserID {
	hn.t.Helper()
	hash, err := passwordpolicy.Hash(testPassword, testHashParams())
	if err != nil {
		hn.t.Fatal(err)
	}
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		hn.t.Fatal(err)
	}
	return hn.store.seedUser(identity.User{
		NormalizedEmail: normalized, DisplayName: "Synthetic Test User", Role: role,
		Status: identity.StateActive, PasswordHash: hash,
	})
}

// loginCookies logs in over HTTP and returns the two cookies the response
// set, for use by later requests in the same test.
func (hn *harness) loginCookies(email string) (sessionCookie, csrfCookie *http.Cookie) {
	hn.t.Helper()
	body := []byte(`{"email":"` + email + `","password":"` + testPassword + `"}`)
	rec := hn.request(http.MethodPost, "/api/auth/login", body, reqOpts{})
	if rec.Code != http.StatusOK {
		hn.t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case authcookie.SessionCookieName:
			sessionCookie = c
		case authcookie.CSRFCookieName:
			csrfCookie = c
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		hn.t.Fatal("expected both session and csrf cookies to be set")
	}
	return
}

// enrollSyntheticMFACredential seeds a non-revoked, synthetic passkey
// credential directly in the fake store for userID, bypassing the
// enrollment ceremony itself (already exercised end-to-end by
// TestMFAEnroll*): tests that need Step 9F-4 session verification only
// need SOME enrolled credential to exist first, since BeginMFALogin
// refuses to begin a ceremony for an account with none.
func (hn *harness) enrollSyntheticMFACredential(userID identity.UserID) {
	hn.t.Helper()
	credData, err := mfa.MarshalCredential(&webauthn.Credential{
		ID:        []byte(fmt.Sprintf("harness-credential-%d", userID)),
		PublicKey: []byte("harness-public-key"),
	})
	if err != nil {
		hn.t.Fatal(err)
	}
	hn.store.mu.Lock()
	hn.store.mfaCreds[userID] = append(hn.store.mfaCreds[userID], &fakeMFACredential{
		credentialType: "passkey", credentialData: credData,
	})
	hn.store.mu.Unlock()
}

// verifyMFAOverHTTP drives the full WebAuthn login-verification ceremony
// (POST /api/auth/mfa/verify, begin then finish) against the session named
// by sessionCookie/csrfCookie, upgrading exactly that session to
// MFA-verified. The account must already have an enrolled credential (see
// enrollSyntheticMFACredential).
func (hn *harness) verifyMFAOverHTTP(sessionCookie, csrfCookie *http.Cookie) {
	hn.t.Helper()
	opts := hn.authedOpts(sessionCookie, csrfCookie)
	if rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"begin"}`), opts); rec.Code != http.StatusOK {
		hn.t.Fatalf("mfa verify begin failed: %d %s", rec.Code, rec.Body.String())
	}
	rec := hn.request(http.MethodPost, "/api/auth/mfa/verify", []byte(`{"action":"finish","credential":"`+validMFAResponseMarker+`"}`), opts)
	if rec.Code != http.StatusOK {
		hn.t.Fatalf("mfa verify finish failed: %d %s", rec.Code, rec.Body.String())
	}
}

// loginAndVerifyMFACookies logs in over HTTP, seeds a synthetic enrolled
// credential for userID if it does not already have a usable one, and
// completes the WebAuthn verification ceremony for the resulting session —
// the one path every admin-route test that expects success must use
// instead of the plain loginCookies, since Step 9F-4 requires an
// MFA-verified current session for an administrator caller.
func (hn *harness) loginAndVerifyMFACookies(userID identity.UserID, email string) (sessionCookie, csrfCookie *http.Cookie) {
	hn.t.Helper()
	sessionCookie, csrfCookie = hn.loginCookies(email)
	hn.enrollSyntheticMFACredential(userID)
	hn.verifyMFAOverHTTP(sessionCookie, csrfCookie)
	return sessionCookie, csrfCookie
}

func (hn *harness) authedOpts(sessionCookie, csrfCookie *http.Cookie) reqOpts {
	return reqOpts{
		cookies: []*http.Cookie{sessionCookie, csrfCookie},
		headers: map[string]string{"X-CSRF-Token": csrfCookie.Value},
	}
}

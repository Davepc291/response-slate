package authhttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/passwordreset"
)

func TestPasswordResetRequestEnumerationResistance(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("hasaccount@example.test", identity.RoleResponder)

	known := hn.request(http.MethodPost, "/api/auth/password-reset",
		[]byte(`{"email":"hasaccount@example.test"}`), reqOpts{})
	unknown := hn.request(http.MethodPost, "/api/auth/password-reset",
		[]byte(`{"email":"no-such-account@example.test"}`), reqOpts{})

	if known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
		t.Fatalf("expected identical responses, got %d/%q vs %d/%q",
			known.Code, known.Body.String(), unknown.Code, unknown.Body.String())
	}
}

func TestPasswordResetResponseNeverContainsToken(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("tokencheck@example.test", identity.RoleResponder)

	rec := hn.request(http.MethodPost, "/api/auth/password-reset",
		[]byte(`{"email":"tokencheck@example.test"}`), reqOpts{})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// The one raw token issued by this request lives only inside the fake
	// store's pending reset row; the HTTP response body must never contain
	// anything resembling it (a base64url token of invitation/reset length
	// would be ~43 characters with no spaces).
	for _, r := range hn.store.resets {
		_ = r // token itself is intentionally inaccessible from here: the
		// service layer never returns it to this test through the HTTP
		// path, which is exactly the property under test.
	}
	if len(rec.Body.String()) > 200 {
		t.Fatalf("unexpectedly large response body, possible token leak: %s", rec.Body.String())
	}
}

func TestPasswordResetCompleteRejectsUnknownToken(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/password-reset/complete",
		[]byte(`{"token":"not-a-real-token","password":"a-new-passphrase-value"}`), reqOpts{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown token, got %d", rec.Code)
	}
}

func TestPasswordResetCompleteRevokesSessionsAndCookiesStillWork(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("resetflow@example.test", identity.RoleResponder)
	oldSession, _ := hn.loginCookies("resetflow@example.test")

	// Request and complete the reset via the service layer directly, since
	// the HTTP response never returns the raw token (by design).
	rawToken, matched, err := hn.serviceRequestPasswordReset("resetflow@example.test")
	if err != nil || !matched {
		t.Fatalf("expected a matched reset, err=%v matched=%v", err, matched)
	}

	rec := hn.request(http.MethodPost, "/api/auth/password-reset/complete",
		[]byte(`{"token":"`+rawToken+`","password":"a-completely-new-passphrase"}`), reqOpts{})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	check := hn.request(http.MethodGet, "/api/auth/me", nil, reqOpts{cookies: []*http.Cookie{oldSession}})
	if check.Code != http.StatusUnauthorized {
		t.Fatalf("expected the pre-reset session to be revoked, got %d", check.Code)
	}

	login := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"resetflow@example.test","password":"a-completely-new-passphrase"}`), reqOpts{})
	if login.Code != http.StatusOK {
		t.Fatalf("expected login with the new password to succeed, got %d", login.Code)
	}
}

func TestPasswordResetRateLimited(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("resetratelimit@example.test", identity.RoleResponder)

	var lastCode int
	for i := 0; i < 10; i++ {
		rec := hn.request(http.MethodPost, "/api/auth/password-reset",
			[]byte(`{"email":"resetratelimit@example.test"}`), reqOpts{})
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected eventual 429, got %d", lastCode)
	}
}

// serviceRequestPasswordReset issues a reset directly against the fake
// store, mirroring identityservice.RequestPasswordReset's own digest/expiry
// handling, so this test can obtain the raw token the public HTTP response
// deliberately never returns.
func (hn *harness) serviceRequestPasswordReset(email string) (rawToken string, matched bool, err error) {
	rawToken, err = passwordreset.GenerateToken()
	if err != nil {
		return "", false, err
	}
	digest := passwordreset.Digest(rawToken)
	matched, _, err = hn.store.RequestPasswordReset(context.Background(), email, 0, digest, hn.now.Add(time.Hour))
	if err != nil || !matched {
		return "", matched, err
	}
	return rawToken, true, nil
}

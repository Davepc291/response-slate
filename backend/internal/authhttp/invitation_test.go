package authhttp

import (
	"net/http"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/invitation"
)

func (hn *harness) seedInvitedUser(email string, role identity.Role, ttl time.Duration) string {
	hn.t.Helper()
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		hn.t.Fatal(err)
	}
	id := hn.store.seedUser(identity.User{NormalizedEmail: normalized, DisplayName: "Synthetic Invitee", Role: role, Status: identity.StateInvited})
	rawToken, err := invitation.GenerateToken()
	if err != nil {
		hn.t.Fatal(err)
	}
	hn.store.seedInvitation(id, invitation.Digest(rawToken), hn.now.Add(ttl))
	return rawToken
}

func TestFirstTimeLoginRedeemsAndEstablishesPassword(t *testing.T) {
	hn := newHarness(t)
	rawToken := hn.seedInvitedUser("invitee@example.test", identity.RoleResponder, time.Hour)

	body := []byte(`{"token":"` + rawToken + `","password":"a-brand-new-passphrase"}`)
	rec := hn.request(http.MethodPost, "/api/auth/first-time-login", body, reqOpts{})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	login := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"invitee@example.test","password":"a-brand-new-passphrase"}`), reqOpts{})
	if login.Code != http.StatusOK {
		t.Fatalf("expected the new password to work, got %d", login.Code)
	}
}

func TestFirstTimeLoginRejectsReuse(t *testing.T) {
	hn := newHarness(t)
	rawToken := hn.seedInvitedUser("reuseinvite@example.test", identity.RoleResponder, time.Hour)

	first := hn.request(http.MethodPost, "/api/auth/first-time-login",
		[]byte(`{"token":"`+rawToken+`","password":"first-passphrase-value"}`), reqOpts{})
	if first.Code != http.StatusOK {
		t.Fatalf("expected first redemption to succeed, got %d", first.Code)
	}
	second := hn.request(http.MethodPost, "/api/auth/first-time-login",
		[]byte(`{"token":"`+rawToken+`","password":"second-passphrase-value"}`), reqOpts{})
	if second.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on reuse, got %d", second.Code)
	}
}

func TestFirstTimeLoginRejectsExpired(t *testing.T) {
	hn := newHarness(t)
	rawToken := hn.seedInvitedUser("expiredinvite@example.test", identity.RoleResponder, -time.Minute)

	rec := hn.request(http.MethodPost, "/api/auth/first-time-login",
		[]byte(`{"token":"`+rawToken+`","password":"some-passphrase-value"}`), reqOpts{})
	if rec.Code != http.StatusGone {
		t.Fatalf("expected 410 for an expired invitation, got %d", rec.Code)
	}
}

func TestFirstTimeLoginRateLimited(t *testing.T) {
	hn := newHarness(t)
	rawToken := hn.seedInvitedUser("ratelimitedinvite@example.test", identity.RoleResponder, time.Hour)
	_ = rawToken

	var lastCode int
	for i := 0; i < 15; i++ {
		rec := hn.request(http.MethodPost, "/api/auth/first-time-login",
			[]byte(`{"token":"not-the-real-token","password":"some-passphrase-value"}`), reqOpts{})
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected eventual 429, got %d", lastCode)
	}
}

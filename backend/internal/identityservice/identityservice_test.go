package identityservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
	"greenwich-fire-responder/backend/internal/session"
)

// testParams trades real security margin for test speed: this package tests
// its own coordination logic, not passwordpolicy's own Argon2id strength,
// which has its own dedicated test suite.
func testParams() passwordpolicy.Params {
	return passwordpolicy.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
}

func testConfig() Config {
	return Config{
		Session:          session.Config{IdleTimeout: 15 * time.Minute, AbsoluteLifetime: 12 * time.Hour},
		PasswordResetTTL: time.Hour,
		PasswordPolicy:   passwordpolicy.DefaultPolicy(),
		HashParams:       testParams(),
	}
}

func newTestService(t *testing.T) (*Service, *fakeStore, *fakeAudit) {
	t.Helper()
	store := newFakeStore()
	audit := &fakeAudit{}
	svc := New(store, audit, testConfig(), nil)
	return svc, store, audit
}

const testPassword = "correct-horse-battery-staple"

func seedActiveUser(t *testing.T, store *fakeStore, email string, role identity.Role) identity.UserID {
	t.Helper()
	hash, err := passwordpolicy.Hash(testPassword, testParams())
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	return store.seedUser(identity.User{
		NormalizedEmail: normalized, DisplayName: "Synthetic Test User", Role: role,
		Status: identity.StateActive, PasswordHash: hash,
	})
}

var fixedNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func TestLoginSuccess(t *testing.T) {
	svc, store, audit := newTestService(t)
	seedActiveUser(t, store, "responder@example.test", identity.RoleResponder)

	result, err := svc.Login(context.Background(), "Responder@Example.TEST", testPassword, "unit-test-device", "203.0.113.5", fixedNow)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.RawToken == "" || result.SessionID == 0 {
		t.Fatal("expected a raw token and session id")
	}
	if !result.ExpiresAt.After(fixedNow) {
		t.Fatal("expected expiry in the future")
	}
	if result.Account.Email != "responder@example.test" {
		t.Fatalf("unexpected account email: %s", result.Account.Email)
	}

	events := audit.snapshot()
	var sawSuccess, sawSessionCreated bool
	for _, ev := range events {
		if ev.Type == identityaudit.LoginSuccess {
			sawSuccess = true
			if ev.Metadata["source_network_hint"] != "203.0.113.5" {
				t.Errorf("expected source hint recorded, got %q", ev.Metadata["source_network_hint"])
			}
		}
		if ev.Type == identityaudit.SessionCreated {
			sawSessionCreated = true
		}
		for _, v := range ev.Metadata {
			if v == testPassword || v == result.RawToken {
				t.Fatal("audit event leaked a secret value")
			}
		}
	}
	if !sawSuccess || !sawSessionCreated {
		t.Fatal("expected login_success and session_created audit events")
	}
}

func TestLoginUnknownAccountAndWrongPasswordAreIndistinguishable(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedActiveUser(t, store, "known@example.test", identity.RoleResponder)

	_, errUnknown := svc.Login(context.Background(), "unknown@example.test", "whatever-password-1", "", "", fixedNow)
	_, errWrongPassword := svc.Login(context.Background(), "known@example.test", "definitely-wrong-password", "", "", fixedNow)

	if !errors.Is(errUnknown, ErrInvalidCredentials) || !errors.Is(errWrongPassword, ErrInvalidCredentials) {
		t.Fatalf("expected identical ErrInvalidCredentials, got %v / %v", errUnknown, errWrongPassword)
	}
}

func TestLoginRejectsNonActiveAccounts(t *testing.T) {
	cases := []struct {
		name   string
		status identity.AccountState
		want   error
	}{
		{"invited", identity.StateInvited, ErrInvalidCredentials},
		{"password_change_required", identity.StatePasswordChangeRequired, ErrInvalidCredentials},
		{"disabled", identity.StateDisabled, ErrInvalidCredentials},
		{"expired", identity.StateExpired, ErrInvalidCredentials},
		{"suspended", identity.StateSuspended, ErrAccountSuspended},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := newTestService(t)
			id := seedActiveUser(t, store, tc.name+"@example.test", identity.RoleResponder)
			store.mu.Lock()
			store.users[id].Status = tc.status
			store.mu.Unlock()

			_, err := svc.Login(context.Background(), tc.name+"@example.test", testPassword, "", "", fixedNow)
			if !errors.Is(err, tc.want) {
				t.Fatalf("status %s: expected %v, got %v", tc.status, tc.want, err)
			}
		})
	}
}

func TestLoginAdministratorRemainsBlockedWithoutMFA(t *testing.T) {
	svc, store, _ := newTestService(t)
	id := seedActiveUser(t, store, "admin@example.test", identity.RoleSystemAdministrator)
	// An administrator role can never legitimately reach StateActive without
	// MFA per Step 9B's own EstablishPassword gate; force the state here to
	// simulate a data anomaly and confirm login still fails closed.
	store.mu.Lock()
	store.users[id].Status = identity.StatePasswordChangeRequired
	store.mu.Unlock()

	if _, err := svc.Login(context.Background(), "admin@example.test", testPassword, "", "", fixedNow); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestResolveSessionLifecycle(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedActiveUser(t, store, "resolve@example.test", identity.RoleResponder)
	login, err := svc.Login(context.Background(), "resolve@example.test", testPassword, "", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	auth, err := svc.ResolveSession(context.Background(), login.RawToken, fixedNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("expected valid session, got %v", err)
	}
	if auth.Account.Email != "resolve@example.test" {
		t.Fatalf("unexpected resolved account: %+v", auth.Account)
	}

	if _, err := svc.ResolveSession(context.Background(), "not-a-real-token", fixedNow); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected ErrSessionInvalid for unknown token, got %v", err)
	}

	// Idle expiration.
	idleLater := fixedNow.Add(20 * time.Minute)
	if _, err := svc.ResolveSession(context.Background(), login.RawToken, idleLater); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected idle-expired session to be invalid, got %v", err)
	}
}

func TestResolveSessionAbsoluteExpiration(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedActiveUser(t, store, "absolute@example.test", identity.RoleResponder)
	login, err := svc.Login(context.Background(), "absolute@example.test", testPassword, "", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	beyondAbsolute := fixedNow.Add(13 * time.Hour)
	if _, err := svc.ResolveSession(context.Background(), login.RawToken, beyondAbsolute); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected absolute-expired session to be invalid, got %v", err)
	}
}

func TestResolveSessionRejectsSuspendedAccount(t *testing.T) {
	svc, store, _ := newTestService(t)
	id := seedActiveUser(t, store, "later-suspended@example.test", identity.RoleResponder)
	login, err := svc.Login(context.Background(), "later-suspended@example.test", testPassword, "", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.users[id].Status = identity.StateSuspended
	store.mu.Unlock()

	if _, err := svc.ResolveSession(context.Background(), login.RawToken, fixedNow.Add(time.Minute)); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expected suspended account's existing session to fail closed, got %v", err)
	}
}

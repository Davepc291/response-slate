package identityservice

import (
	"context"
	"errors"
	"testing"

	"greenwich-fire-responder/backend/internal/identity"
)

func loginTestUser(t *testing.T, svc *Service, store *fakeStore, email string, role identity.Role) (identity.UserID, LoginResult) {
	t.Helper()
	id := seedActiveUser(t, store, email, role)
	result, err := svc.Login(context.Background(), email, testPassword, "device-"+email, "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return id, result
}

func TestLogoutRevokesOnlyCurrentSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	userID, first := loginTestUser(t, svc, store, "logout@example.test", identity.RoleResponder)
	second, err := svc.Login(context.Background(), "logout@example.test", testPassword, "second-device", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Logout(context.Background(), userID, first.SessionID, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveSession(context.Background(), first.RawToken, fixedNow); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("expected the logged-out session to be invalid")
	}
	if _, err := svc.ResolveSession(context.Background(), second.RawToken, fixedNow); err != nil {
		t.Fatalf("expected the other session to remain valid, got %v", err)
	}
}

func TestLogoutAllRevokesEverySession(t *testing.T) {
	svc, store, _ := newTestService(t)
	userID, first := loginTestUser(t, svc, store, "logoutall@example.test", identity.RoleResponder)
	second, err := svc.Login(context.Background(), "logoutall@example.test", testPassword, "second-device", "", fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	count, err := svc.LogoutAll(context.Background(), userID, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 sessions revoked, got %d", count)
	}
	for _, tok := range []string{first.RawToken, second.RawToken} {
		if _, err := svc.ResolveSession(context.Background(), tok, fixedNow); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal("expected every session to be revoked")
		}
	}
}

func TestListSessionsReturnsOnlyOwnSessions(t *testing.T) {
	svc, store, _ := newTestService(t)
	userA, _ := loginTestUser(t, svc, store, "usera@example.test", identity.RoleResponder)
	_, _ = loginTestUser(t, svc, store, "userb@example.test", identity.RoleResponder)

	sessions, err := svc.ListSessions(context.Background(), userA)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected exactly one session for user A, got %d", len(sessions))
	}
}

func TestRevokeOwnedSessionRejectsAnotherUsersSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	_, resultA := loginTestUser(t, svc, store, "ownera@example.test", identity.RoleResponder)
	userB, _ := loginTestUser(t, svc, store, "ownerb@example.test", identity.RoleResponder)

	err := svc.RevokeOwnedSession(context.Background(), userB, resultA.SessionID, fixedNow)
	if !errors.Is(err, ErrSessionNotOwned) {
		t.Fatalf("expected ErrSessionNotOwned, got %v", err)
	}
	// The session must remain valid: the rejected attempt had no effect.
	if _, err := svc.ResolveSession(context.Background(), resultA.RawToken, fixedNow); err != nil {
		t.Fatalf("expected user A's session to remain valid, got %v", err)
	}
}

func TestRevokeOwnedSessionSucceedsForOwner(t *testing.T) {
	svc, store, _ := newTestService(t)
	userID, result := loginTestUser(t, svc, store, "owner@example.test", identity.RoleResponder)

	if err := svc.RevokeOwnedSession(context.Background(), userID, result.SessionID, fixedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveSession(context.Background(), result.RawToken, fixedNow); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("expected the owner's own revoked session to be invalid")
	}
}

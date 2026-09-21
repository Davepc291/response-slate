package adminservice

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/authorization"
	"greenwich-fire-responder/backend/internal/identity"
)

var fixedNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func testConfig() Config { return Config{InvitationTTL: time.Hour} }

func newTestService() (*Service, *fakeStore, *fakeAudit) {
	store := newFakeStore()
	audit := &fakeAudit{}
	svc := New(store, audit, testConfig(), nil)
	return svc, store, audit
}

func systemAdmin(id identity.UserID) authorization.Actor {
	return authorization.Actor{UserID: id, Role: identity.RoleSystemAdministrator}
}

func deptAdmin(id identity.UserID, scope identity.Scope) authorization.Actor {
	return authorization.Actor{UserID: id, Role: identity.RoleDepartmentAdministrator, Scope: scope}
}

func responder(id identity.UserID, scope identity.Scope) authorization.Actor {
	return authorization.Actor{UserID: id, Role: identity.RoleResponder, Scope: scope}
}

// --- ListUsers ---------------------------------------------------------

func TestListUsersDefaultDenyForNonAdministrator(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.ListUsers(context.Background(), responder(1, "engine-1"), ListFilter{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestListUsersUnknownRoleDenies(t *testing.T) {
	svc, _, _ := newTestService()
	actor := authorization.Actor{UserID: 1, Role: "bogus_role"}
	if _, err := svc.ListUsers(context.Background(), actor, ListFilter{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for unknown role, got %v", err)
	}
}

func TestListUsersSystemAdministratorSeesEveryScope(t *testing.T) {
	svc, store, _ := newTestService()
	store.seedUser(identity.User{NormalizedEmail: "a@example.test", DisplayName: "A", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateActive})
	store.seedUser(identity.User{NormalizedEmail: "b@example.test", DisplayName: "B", Role: identity.RoleResponder, Scope: "engine-2", Status: identity.StateActive})

	views, err := svc.ListUsers(context.Background(), systemAdmin(99), ListFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 users visible to a system administrator, got %d", len(views))
	}
}

func TestListUsersDepartmentAdministratorPinnedToOwnScope(t *testing.T) {
	svc, store, _ := newTestService()
	store.seedUser(identity.User{NormalizedEmail: "a@example.test", DisplayName: "A", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateActive})
	store.seedUser(identity.User{NormalizedEmail: "b@example.test", DisplayName: "B", Role: identity.RoleResponder, Scope: "engine-2", Status: identity.StateActive})

	views, err := svc.ListUsers(context.Background(), deptAdmin(99, "engine-1"), ListFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(views) != 1 || views[0].Scope != "engine-1" {
		t.Fatalf("expected only engine-1 users visible, got %+v", views)
	}
}

func TestListUsersDepartmentAdministratorCannotWidenToAnotherScope(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.ListUsers(context.Background(), deptAdmin(99, "engine-1"), ListFilter{Scope: "engine-2"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden when a department administrator requests another scope, got %v", err)
	}
}

func TestListUsersRejectsUnknownFilterValues(t *testing.T) {
	svc, _, _ := newTestService()
	if _, err := svc.ListUsers(context.Background(), systemAdmin(9999), ListFilter{Role: "not_a_role"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unknown role filter, got %v", err)
	}
	if _, err := svc.ListUsers(context.Background(), systemAdmin(9999), ListFilter{Status: "not_a_status"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unknown status filter, got %v", err)
	}
}

// --- GetUser -------------------------------------------------------------

func TestGetUserOutOfScopeIsNotFoundNotForbidden(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "a@example.test", DisplayName: "A", Role: identity.RoleResponder, Scope: "engine-2", Status: identity.StateActive})

	_, err := svc.GetUser(context.Background(), deptAdmin(99, "engine-1"), target)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound (never ErrForbidden) for an out-of-scope target, got %v", err)
	}
}

func TestGetUserUnknownIDIsNotFound(t *testing.T) {
	svc, _, _ := newTestService()
	if _, err := svc.GetUser(context.Background(), systemAdmin(9999), 12345); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- CreateUser ------------------------------------------------------------

func TestCreateUserSystemAdministratorCanCreateAdministrator(t *testing.T) {
	svc, _, audit := newTestService()
	result, err := svc.CreateUser(context.Background(), systemAdmin(9999), CreateUserParams{
		Email: "New.Admin@Example.test", DisplayName: "New Admin", Role: identity.RoleDepartmentAdministrator, Scope: "engine-1",
	}, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.View.Email != "new.admin@example.test" {
		t.Fatalf("expected normalized email, got %q", result.View.Email)
	}
	if result.RawInvitationCode == "" {
		t.Fatal("expected a non-empty raw invitation code")
	}
	if !result.InvitationExpiresAt.Equal(fixedNow.Add(time.Hour)) {
		t.Fatalf("unexpected expiry: %v", result.InvitationExpiresAt)
	}

	events := audit.snapshot()
	if len(events) != 1 || string(events[0].Type) != "invitation_created" {
		t.Fatalf("expected exactly one invitation_created audit event, got %+v", events)
	}
	for _, v := range events[0].Metadata {
		if strings.Contains(v, result.RawInvitationCode) {
			t.Fatal("audit metadata must never contain the raw invitation code")
		}
	}
}

func TestCreateUserDepartmentAdministratorCannotCreateSystemAdministrator(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.CreateUser(context.Background(), deptAdmin(9999, "engine-1"), CreateUserParams{
		Email: "x@example.test", DisplayName: "X", Role: identity.RoleSystemAdministrator, Scope: "",
	}, fixedNow)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestCreateUserDepartmentAdministratorCannotCreateOutsideOwnScope(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.CreateUser(context.Background(), deptAdmin(9999, "engine-1"), CreateUserParams{
		Email: "x@example.test", DisplayName: "X", Role: identity.RoleResponder, Scope: "engine-2",
	}, fixedNow)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestCreateUserDepartmentAdministratorCanCreateWithinOwnScope(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.CreateUser(context.Background(), deptAdmin(9999, "engine-1"), CreateUserParams{
		Email: "x@example.test", DisplayName: "X", Role: identity.RoleResponder, Scope: "engine-1",
	}, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateUserNonAdministratorForbidden(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.CreateUser(context.Background(), responder(1, "engine-1"), CreateUserParams{
		Email: "x@example.test", DisplayName: "X", Role: identity.RoleResponder, Scope: "engine-1",
	}, fixedNow)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestCreateUserDuplicateEmailConflict(t *testing.T) {
	svc, store, _ := newTestService()
	store.seedUser(identity.User{NormalizedEmail: "dup@example.test", DisplayName: "Dup", Role: identity.RoleResponder, Status: identity.StateActive})
	_, err := svc.CreateUser(context.Background(), systemAdmin(9999), CreateUserParams{
		Email: "dup@example.test", DisplayName: "Dup2", Role: identity.RoleResponder,
	}, fixedNow)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestCreateUserValidatesInput(t *testing.T) {
	svc, _, _ := newTestService()
	cases := []CreateUserParams{
		{Email: "not-an-email", DisplayName: "X", Role: identity.RoleResponder},
		{Email: "x@example.test", DisplayName: "", Role: identity.RoleResponder},
		{Email: "x@example.test", DisplayName: "X", Role: "not_a_role"},
		{Email: "x@example.test", DisplayName: "X", Role: identity.RoleResponder, Scope: " leading-space"},
	}
	for _, c := range cases {
		if _, err := svc.CreateUser(context.Background(), systemAdmin(9999), c, fixedNow); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("params %+v: expected ErrInvalidInput, got %v", c, err)
		}
	}
}

// --- ResendInvitation --------------------------------------------------

func TestResendInvitationInvalidatesPrior(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "inv@example.test", DisplayName: "Inv", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateInvited})
	store.invitations["seed"] = &fakeInvitation{userID: target, status: "pending", expiresAt: fixedNow.Add(time.Hour)}

	result, err := svc.ResendInvitation(context.Background(), systemAdmin(9999), target, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RawInvitationCode == "" {
		t.Fatal("expected a non-empty raw invitation code")
	}
	if n := store.pendingInvitationCount(target); n != 1 {
		t.Fatalf("expected exactly one pending invitation after resend, got %d", n)
	}
}

func TestResendInvitationWrongStateRejected(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "active@example.test", DisplayName: "Active", Role: identity.RoleResponder, Status: identity.StateActive})
	if _, err := svc.ResendInvitation(context.Background(), systemAdmin(9999), target, fixedNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestResendInvitationDepartmentAdministratorOutOfScopeIsNotFound(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "inv2@example.test", DisplayName: "Inv2", Role: identity.RoleResponder, Scope: "engine-2", Status: identity.StateInvited})
	if _, err := svc.ResendInvitation(context.Background(), deptAdmin(9999, "engine-1"), target, fixedNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- Suspend/Disable -----------------------------------------------------

func TestSuspendRevokesSessionsAndAudits(t *testing.T) {
	svc, store, audit := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "s@example.test", DisplayName: "S", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateActive})
	store.seedSessions(target, 3)

	if err := svc.Suspend(context.Background(), systemAdmin(9999), target, fixedNow); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.activeSessionCount(target) != 0 {
		t.Fatal("expected every session revoked on suspend")
	}
	u, _ := store.GetByID(context.Background(), target)
	if u.Status != identity.StateSuspended {
		t.Fatalf("expected suspended status, got %s", u.Status)
	}
	events := audit.snapshot()
	if len(events) != 1 || string(events[0].Type) != "account_state_change" {
		t.Fatalf("expected one account_state_change event, got %+v", events)
	}
}

func TestSuspendSelfForbidden(t *testing.T) {
	svc, store, _ := newTestService()
	self := store.seedUser(identity.User{NormalizedEmail: "self@example.test", DisplayName: "Self", Role: identity.RoleSystemAdministrator, Status: identity.StateActive})
	if err := svc.Suspend(context.Background(), systemAdmin(self), self, fixedNow); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("expected ErrSelfAction, got %v", err)
	}
}

func TestDisableSelfForbidden(t *testing.T) {
	svc, store, _ := newTestService()
	self := store.seedUser(identity.User{NormalizedEmail: "self2@example.test", DisplayName: "Self2", Role: identity.RoleSystemAdministrator, Status: identity.StateActive})
	if err := svc.Disable(context.Background(), systemAdmin(self), self, fixedNow); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("expected ErrSelfAction, got %v", err)
	}
}

func TestSuspendDepartmentAdministratorCannotActOutsideScope(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "t@example.test", DisplayName: "T", Role: identity.RoleResponder, Scope: "engine-2", Status: identity.StateActive})
	if err := svc.Suspend(context.Background(), deptAdmin(9999, "engine-1"), target, fixedNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for an out-of-scope target, got %v", err)
	}
}

// --- Restore ---------------------------------------------------------------

func TestRestoreReturnsToActiveWhenPasswordAlreadyEstablished(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "r@example.test", DisplayName: "R", Role: identity.RoleResponder, Status: identity.StateSuspended, PasswordHash: "hash"})
	state, err := svc.Restore(context.Background(), systemAdmin(9999), target, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != identity.StateActive {
		t.Fatalf("expected active, got %s", state)
	}
}

func TestRestoreReturnsToInvitedWhenPendingInvitationStillValid(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "r2@example.test", DisplayName: "R2", Role: identity.RoleResponder, Status: identity.StateSuspended})
	store.invitations["k"] = &fakeInvitation{userID: target, status: "pending", expiresAt: fixedNow.Add(time.Hour)}
	state, err := svc.Restore(context.Background(), systemAdmin(9999), target, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != identity.StateInvited {
		t.Fatalf("expected invited, got %s", state)
	}
}

func TestRestoreRejectsNonSuspendedNonDisabled(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "r3@example.test", DisplayName: "R3", Role: identity.RoleResponder, Status: identity.StateActive})
	if _, err := svc.Restore(context.Background(), systemAdmin(9999), target, fixedNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

// --- RevokeSessions ----------------------------------------------------

func TestRevokeSessionsDoesNotChangeStatus(t *testing.T) {
	svc, store, audit := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "rv@example.test", DisplayName: "RV", Role: identity.RoleResponder, Status: identity.StateActive})
	store.seedSessions(target, 2)

	count, err := svc.RevokeSessions(context.Background(), systemAdmin(9999), target, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 revoked sessions, got %d", count)
	}
	u, _ := store.GetByID(context.Background(), target)
	if u.Status != identity.StateActive {
		t.Fatalf("expected status unchanged, got %s", u.Status)
	}
	events := audit.snapshot()
	if len(events) != 1 || string(events[0].Type) != "administrative_action" || events[0].Metadata["action"] != "revoke_sessions" {
		t.Fatalf("expected one administrative_action(revoke_sessions) event, got %+v", events)
	}
}

func TestRevokeSessionsSelfForbidden(t *testing.T) {
	svc, store, _ := newTestService()
	self := store.seedUser(identity.User{NormalizedEmail: "self3@example.test", DisplayName: "Self3", Role: identity.RoleSystemAdministrator, Status: identity.StateActive})
	if _, err := svc.RevokeSessions(context.Background(), systemAdmin(self), self, fixedNow); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("expected ErrSelfAction, got %v", err)
	}
}

// --- AdminReset --------------------------------------------------------

func TestAdminResetClearsPasswordAndRevokesSessions(t *testing.T) {
	svc, store, audit := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "ar@example.test", DisplayName: "AR", Role: identity.RoleResponder, Status: identity.StateActive, PasswordHash: "hash"})
	store.seedSessions(target, 4)

	result, err := svc.AdminReset(context.Background(), systemAdmin(9999), target, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RawInvitationCode == "" {
		t.Fatal("expected a non-empty raw credential code")
	}
	u, _ := store.GetByID(context.Background(), target)
	if u.Status != identity.StatePasswordChangeRequired || u.PasswordHash != "" {
		t.Fatalf("expected password cleared and status password_change_required, got %+v", u)
	}
	if store.activeSessionCount(target) != 0 {
		t.Fatal("expected every session revoked on administrator reset")
	}
	events := audit.snapshot()
	if len(events) != 1 || events[0].Metadata["action"] != "admin_reset" {
		t.Fatalf("expected one admin_reset audit event, got %+v", events)
	}
}

func TestAdminResetSelfForbidden(t *testing.T) {
	svc, store, _ := newTestService()
	self := store.seedUser(identity.User{NormalizedEmail: "self4@example.test", DisplayName: "Self4", Role: identity.RoleSystemAdministrator, Status: identity.StateActive})
	if _, err := svc.AdminReset(context.Background(), systemAdmin(self), self, fixedNow); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("expected ErrSelfAction, got %v", err)
	}
}

// --- ChangeRole ----------------------------------------------------------

func TestChangeRoleSelfForbidden(t *testing.T) {
	svc, store, _ := newTestService()
	self := store.seedUser(identity.User{NormalizedEmail: "self5@example.test", DisplayName: "Self5", Role: identity.RoleSystemAdministrator, Status: identity.StateActive})
	if _, err := svc.ChangeRole(context.Background(), systemAdmin(self), self, identity.RoleResponder, "", fixedNow); !errors.Is(err, ErrSelfAction) {
		t.Fatalf("expected ErrSelfAction, got %v", err)
	}
}

func TestChangeRoleDepartmentAdministratorCannotPromoteToSystemAdministrator(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "cr@example.test", DisplayName: "CR", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateActive})
	if _, err := svc.ChangeRole(context.Background(), deptAdmin(9999, "engine-1"), target, identity.RoleSystemAdministrator, "", fixedNow); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestChangeRoleDepartmentAdministratorCannotMoveUserToAnotherScope(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "cr2@example.test", DisplayName: "CR2", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateActive})
	if _, err := svc.ChangeRole(context.Background(), deptAdmin(9999, "engine-1"), target, identity.RoleResponder, "engine-2", fixedNow); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestChangeRoleSuccessAudited(t *testing.T) {
	svc, store, audit := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "cr3@example.test", DisplayName: "CR3", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateActive})
	prior, err := svc.ChangeRole(context.Background(), systemAdmin(9999), target, identity.RoleDispatcherOperator, "engine-1", fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prior.Role != identity.RoleResponder {
		t.Fatalf("expected prior role responder, got %s", prior.Role)
	}
	events := audit.snapshot()
	if len(events) != 1 || string(events[0].Type) != "role_or_scope_change" {
		t.Fatalf("expected one role_or_scope_change event, got %+v", events)
	}
	if events[0].Metadata["prior_role"] != "responder" || events[0].Metadata["new_role"] != "dispatcher_operator" {
		t.Fatalf("unexpected role change metadata: %+v", events[0].Metadata)
	}
}

func TestChangeRoleUnknownNewRoleRejected(t *testing.T) {
	svc, store, _ := newTestService()
	target := store.seedUser(identity.User{NormalizedEmail: "cr4@example.test", DisplayName: "CR4", Role: identity.RoleResponder, Scope: "engine-1", Status: identity.StateActive})
	if _, err := svc.ChangeRole(context.Background(), systemAdmin(9999), target, "not_a_role", "engine-1", fixedNow); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

// --- Construction ---------------------------------------------------------

func TestNewPanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for non-positive InvitationTTL")
		}
	}()
	New(newFakeStore(), &fakeAudit{}, Config{}, nil)
}

func TestNewPanicsOnNilStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil store")
		}
	}()
	New(nil, &fakeAudit{}, testConfig(), nil)
}

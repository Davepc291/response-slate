package authhttp

// Extends fakeStore (fake_store_test.go) with the adminservice.Store
// methods, so the same in-memory fake backs both the Step 9C
// identityservice.Store and the Step 9E adminservice.Store in this
// package's HTTP-transport-level tests. This file tests only routing,
// session/CSRF enforcement, and response shape — adminservice's own
// authorization/orchestration logic has its own thorough unit tests in
// backend/internal/adminservice.

import (
	"context"
	"sort"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/session"
)

func (f *fakeStore) CreateUserAndInvite(_ context.Context, params identitystore.NewUserParams) (identity.UserID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.NormalizedEmail == params.Email {
			return 0, identitystore.ErrConflict
		}
	}
	f.nextUserID++
	id := identity.UserID(f.nextUserID)
	f.users[id] = &identity.User{
		ID: id, NormalizedEmail: params.Email, DisplayName: params.DisplayName,
		Role: params.Role, Scope: params.Scope, Status: identity.StateInvited,
		CreatedByID: params.CreatedBy,
	}
	f.invitations[digestKey(params.InvitationTokenDigest)] = &fakeInvitation{
		userID: id, status: "pending", expiresAt: params.InvitationExpiresAt,
	}
	return id, nil
}

func (f *fakeStore) ListUsers(_ context.Context, filter identitystore.ListUsersFilter) ([]identity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []identity.User
	for _, u := range f.users {
		if filter.Scope != nil && u.Scope != *filter.Scope {
			continue
		}
		if filter.Role != nil && u.Role != *filter.Role {
			continue
		}
		if filter.Status != nil && u.Status != *filter.Status {
			continue
		}
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) revokeAllSessions(userID identity.UserID, now time.Time, reason session.RevocationReason) int64 {
	var count int64
	for _, s := range f.sessions {
		if identity.UserID(s.UserID) == userID && s.RevokedAt == nil {
			t := now
			s.RevokedAt = &t
			s.RevocationReason = reason
			count++
		}
	}
	return count
}

func (f *fakeStore) adminTransition(userID identity.UserID, to identity.AccountState, now time.Time, reason session.RevocationReason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return identitystore.ErrNotFound
	}
	if !identity.AllowedTransition(u.Status, to) {
		return identitystore.ErrInvalidTransition
	}
	u.Status = to
	f.revokeAllSessions(userID, now, reason)
	return nil
}

func (f *fakeStore) Suspend(_ context.Context, userID identity.UserID, now time.Time) error {
	return f.adminTransition(userID, identity.StateSuspended, now, session.RevokedAccountSuspended)
}

func (f *fakeStore) Disable(_ context.Context, userID identity.UserID, now time.Time) error {
	return f.adminTransition(userID, identity.StateDisabled, now, session.RevokedAccountDisabled)
}

func (f *fakeStore) Restore(_ context.Context, userID identity.UserID, now time.Time) (identity.AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return "", identitystore.ErrNotFound
	}
	if u.Status != identity.StateSuspended && u.Status != identity.StateDisabled {
		return "", identitystore.ErrInvalidTransition
	}
	target := identity.StateExpired
	if u.HasPassword() {
		target = identity.StateActive
	} else {
		for _, inv := range f.invitations {
			if inv.userID == userID && inv.status == "pending" && now.Before(inv.expiresAt) {
				target = identity.StateInvited
			}
		}
	}
	u.Status = target
	return target, nil
}

func (f *fakeStore) Revoke(_ context.Context, userID identity.UserID, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[userID]; !ok {
		return 0, identitystore.ErrNotFound
	}
	return f.revokeAllSessions(userID, now, session.RevokedAdminRevoke), nil
}

func (f *fakeStore) ChangeRole(_ context.Context, params identitystore.ChangeRoleParams) (identitystore.PriorRoleScope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[params.UserID]
	if !ok {
		return identitystore.PriorRoleScope{}, identitystore.ErrNotFound
	}
	prior := identitystore.PriorRoleScope{Role: u.Role, Scope: u.Scope}
	u.Role = params.NewRole
	u.Scope = params.NewScope
	return prior, nil
}

func (f *fakeStore) ResendInvitation(_ context.Context, userID, _ identity.UserID, newTokenDigest []byte, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return identitystore.ErrNotFound
	}
	if u.Status != identity.StateInvited && u.Status != identity.StateExpired {
		return identitystore.ErrInvalidTransition
	}
	for _, inv := range f.invitations {
		if inv.userID == userID && inv.status == "pending" {
			inv.status = "superseded"
		}
	}
	f.invitations[digestKey(newTokenDigest)] = &fakeInvitation{userID: userID, status: "pending", expiresAt: expiresAt}
	u.Status = identity.StateInvited
	return nil
}

func (f *fakeStore) AdminReset(_ context.Context, userID, _ identity.UserID, newTokenDigest []byte, expiresAt, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[userID]
	if !ok {
		return identitystore.ErrNotFound
	}
	switch u.Status {
	case identity.StateActive, identity.StateSuspended, identity.StatePasswordChangeRequired:
	default:
		return identitystore.ErrInvalidTransition
	}
	for _, inv := range f.invitations {
		if inv.userID == userID && inv.status == "pending" {
			inv.status = "superseded"
		}
	}
	f.invitations[digestKey(newTokenDigest)] = &fakeInvitation{userID: userID, status: "pending", expiresAt: expiresAt}
	u.Status = identity.StatePasswordChangeRequired
	u.PasswordHash = ""
	f.revokeAllSessions(userID, now, session.RevokedPasswordReset)
	return nil
}

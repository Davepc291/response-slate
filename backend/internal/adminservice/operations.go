package adminservice

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/authorization"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
)

// ListFilter carries the caller-facing list parameters. Scope is only ever
// honored for a system administrator caller (see resolveManageScope); a
// department administrator's own scope always applies regardless of this
// field.
type ListFilter struct {
	Scope  identity.Scope
	Role   identity.Role
	Status identity.AccountState
	Search string
	Limit  int
	Offset int
}

// ListUsers returns every account visible to actor under filter (Section 7:
// "Users (list)... Search/filter by name, email, role, status"). A
// department administrator always sees only their own scope, regardless of
// filter.Scope; a system administrator may optionally narrow to one scope.
func (s *Service) ListUsers(ctx context.Context, actor authorization.Actor, filter ListFilter) ([]identity.View, error) {
	scope, err := resolveManageScope(actor, filter.Scope)
	if err != nil {
		return nil, err
	}
	storeFilter := identitystore.ListUsersFilter{
		Search: filter.Search,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}
	if scope != "" {
		storeFilter.Scope = &scope
	}
	if filter.Role != "" {
		if !filter.Role.Valid() {
			return nil, ErrInvalidInput
		}
		storeFilter.Role = &filter.Role
	}
	if filter.Status != "" {
		if !filter.Status.Valid() {
			return nil, ErrInvalidInput
		}
		storeFilter.Status = &filter.Status
	}

	users, err := s.store.ListUsers(ctx, storeFilter)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]identity.View, 0, len(users))
	for _, u := range users {
		out = append(out, u.Public())
	}
	return out, nil
}

// GetUser returns one account visible to actor (Section 7: "User detail").
func (s *Service) GetUser(ctx context.Context, actor authorization.Actor, targetID identity.UserID) (identity.View, error) {
	user, err := s.loadVisible(ctx, actor, targetID)
	if err != nil {
		return identity.View{}, err
	}
	return user.Public(), nil
}

// CreateUserParams carries Section 7's approved minimum information for a
// new account: no password field exists here, matching "the system
// generates the invitation, never an administrator-chosen password."
type CreateUserParams struct {
	Email       string
	DisplayName string
	Role        identity.Role
	Scope       identity.Scope
}

// CreateUserResult carries the one-time raw invitation code the HTTP layer
// must render exactly once and never again, plus the safe view of the
// account just created (built from already-known, just-validated fields —
// no extra store round trip).
type CreateUserResult struct {
	View                identity.View
	RawInvitationCode   string
	InvitationExpiresAt time.Time
}

// CreateUser creates a new account in the invited state and issues its
// first invitation, atomically (Section 7, steps 3-5). Only an actor who
// may assign the requested role within the requested scope
// (authorization.CanAssignRole) may create it; a department administrator
// can never create a system_administrator or an account outside their own
// scope, and an unknown role/scope always denies.
func (s *Service) CreateUser(ctx context.Context, actor authorization.Actor, params CreateUserParams, now time.Time) (CreateUserResult, error) {
	if !actor.Role.IsAdministrator() {
		return CreateUserResult{}, ErrForbidden
	}
	if err := identity.ValidateRole(params.Role); err != nil {
		return CreateUserResult{}, ErrInvalidInput
	}
	if err := identity.ValidateScope(params.Scope); err != nil {
		return CreateUserResult{}, ErrInvalidInput
	}
	if err := identity.ValidateDisplayName(params.DisplayName); err != nil {
		return CreateUserResult{}, ErrInvalidInput
	}
	normalizedEmail, err := identity.NormalizeEmail(params.Email)
	if err != nil {
		return CreateUserResult{}, ErrInvalidInput
	}
	if !authorization.CanAssignRole(actor, params.Scope, params.Role) {
		return CreateUserResult{}, ErrForbidden
	}

	rawToken, digest, expiresAt, err := s.newInvitationToken(now)
	if err != nil {
		return CreateUserResult{}, err
	}

	userID, err := s.store.CreateUserAndInvite(ctx, identitystore.NewUserParams{
		Email:                 normalizedEmail,
		DisplayName:           params.DisplayName,
		Role:                  params.Role,
		Scope:                 params.Scope,
		CreatedBy:             actor.UserID,
		InvitationTokenDigest: digest,
		InvitationExpiresAt:   expiresAt,
	})
	if err != nil {
		return CreateUserResult{}, mapStoreErr(err)
	}

	s.recordAudit(ctx, identityaudit.InvitationCreated, userID, actor.UserID, "", identityaudit.Metadata{
		"role": string(params.Role),
	}, now)

	view := identity.View{
		ID: userID, Email: normalizedEmail, DisplayName: params.DisplayName,
		Role: params.Role, Scope: params.Scope, Status: identity.StateInvited,
	}
	return CreateUserResult{View: view, RawInvitationCode: rawToken, InvitationExpiresAt: expiresAt}, nil
}

// ResendInvitationResult carries the one-time raw invitation code.
type ResendInvitationResult struct {
	RawInvitationCode   string
	InvitationExpiresAt time.Time
}

// ResendInvitation invalidates any prior invitation for targetID and issues
// a fresh one (Section 2: "Resend"). Only permitted for an account
// currently invited or expired.
func (s *Service) ResendInvitation(ctx context.Context, actor authorization.Actor, targetID identity.UserID, now time.Time) (ResendInvitationResult, error) {
	target, err := s.loadVisible(ctx, actor, targetID)
	if err != nil {
		return ResendInvitationResult{}, err
	}
	if !authorization.Allowed(actor, authorization.ManageInvitationsResets, target.Scope, target.ID) {
		return ResendInvitationResult{}, ErrForbidden
	}

	rawToken, digest, expiresAt, err := s.newInvitationToken(now)
	if err != nil {
		return ResendInvitationResult{}, err
	}
	if err := s.store.ResendInvitation(ctx, targetID, actor.UserID, digest, expiresAt); err != nil {
		return ResendInvitationResult{}, mapStoreErr(err)
	}

	s.recordAudit(ctx, identityaudit.InvitationCreated, targetID, actor.UserID, "resend", identityaudit.Metadata{
		"role": string(target.Role),
	}, now)

	return ResendInvitationResult{RawInvitationCode: rawToken, InvitationExpiresAt: expiresAt}, nil
}

// Suspend temporarily blocks authentication for targetID (Section 7). An
// administrator can never suspend their own account (ErrSelfAction).
func (s *Service) Suspend(ctx context.Context, actor authorization.Actor, targetID identity.UserID, now time.Time) error {
	return s.transition(ctx, actor, targetID, now, func(target identity.User) error {
		return s.store.Suspend(ctx, targetID, now)
	}, "suspended")
}

// Disable permanently blocks authentication for targetID (Section 7). An
// administrator can never disable their own account (ErrSelfAction).
func (s *Service) Disable(ctx context.Context, actor authorization.Actor, targetID identity.UserID, now time.Time) error {
	return s.transition(ctx, actor, targetID, now, func(target identity.User) error {
		return s.store.Disable(ctx, targetID, now)
	}, "disabled")
}

// transition is the shared Suspend/Disable implementation: both require
// SuspendDisableRestore permission, both forbid a self-target, and both
// record an identical AccountStateChange audit shape.
func (s *Service) transition(ctx context.Context, actor authorization.Actor, targetID identity.UserID, now time.Time, apply func(identity.User) error, newStatusLabel string) error {
	target, err := s.loadVisible(ctx, actor, targetID)
	if err != nil {
		return err
	}
	if targetID == actor.UserID {
		return ErrSelfAction
	}
	if !authorization.Allowed(actor, authorization.SuspendDisableRestore, target.Scope, target.ID) {
		return ErrForbidden
	}
	if err := apply(target); err != nil {
		return mapStoreErr(err)
	}
	s.recordAudit(ctx, identityaudit.AccountStateChange, targetID, actor.UserID, "", identityaudit.Metadata{
		"prior_status": string(target.Status),
		"new_status":   newStatusLabel,
	}, now)
	return nil
}

// Restore returns a suspended or disabled account to active/invited/expired
// handling (Section 7). Restoring an administrator's own account is
// harmless (it can only ever be reached while that account is not the
// caller of this request) and is not blocked.
func (s *Service) Restore(ctx context.Context, actor authorization.Actor, targetID identity.UserID, now time.Time) (identity.AccountState, error) {
	target, err := s.loadVisible(ctx, actor, targetID)
	if err != nil {
		return "", err
	}
	if !authorization.Allowed(actor, authorization.SuspendDisableRestore, target.Scope, target.ID) {
		return "", ErrForbidden
	}
	newStatus, err := s.store.Restore(ctx, targetID, now)
	if err != nil {
		return "", mapStoreErr(err)
	}
	s.recordAudit(ctx, identityaudit.AccountStateChange, targetID, actor.UserID, "", identityaudit.Metadata{
		"prior_status": string(target.Status),
		"new_status":   string(newStatus),
	}, now)
	return newStatus, nil
}

// RevokeSessions immediately invalidates every session for targetID without
// changing its enabled/disabled state (Section 7: "Revoke"). An
// administrator can never revoke their own sessions through this action
// (ErrSelfAction): doing so mid-request would immediately invalidate the
// very session making the request.
func (s *Service) RevokeSessions(ctx context.Context, actor authorization.Actor, targetID identity.UserID, now time.Time) (int64, error) {
	target, err := s.loadVisible(ctx, actor, targetID)
	if err != nil {
		return 0, err
	}
	if targetID == actor.UserID {
		return 0, ErrSelfAction
	}
	if !authorization.Allowed(actor, authorization.SuspendDisableRestore, target.Scope, target.ID) {
		return 0, ErrForbidden
	}
	count, err := s.store.Revoke(ctx, targetID, now)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	s.recordAudit(ctx, identityaudit.AdministrativeAction, targetID, actor.UserID, "", identityaudit.Metadata{
		"action": "revoke_sessions",
	}, now)
	return count, nil
}

// AdminResetResult carries the one-time raw credential/invitation code.
type AdminResetResult struct {
	RawInvitationCode   string
	InvitationExpiresAt time.Time
}

// AdminReset performs Section 2's administrator "Reset" action: revokes
// every session, clears the permanent password, and issues a fresh
// single-use credential. An administrator can never reset their own account
// (ErrSelfAction): it would immediately sign the caller out mid-request.
func (s *Service) AdminReset(ctx context.Context, actor authorization.Actor, targetID identity.UserID, now time.Time) (AdminResetResult, error) {
	target, err := s.loadVisible(ctx, actor, targetID)
	if err != nil {
		return AdminResetResult{}, err
	}
	if targetID == actor.UserID {
		return AdminResetResult{}, ErrSelfAction
	}
	if !authorization.Allowed(actor, authorization.ManageInvitationsResets, target.Scope, target.ID) {
		return AdminResetResult{}, ErrForbidden
	}

	rawToken, digest, expiresAt, err := s.newInvitationToken(now)
	if err != nil {
		return AdminResetResult{}, err
	}
	if err := s.store.AdminReset(ctx, targetID, actor.UserID, digest, expiresAt, now); err != nil {
		return AdminResetResult{}, mapStoreErr(err)
	}

	s.recordAudit(ctx, identityaudit.AdministrativeAction, targetID, actor.UserID, "", identityaudit.Metadata{
		"action": "admin_reset",
	}, now)

	return AdminResetResult{RawInvitationCode: rawToken, InvitationExpiresAt: expiresAt}, nil
}

// ChangeRole changes targetID's role and/or scope (Section 7, Section 10:
// "Role or scope change"). Only an actor who may assign newRole within
// newScope may perform this (authorization.CanAssignRole); an administrator
// can never change their own role/scope through this action (ErrSelfAction),
// preventing accidental or malicious self-demotion/self-lockout.
func (s *Service) ChangeRole(ctx context.Context, actor authorization.Actor, targetID identity.UserID, newRole identity.Role, newScope identity.Scope, now time.Time) (identitystore.PriorRoleScope, error) {
	target, err := s.loadVisible(ctx, actor, targetID)
	if err != nil {
		return identitystore.PriorRoleScope{}, err
	}
	if targetID == actor.UserID {
		return identitystore.PriorRoleScope{}, ErrSelfAction
	}
	if !authorization.Allowed(actor, authorization.ChangeRoles, target.Scope, target.ID) {
		return identitystore.PriorRoleScope{}, ErrForbidden
	}
	if err := identity.ValidateRole(newRole); err != nil {
		return identitystore.PriorRoleScope{}, ErrInvalidInput
	}
	if err := identity.ValidateScope(newScope); err != nil {
		return identitystore.PriorRoleScope{}, ErrInvalidInput
	}
	if !authorization.CanAssignRole(actor, newScope, newRole) {
		return identitystore.PriorRoleScope{}, ErrForbidden
	}

	prior, err := s.store.ChangeRole(ctx, identitystore.ChangeRoleParams{
		UserID: targetID, NewRole: newRole, NewScope: newScope,
	})
	if err != nil {
		return identitystore.PriorRoleScope{}, mapStoreErr(err)
	}

	s.recordAudit(ctx, identityaudit.RoleOrScopeChange, targetID, actor.UserID, "", identityaudit.Metadata{
		"prior_role":  string(prior.Role),
		"new_role":    string(newRole),
		"prior_scope": string(prior.Scope),
		"new_scope":   string(newScope),
	}, now)

	return prior, nil
}

// Package adminservice is the Step 9E administrator user-management service
// layer: it coordinates the existing, approved Step 9B/9C packages
// (identity, authorization, invitation, session, identitystore,
// identityaudit) into the administrator operations
// docs/authentication-authorization-v1.md Section 7 requires — list/view
// users within scope, create a user and issue an invitation, resend/reissue
// an invitation, suspend/disable/restore an account, revoke sessions,
// perform an administrator reset, and change an eligible user's role/scope
// — without duplicating any of those packages' own security logic. It has
// no HTTP handler, no cookie, and no route: see backend/internal/authhttp
// for the transport layer built on top of this package, mirroring
// identityservice's own architecture exactly.
//
// Every method takes an explicit now time.Time parameter and an
// authorization.Actor built by the HTTP layer from the caller's resolved
// session principal, matching this repository's existing caller-supplied
// time and "backend authorization, never Angular visibility" conventions.
// Every method re-checks authorization.Allowed/CanAssignRole itself: no
// operation trusts that the HTTP layer, or any future caller, already did
// so.
package adminservice

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"greenwich-fire-responder/backend/internal/authorization"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/invitation"
)

// Generic, externally-visible sentinel errors. None of these, nor any
// wrapped error this package returns to an HTTP caller, ever includes a
// password, raw token, cookie value, hash, or other sensitive account
// detail, matching identityservice's identical convention.
var (
	// ErrForbidden marks any authorization denial: an unknown/non-admin
	// role, a permission the matrix does not grant, a role/scope this actor
	// may not assign, or an unsafe self-target action. It never distinguishes
	// which of these applied.
	ErrForbidden = errors.New("adminservice: not authorized to perform this action")
	// ErrNotFound covers both "no such account" and "account exists but is
	// outside the caller's authorized scope," deliberately never
	// distinguished (Section 6/9's insecure-direct-object-reference
	// principle, restated for department-scoped administrators): a
	// department administrator must never learn, through response shape or
	// timing, that an account outside their scope exists at all.
	ErrNotFound = errors.New("adminservice: user not found")
	// ErrConflict marks a duplicate normalized email on create. Safe to
	// return distinctly here (unlike the public login/reset flows) because
	// the caller is already an authenticated, authorized administrator with
	// ManageUsers permission, matching Section 7's proposed validation
	// message "This email already has an account."
	ErrConflict = errors.New("adminservice: email already has an account")
	// ErrInvalidTransition marks a state-changing action not permitted from
	// the account's current state (identity.AllowedTransition's own rule,
	// surfaced unchanged).
	ErrInvalidTransition = errors.New("adminservice: action not permitted for the account's current state")
	// ErrSelfAction marks an administrator attempting a dangerous action
	// against their own account (suspend, disable, revoke sessions,
	// administrator reset, or role/scope change) — Section 7's implicit
	// safety requirement that an administrator cannot lock themselves out
	// mid-session.
	ErrSelfAction = errors.New("adminservice: this action cannot be performed on your own account")
	// ErrInternal marks an unexpected persistence failure. The HTTP layer
	// must render this as a generic "temporarily unavailable" response,
	// never a raw database error.
	ErrInternal = errors.New("adminservice: internal error")
)

// Store is the subset of identitystore.Postgres this service depends on, so
// tests can substitute a fake without a live database.
type Store interface {
	CreateUserAndInvite(ctx context.Context, params identitystore.NewUserParams) (identity.UserID, error)
	GetByID(ctx context.Context, id identity.UserID) (identity.User, error)
	ListUsers(ctx context.Context, filter identitystore.ListUsersFilter) ([]identity.User, error)
	Suspend(ctx context.Context, userID identity.UserID, now time.Time) error
	Disable(ctx context.Context, userID identity.UserID, now time.Time) error
	Restore(ctx context.Context, userID identity.UserID, now time.Time) (identity.AccountState, error)
	Revoke(ctx context.Context, userID identity.UserID, now time.Time) (int64, error)
	ChangeRole(ctx context.Context, params identitystore.ChangeRoleParams) (identitystore.PriorRoleScope, error)
	ResendInvitation(ctx context.Context, userID, issuedBy identity.UserID, newTokenDigest []byte, expiresAt time.Time) error
	AdminReset(ctx context.Context, userID, issuedBy identity.UserID, newTokenDigest []byte, expiresAt, now time.Time) error
}

// AuditRecorder is the subset of identityauditstore.Store this service
// depends on, structurally identical to identityservice's own
// AuditRecorder so a *identityauditstore.Postgres satisfies both without an
// import-cycle-causing direct dependency.
type AuditRecorder interface {
	Record(ctx context.Context, ev identityaudit.Event) (int64, error)
}

// Config is the explicit, caller-supplied policy this service enforces.
type Config struct {
	// InvitationTTL is Section 2's invitation-token expiration window,
	// applied identically to initial issuance, resend, and administrator
	// reset. Sourced from the contract's own reserved
	// GFR_AUTH_INVITATION_TTL variable (Section 11.5); this package picks
	// no implicit default.
	InvitationTTL time.Duration
}

// Service implements the Step 9E administrator user-management operations.
type Service struct {
	store  Store
	audit  AuditRecorder
	cfg    Config
	logger *slog.Logger
}

// New constructs a Service. logger may be nil (a no-op logger is used), but
// store, audit, and cfg.InvitationTTL must be valid; New panics on a nil
// store/audit or a non-positive InvitationTTL, matching identityservice's
// fail-fast-at-construction convention for explicitly-chosen policy values.
func New(store Store, audit AuditRecorder, cfg Config, logger *slog.Logger) *Service {
	if store == nil || audit == nil {
		panic("adminservice: store and audit are required")
	}
	if cfg.InvitationTTL <= 0 {
		panic("adminservice: InvitationTTL must be positive and explicitly chosen")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Service{store: store, audit: audit, cfg: cfg, logger: logger}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func (s *Service) recordAudit(ctx context.Context, t identityaudit.EventType, accountID, actorID identity.UserID, reason string, metadata identityaudit.Metadata, now time.Time) {
	ev, err := identityaudit.New(t, accountID, actorID, reason, metadata, now)
	if err != nil {
		s.logger.Error("admin_audit_build_failed", "event_type", string(t))
		return
	}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		s.logger.Error("admin_audit_write_failed", "event_type", string(t))
	}
}

// resolveManageScope authorizes and resolves the scope within which actor
// may manage users, reusing the Step 9B authorization.Allowed matrix rather
// than re-implementing role logic here. A system administrator may pass any
// requested scope, including empty (no restriction). A department
// administrator is always pinned to their own scope: a requested scope
// different from their own is forbidden, never silently widened or
// redirected. Any other role is denied outright, since the matrix grants no
// role outside these two ManageUsers reach at all.
func resolveManageScope(actor authorization.Actor, requested identity.Scope) (identity.Scope, error) {
	switch actor.Role {
	case identity.RoleSystemAdministrator:
		if !authorization.Allowed(actor, authorization.ManageUsers, requested, 0) {
			return "", ErrForbidden
		}
		return requested, nil
	case identity.RoleDepartmentAdministrator:
		if requested != "" && requested != actor.Scope {
			return "", ErrForbidden
		}
		if !authorization.Allowed(actor, authorization.ManageUsers, actor.Scope, 0) {
			return "", ErrForbidden
		}
		return actor.Scope, nil
	default:
		return "", ErrForbidden
	}
}

// loadVisible loads targetID and confirms actor may see it under the
// Step 9B ManageUsers permission. An out-of-scope target is reported
// identically to a nonexistent one (ErrNotFound), so a department
// administrator can never distinguish "not visible to you" from "does not
// exist" through this service's response.
func (s *Service) loadVisible(ctx context.Context, actor authorization.Actor, targetID identity.UserID) (identity.User, error) {
	user, err := s.store.GetByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, identitystore.ErrNotFound) {
			return identity.User{}, ErrNotFound
		}
		return identity.User{}, ErrInternal
	}
	if !authorization.Allowed(actor, authorization.ManageUsers, user.Scope, user.ID) {
		return identity.User{}, ErrNotFound
	}
	return user, nil
}

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, identitystore.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, identitystore.ErrConflict):
		return ErrConflict
	case errors.Is(err, identitystore.ErrInvalidTransition):
		return ErrInvalidTransition
	case errors.Is(err, identitystore.ErrInput):
		return ErrInvalidInput
	default:
		return ErrInternal
	}
}

// ErrInvalidInput marks a caller/validation error (malformed email, unknown
// role, oversized display name, invalid scope, and so on). Safe to display;
// never wraps a raw database error or echoes a credential.
var ErrInvalidInput = errors.New("adminservice: invalid input")

// newInvitationToken generates a fresh single-use invitation token and its
// digest, matching Section 2's "single-use, server-generated, random token"
// requirement.
func (s *Service) newInvitationToken(now time.Time) (raw string, digest []byte, expiresAt time.Time, err error) {
	raw, err = invitation.GenerateToken()
	if err != nil {
		return "", nil, time.Time{}, ErrInternal
	}
	return raw, invitation.Digest(raw), now.Add(s.cfg.InvitationTTL), nil
}

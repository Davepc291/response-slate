package identitystore

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/session"
)

// lockUserState locks and returns userID's current state and password
// presence within tx, so the caller can decide the next state under
// identity.AllowedTransition before writing it, race-free against a
// concurrent state change on the same row.
func lockUserState(ctx context.Context, tx Conn, userID identity.UserID) (identity.AccountState, bool, error) {
	var status string
	var hasPassword bool
	err := tx.QueryRow(ctx, `SELECT status, password_hash IS NOT NULL FROM users WHERE id = $1 FOR UPDATE`,
		int64(userID)).Scan(&status, &hasPassword)
	if err != nil {
		return "", false, safeDB(err)
	}
	return identity.AccountState(status), hasPassword, nil
}

func setUserStatus(ctx context.Context, tx Conn, userID identity.UserID, to identity.AccountState) error {
	_, err := tx.Exec(ctx, `UPDATE users SET status = $1 WHERE id = $2`, string(to), int64(userID))
	return safeDB(err)
}

func revokeAllSessionsTx(ctx context.Context, tx Conn, userID identity.UserID, reason session.RevocationReason, now time.Time) (int64, error) {
	tag, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = $1, revocation_reason = $2
        WHERE user_id = $3 AND revoked_at IS NULL`, now, string(reason), int64(userID))
	if err != nil {
		return 0, safeDB(err)
	}
	return tag.RowsAffected(), nil
}

// Suspend moves userID to suspended (Section 7: temporary, reversible
// block) and revokes every active session, atomically. Returns
// ErrInvalidTransition if the account's current state does not permit it.
func (p *Postgres) Suspend(ctx context.Context, userID identity.UserID, now time.Time) error {
	return p.transitionAndRevoke(ctx, userID, identity.StateSuspended, session.RevokedAccountSuspended, now)
}

// Disable moves userID to disabled (Section 7: permanent block for departed
// personnel) and revokes every active session, atomically.
func (p *Postgres) Disable(ctx context.Context, userID identity.UserID, now time.Time) error {
	return p.transitionAndRevoke(ctx, userID, identity.StateDisabled, session.RevokedAccountDisabled, now)
}

func (p *Postgres) transitionAndRevoke(ctx context.Context, userID identity.UserID, to identity.AccountState, reason session.RevocationReason, now time.Time) error {
	if p == nil || p.DB == nil {
		return ErrUnavailable
	}
	if now.IsZero() {
		return ErrInput
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer rollback(tx)

	current, _, err := lockUserState(ctx, tx, userID)
	if err != nil {
		return err
	}
	if !identity.AllowedTransition(current, to) {
		return ErrInvalidTransition
	}
	if err := setUserStatus(ctx, tx, userID, to); err != nil {
		return err
	}
	if _, err := revokeAllSessionsTx(ctx, tx, userID, reason, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}

// Restore returns a suspended or disabled account to active (if it already
// established a permanent password), or to invited/expired handling
// otherwise (Section 7): invited if an unexpired pending invitation still
// exists, expired if not. Returns the resulting state.
func (p *Postgres) Restore(ctx context.Context, userID identity.UserID, now time.Time) (identity.AccountState, error) {
	if p == nil || p.DB == nil {
		return "", ErrUnavailable
	}
	if now.IsZero() {
		return "", ErrInput
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer rollback(tx)

	current, hasPassword, err := lockUserState(ctx, tx, userID)
	if err != nil {
		return "", err
	}
	if current != identity.StateSuspended && current != identity.StateDisabled {
		return "", ErrInvalidTransition
	}

	target := identity.StateExpired
	if hasPassword {
		target = identity.StateActive
	} else {
		var expiresAt time.Time
		err := tx.QueryRow(ctx, `SELECT expires_at FROM invitations WHERE user_id = $1 AND status = 'pending'`,
			int64(userID)).Scan(&expiresAt)
		switch {
		case err == nil && now.Before(expiresAt):
			target = identity.StateInvited
		case err == nil, safeDB(err) == ErrNotFound:
			target = identity.StateExpired
		default:
			return "", safeDB(err)
		}
	}
	if !identity.AllowedTransition(current, target) {
		return "", ErrInvalidTransition
	}
	if err := setUserStatus(ctx, tx, userID, target); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", ErrUnavailable
	}
	return target, nil
}

// Revoke immediately invalidates every session for userID without changing
// its enabled/disabled state (Section 7: "kill this person's current access
// right now, decide the account's fate after").
func (p *Postgres) Revoke(ctx context.Context, userID identity.UserID, now time.Time) (int64, error) {
	if p == nil || p.DB == nil {
		return 0, ErrUnavailable
	}
	if now.IsZero() {
		return 0, ErrInput
	}
	return revokeAllSessionsTx(ctx, p.DB, userID, session.RevokedAdminRevoke, now)
}

// ChangeRoleParams carries a role/scope change (Section 7, Section 10:
// "Role or scope change"). Authorization (who may make this change) is the
// authorization package's responsibility, evaluated by the caller before
// this method is ever invoked; this method only performs the write.
type ChangeRoleParams struct {
	UserID   identity.UserID
	NewRole  identity.Role
	NewScope identity.Scope
}

// PriorRoleScope reports the role/scope in effect immediately before a
// ChangeRole call, for the caller to build an audit event from.
type PriorRoleScope struct {
	Role  identity.Role
	Scope identity.Scope
}

// ChangeRole updates a user's role and/or scope and returns the prior
// values. It performs no authorization check itself; see the
// authorization package's CanAssignRole.
func (p *Postgres) ChangeRole(ctx context.Context, params ChangeRoleParams) (PriorRoleScope, error) {
	if p == nil || p.DB == nil {
		return PriorRoleScope{}, ErrUnavailable
	}
	if err := identity.ValidateRole(params.NewRole); err != nil {
		return PriorRoleScope{}, ErrInput
	}
	if err := identity.ValidateScope(params.NewScope); err != nil {
		return PriorRoleScope{}, ErrInput
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return PriorRoleScope{}, ErrUnavailable
	}
	defer rollback(tx)

	var priorRole, priorScope string
	if err := tx.QueryRow(ctx, `SELECT role, coalesce(scope, '') FROM users WHERE id = $1 FOR UPDATE`,
		int64(params.UserID)).Scan(&priorRole, &priorScope); err != nil {
		return PriorRoleScope{}, safeDB(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET role = $1, scope = NULLIF($2, '') WHERE id = $3`,
		string(params.NewRole), string(params.NewScope), int64(params.UserID)); err != nil {
		return PriorRoleScope{}, safeDB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PriorRoleScope{}, ErrUnavailable
	}
	return PriorRoleScope{Role: identity.Role(priorRole), Scope: identity.Scope(priorScope)}, nil
}

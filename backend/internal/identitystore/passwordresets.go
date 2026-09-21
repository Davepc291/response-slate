package identitystore

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/session"
)

// RequestPasswordReset is Section 4's "forgot password" / administrator-
// initiated reset-token issuance, distinct from Section 2's full re-
// invitation (AdminReset): it lets a user already capable of authenticating
// (active or password_change_required) set a new password directly via a
// short-lived reset token, without forcing a full re-invitation cycle.
//
// Per Section 3/4's account-enumeration resistance rule, this method
// returns (false, nil) — never an error — when email matches no eligible
// account, so a caller (a future HTTP handler) can render the identical
// "If that email has an account, a reset link has been sent." response
// regardless of match. The bool return is for the caller's own audit/
// issuance bookkeeping only, never for building a different user-facing
// response.
func (p *Postgres) RequestPasswordReset(ctx context.Context, email string, issuedBy identity.UserID, newTokenDigest []byte, expiresAt time.Time) (bool, identity.UserID, error) {
	if p == nil || p.DB == nil {
		return false, 0, ErrUnavailable
	}
	if len(newTokenDigest) == 0 || expiresAt.IsZero() {
		return false, 0, ErrInput
	}
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		return false, 0, ErrInput
	}

	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return false, 0, ErrUnavailable
	}
	defer rollback(tx)

	var userID int64
	var status string
	err = tx.QueryRow(ctx, `SELECT id, status FROM users WHERE normalized_email = $1 FOR UPDATE`, normalized).Scan(&userID, &status)
	if err != nil {
		if safeDB(err) == ErrNotFound {
			return false, 0, nil // no matching account: identical, silent non-error
		}
		return false, 0, safeDB(err)
	}
	switch identity.AccountState(status) {
	case identity.StateActive, identity.StatePasswordChangeRequired:
	default:
		// Suspended/disabled/invited/expired: do not issue a usable reset
		// token, but still respond identically to "not found" — never
		// confirm account existence or state through this path.
		return false, 0, nil
	}

	if _, err := tx.Exec(ctx, `UPDATE password_resets SET status = 'superseded' WHERE user_id = $1 AND status = 'pending'`,
		userID); err != nil {
		return false, 0, safeDB(err)
	}
	var issuedByArg *int64
	if issuedBy != 0 {
		v := int64(issuedBy)
		issuedByArg = &v
	}
	if _, err := tx.Exec(ctx, `INSERT INTO password_resets (user_id, token_digest, issued_by, expires_at)
        VALUES ($1, $2, $3, $4)`, userID, newTokenDigest, issuedByArg, expiresAt); err != nil {
		return false, 0, safeDB(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, ErrUnavailable
	}
	return true, identity.UserID(userID), nil
}

// CompletePasswordReset atomically redeems rawTokenDigest, sets a new
// permanent password hash, advances a still-in-progress
// password_change_required account to active, and revokes every existing
// session for that account (Section 4: "immediately revokes every existing
// session...not only the session that requested the reset"). It is
// resistant to concurrent reuse for the same reason RedeemInvitation is:
// the claiming UPDATE only ever matches a still-pending row.
func (p *Postgres) CompletePasswordReset(ctx context.Context, rawTokenDigest []byte, newPasswordHash string, now time.Time) (identity.UserID, error) {
	if p == nil || p.DB == nil {
		return 0, ErrUnavailable
	}
	if len(rawTokenDigest) == 0 || newPasswordHash == "" || now.IsZero() {
		return 0, ErrInput
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer rollback(tx)

	var userID int64
	var resultStatus string
	row := tx.QueryRow(ctx, `UPDATE password_resets SET
            status = CASE WHEN expires_at <= $2 THEN 'expired' ELSE 'redeemed' END,
            redeemed_at = CASE WHEN expires_at <= $2 THEN NULL ELSE $2 END
        WHERE token_digest = $1 AND status = 'pending'
        RETURNING user_id, status`, rawTokenDigest, now)
	if err := row.Scan(&userID, &resultStatus); err != nil {
		if safeDB(err) != ErrNotFound {
			return 0, safeDB(err)
		}
		var existingStatus string
		lookupErr := tx.QueryRow(ctx, `SELECT status FROM password_resets WHERE token_digest = $1`, rawTokenDigest).Scan(&existingStatus)
		if lookupErr == nil && existingStatus == "expired" {
			if err := tx.Commit(ctx); err != nil {
				return 0, ErrUnavailable
			}
			return 0, ErrTokenExpired
		}
		return 0, ErrTokenInvalid
	}
	if resultStatus == "expired" {
		if err := tx.Commit(ctx); err != nil {
			return 0, ErrUnavailable
		}
		return 0, ErrTokenExpired
	}

	tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $1, password_updated_at = $2
        WHERE id = $3 AND status IN ($4, $5)`,
		newPasswordHash, now, userID, string(identity.StateActive), string(identity.StatePasswordChangeRequired))
	if err != nil {
		return 0, safeDB(err)
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrInvalidTransition
	}
	if _, err := activateIfEligible(ctx, tx, identity.UserID(userID)); err != nil {
		return 0, err
	}
	if _, err := revokeAllSessionsTx(ctx, tx, identity.UserID(userID), session.RevokedPasswordReset, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, ErrUnavailable
	}
	return identity.UserID(userID), nil
}

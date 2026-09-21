package identitystore

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/session"
)

// RedeemInvitation atomically consumes rawTokenDigest (Section 2, step 4):
// on success it marks the invitation redeemed and moves the account from
// invited to password_change_required in one transaction, and is resistant
// to concurrent reuse because the UPDATE's WHERE clause only ever matches a
// still-pending row — a second concurrent caller racing the same token
// digest affects zero rows and receives ErrTokenInvalid, never a second
// success (AAX-06). A lazily-discovered expiration (expires_at already
// passed, but no background job has marked the row expired yet) is
// resolved atomically in the same statement, distinctly from an
// already-redeemed/superseded/unknown token, matching Section 3's narrow
// enumeration-resistance exception for expired invitations.
func (p *Postgres) RedeemInvitation(ctx context.Context, rawTokenDigest []byte, now time.Time) (identity.UserID, error) {
	if p == nil || p.DB == nil {
		return 0, ErrUnavailable
	}
	if len(rawTokenDigest) == 0 || now.IsZero() {
		return 0, ErrInput
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer rollback(tx)

	var userID int64
	var resultStatus string
	row := tx.QueryRow(ctx, `UPDATE invitations SET
            status = CASE WHEN expires_at <= $2 THEN 'expired' ELSE 'redeemed' END,
            redeemed_at = CASE WHEN expires_at <= $2 THEN NULL ELSE $2 END
        WHERE token_digest = $1 AND status = 'pending'
        RETURNING user_id, status`, rawTokenDigest, now)
	err = row.Scan(&userID, &resultStatus)
	if err != nil {
		if safeDB(err) != ErrNotFound {
			return 0, safeDB(err)
		}
		// Zero rows matched: either no such token, or it was already
		// redeemed/superseded, or a prior call already transitioned it to
		// expired. Distinguish only the "already expired" case, per
		// Section 3's narrow exception.
		var existingStatus string
		lookupErr := tx.QueryRow(ctx, `SELECT status FROM invitations WHERE token_digest = $1`, rawTokenDigest).Scan(&existingStatus)
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

	// Advance the account. Only an invited account should ever own a
	// pending invitation; guard against a corrupted invariant rather than
	// trusting it silently.
	if err := setUserStatus(ctx, tx, identity.UserID(userID), identity.StatePasswordChangeRequired); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, ErrUnavailable
	}
	return identity.UserID(userID), nil
}

// ResendInvitation invalidates any prior active invitation for userID and
// issues a new one with a fresh expiration (Section 2: "Resend"), moving an
// expired account back to invited. The prior token is invalidated even if
// it was still technically unexpired, so only the newest issued invitation
// is ever valid. Only permitted for an account currently invited or
// expired; returns ErrInvalidTransition otherwise.
func (p *Postgres) ResendInvitation(ctx context.Context, userID, issuedBy identity.UserID, newTokenDigest []byte, expiresAt time.Time) error {
	if p == nil || p.DB == nil {
		return ErrUnavailable
	}
	if len(newTokenDigest) == 0 || expiresAt.IsZero() || issuedBy == 0 {
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
	if current != identity.StateInvited && current != identity.StateExpired {
		return ErrInvalidTransition
	}
	if _, err := tx.Exec(ctx, `UPDATE invitations SET status = 'superseded' WHERE user_id = $1 AND status = 'pending'`,
		int64(userID)); err != nil {
		return safeDB(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invitations (user_id, token_digest, issued_by, expires_at)
        VALUES ($1, $2, $3, $4)`, int64(userID), newTokenDigest, int64(issuedBy), expiresAt); err != nil {
		return safeDB(err)
	}
	if current != identity.StateInvited {
		if !identity.AllowedTransition(current, identity.StateInvited) {
			return ErrInvalidTransition
		}
		if err := setUserStatus(ctx, tx, userID, identity.StateInvited); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}

// AdminReset is Section 2's administrator "Reset" action, for an active,
// suspended, or password_change_required account that needs a new
// temporary credential (for example, after a suspected compromise): it
// immediately invalidates every existing session, clears the permanent
// password hash, issues a new single-use invitation following the exact
// same rules as initial issuance, and moves the account to
// password_change_required, all atomically. Returns ErrInvalidTransition if
// the account is not currently active, suspended, or password_change_required.
func (p *Postgres) AdminReset(ctx context.Context, userID, issuedBy identity.UserID, newTokenDigest []byte, expiresAt, now time.Time) error {
	if p == nil || p.DB == nil {
		return ErrUnavailable
	}
	if len(newTokenDigest) == 0 || expiresAt.IsZero() || now.IsZero() || issuedBy == 0 {
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
	switch current {
	case identity.StateActive, identity.StateSuspended, identity.StatePasswordChangeRequired:
	default:
		return ErrInvalidTransition
	}
	if !identity.AllowedTransition(current, identity.StatePasswordChangeRequired) {
		return ErrInvalidTransition
	}

	if _, err := tx.Exec(ctx, `UPDATE invitations SET status = 'superseded' WHERE user_id = $1 AND status = 'pending'`,
		int64(userID)); err != nil {
		return safeDB(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invitations (user_id, token_digest, issued_by, expires_at)
        VALUES ($1, $2, $3, $4)`, int64(userID), newTokenDigest, int64(issuedBy), expiresAt); err != nil {
		return safeDB(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status = $1, password_hash = NULL, password_updated_at = NULL
        WHERE id = $2`, string(identity.StatePasswordChangeRequired), int64(userID)); err != nil {
		return safeDB(err)
	}
	if _, err := revokeAllSessionsTx(ctx, tx, userID, session.RevokedPasswordReset, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}

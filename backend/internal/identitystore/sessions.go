package identitystore

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/session"
)

// CreateSession issues a new session row for userID, storing only
// tokenDigest (Section 8). It is only permitted for a currently active
// account: a session is never created for an account that could not
// authorize protected access anyway (Section 1, Section 8), so
// ErrAccountNotActive is returned instead of a session that could never be
// used.
func (p *Postgres) CreateSession(ctx context.Context, userID identity.UserID, tokenDigest []byte, deviceHint string, createdAt, expiresAt time.Time) (session.ID, error) {
	if p == nil || p.DB == nil {
		return 0, ErrUnavailable
	}
	if len(tokenDigest) == 0 || createdAt.IsZero() || !expiresAt.After(createdAt) {
		return 0, ErrInput
	}
	var hint *string
	if deviceHint != "" {
		hint = &deviceHint
	}
	var id int64
	row := p.DB.QueryRow(ctx, `INSERT INTO sessions (user_id, token_digest, device_hint, created_at, last_seen_at, expires_at)
        SELECT $1, $2, $3, $4, $4, $5 FROM users WHERE id = $1 AND status = $6
        RETURNING id`,
		int64(userID), tokenDigest, hint, createdAt, expiresAt, string(identity.StateActive))
	if err := row.Scan(&id); err != nil {
		if safeDB(err) == ErrNotFound {
			return 0, ErrAccountNotActive
		}
		return 0, safeDB(err)
	}
	return session.ID(id), nil
}

// Authorized is the result of resolving a presented session token: the
// session record and the safe public view of the account it belongs to.
// The caller must still separately check role/scope authorization for the
// specific operation requested (Section 6, Section 9): a valid, Usable
// session only proves identity, never authorization for a specific
// operation.
type Authorized struct {
	Session session.Session
	Account identity.View
	Role    identity.Role
	Scope   identity.Scope
}

// ResolveSession looks up tokenDigest and reports whether it currently
// authorizes access: the session must be unrevoked and unexpired (idle and
// absolute, per cfg), and the owning account must currently be active
// (Section 1, Section 8: "Sessions for suspended, disabled, expired, or
// password-change-required accounts cannot authorize protected access").
// Any failure of either check returns ErrTokenInvalid without
// distinguishing which one failed, so a caller can never use this method to
// probe account state through session-validity timing/detail.
func (p *Postgres) ResolveSession(ctx context.Context, tokenDigest []byte, cfg session.Config, now time.Time) (Authorized, error) {
	if p == nil || p.DB == nil {
		return Authorized{}, ErrUnavailable
	}
	if len(tokenDigest) == 0 || now.IsZero() {
		return Authorized{}, ErrInput
	}
	if err := cfg.Validate(); err != nil {
		return Authorized{}, ErrInput
	}

	var (
		s                       session.Session
		id                      int64
		userID                  int64
		deviceHint              *string
		revokedAt               *time.Time
		revocationReason        *string
		mfaVerifiedAt           *time.Time
		email, displayName      string
		role, scope, status     string
		lastLoginAt, createdAt2 *time.Time
	)
	row := p.DB.QueryRow(ctx, `SELECT s.id, s.user_id, s.device_hint, s.created_at, s.last_seen_at, s.expires_at,
            s.revoked_at, s.revocation_reason, s.mfa_verified_at,
            u.normalized_email, u.display_name, u.role, coalesce(u.scope, ''), u.status, u.last_login_at, u.created_at
        FROM sessions s JOIN users u ON u.id = s.user_id
        WHERE s.token_digest = $1`, tokenDigest)
	if err := row.Scan(&id, &userID, &deviceHint, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt,
		&revokedAt, &revocationReason, &mfaVerifiedAt, &email, &displayName, &role, &scope, &status, &lastLoginAt, &createdAt2); err != nil {
		if safeDB(err) == ErrNotFound {
			return Authorized{}, ErrTokenInvalid
		}
		return Authorized{}, safeDB(err)
	}
	s.ID = session.ID(id)
	s.UserID = userID
	if deviceHint != nil {
		s.DeviceHint = *deviceHint
	}
	s.RevokedAt = revokedAt
	if revocationReason != nil {
		s.RevocationReason = session.RevocationReason(*revocationReason)
	}
	s.MFAVerifiedAt = mfaVerifiedAt

	if !cfg.Usable(s, now) || identity.AccountState(status) != identity.StateActive {
		return Authorized{}, ErrTokenInvalid
	}

	view := identity.View{
		ID: identity.UserID(userID), Email: email, DisplayName: displayName,
		Role: identity.Role(role), Scope: identity.Scope(scope), Status: identity.AccountState(status),
	}
	if lastLoginAt != nil {
		view.LastLoginAt = *lastLoginAt
	}
	if createdAt2 != nil {
		view.CreatedAt = *createdAt2
	}
	return Authorized{Session: s, Account: view, Role: identity.Role(role), Scope: identity.Scope(scope)}, nil
}

// MarkSessionMFAVerified records that id completed a WebAuthn
// authentication ceremony (Step 9F-4, Section 5, AAX-07): only this one
// session row is affected, never every session belonging to the owning
// account, and never any mfa_credentials row (enrollment and per-session
// verification are deliberately distinct facts). The update is scoped to a
// session that is, as of now, both unrevoked AND unexpired — mirroring
// exactly the two conditions session.Config.Usable checks — and the
// affected row count is verified: a revoked session, an already-expired
// session, or an unknown id all return ErrNotFound rather than a silent
// success, so a caller (FinishMFALogin) can never report a completed
// verification, or record its audit event, for a session that was not
// actually updated (e.g. a concurrent revocation racing this call).
func (p *Postgres) MarkSessionMFAVerified(ctx context.Context, id session.ID, now time.Time) error {
	if p == nil || p.DB == nil {
		return ErrUnavailable
	}
	if now.IsZero() {
		return ErrInput
	}
	tag, err := p.DB.Exec(ctx, `UPDATE sessions SET mfa_verified_at = $1
        WHERE id = $2 AND revoked_at IS NULL AND expires_at > $1`, now, int64(id))
	if err != nil {
		return safeDB(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchSession advances a session's last_seen_at, extending its idle
// window. It has no effect on (and does not reset) the absolute
// expires_at ceiling.
func (p *Postgres) TouchSession(ctx context.Context, id session.ID, now time.Time) error {
	if p == nil || p.DB == nil {
		return ErrUnavailable
	}
	if now.IsZero() {
		return ErrInput
	}
	tag, err := p.DB.Exec(ctx, `UPDATE sessions SET last_seen_at = $1 WHERE id = $2 AND revoked_at IS NULL`, now, int64(id))
	if err != nil {
		return safeDB(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeSession ends exactly one session (Section 8: "Logout"). Revoking an
// already-revoked or unknown session is a no-op success, matching
// idempotent revocation semantics used elsewhere in this repository.
func (p *Postgres) RevokeSession(ctx context.Context, id session.ID, reason session.RevocationReason, now time.Time) error {
	if p == nil || p.DB == nil {
		return ErrUnavailable
	}
	if !reason.Valid() || now.IsZero() {
		return ErrInput
	}
	_, err := p.DB.Exec(ctx, `UPDATE sessions SET revoked_at = $1, revocation_reason = $2
        WHERE id = $3 AND revoked_at IS NULL`, now, string(reason), int64(id))
	return safeDB(err)
}

// RevokeAllSessionsForUser ends every currently active session for userID
// (Section 8: "Logout of all devices"), returning the count revoked.
func (p *Postgres) RevokeAllSessionsForUser(ctx context.Context, userID identity.UserID, reason session.RevocationReason, now time.Time) (int64, error) {
	if p == nil || p.DB == nil {
		return 0, ErrUnavailable
	}
	if !reason.Valid() || now.IsZero() {
		return 0, ErrInput
	}
	return revokeAllSessionsTx(ctx, p.DB, userID, reason, now)
}

// ListActiveSessions returns every currently active (unrevoked) session for
// userID, for Section 8's "device/session listing" feature.
func (p *Postgres) ListActiveSessions(ctx context.Context, userID identity.UserID) ([]session.Session, error) {
	if p == nil || p.DB == nil {
		return nil, ErrUnavailable
	}
	rows, err := p.DB.Query(ctx, `SELECT id, device_hint, created_at, last_seen_at, expires_at FROM sessions
        WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC`, int64(userID))
	if err != nil {
		return nil, safeDB(err)
	}
	defer rows.Close()

	var out []session.Session
	for rows.Next() {
		var s session.Session
		var id int64
		var hint *string
		if err := rows.Scan(&id, &hint, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt); err != nil {
			return nil, safeDB(err)
		}
		s.ID = session.ID(id)
		s.UserID = int64(userID)
		if hint != nil {
			s.DeviceHint = *hint
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDB(err)
	}
	return out, nil
}

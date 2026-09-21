package identitystore

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

// NewUserParams carries the approved minimum information for creating an
// account (Section 7, step 3): normalized email, display name, role, and
// optional scope. There is no password field: the system generates the
// invitation, never an administrator-chosen password (Section 7).
type NewUserParams struct {
	Email       string
	DisplayName string
	Role        identity.Role
	Scope       identity.Scope
	CreatedBy   identity.UserID
	// InvitationTokenDigest and InvitationExpiresAt are supplied by the
	// caller (via the invitation package's GenerateToken/Digest), because
	// this store never generates or sees a raw token itself.
	InvitationTokenDigest []byte
	InvitationExpiresAt   time.Time
}

func (p NewUserParams) validate() error {
	if len(p.InvitationTokenDigest) == 0 || p.InvitationExpiresAt.IsZero() || p.CreatedBy == 0 {
		return ErrInput
	}
	if err := identity.ValidateRole(p.Role); err != nil {
		return ErrInput
	}
	if err := identity.ValidateScope(p.Scope); err != nil {
		return ErrInput
	}
	if err := identity.ValidateDisplayName(p.DisplayName); err != nil {
		return ErrInput
	}
	return nil
}

// CreateUserAndInvite creates a new account in the invited state and issues
// its first invitation atomically (Section 7, steps 3-5): if the invitation
// insert fails, the user row is never left behind without one. Returns
// ErrConflict if the normalized email is already in use by any account,
// including a disabled or expired one (Section 1: an email is never
// reused).
func (p *Postgres) CreateUserAndInvite(ctx context.Context, params NewUserParams) (identity.UserID, error) {
	if p == nil || p.DB == nil {
		return 0, ErrUnavailable
	}
	if err := params.validate(); err != nil {
		return 0, err
	}
	email, err := identity.NormalizeEmail(params.Email)
	if err != nil {
		return 0, ErrInput
	}

	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer rollback(tx)

	var userID int64
	row := tx.QueryRow(ctx, `INSERT INTO users (normalized_email, display_name, role, scope, created_by)
        VALUES ($1, $2, $3, NULLIF($4, ''), $5) RETURNING id`,
		email, params.DisplayName, string(params.Role), string(params.Scope), int64(params.CreatedBy))
	if err := row.Scan(&userID); err != nil {
		return 0, safeDB(err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO invitations (user_id, token_digest, issued_by, expires_at)
        VALUES ($1, $2, $3, $4)`,
		userID, params.InvitationTokenDigest, int64(params.CreatedBy), params.InvitationExpiresAt); err != nil {
		return 0, safeDB(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, ErrUnavailable
	}
	return identity.UserID(userID), nil
}

// GetByID returns the full internal User record. Callers outside this
// package and passwordpolicy must render it via User.Public() before
// exposing it anywhere.
func (p *Postgres) GetByID(ctx context.Context, id identity.UserID) (identity.User, error) {
	if p == nil || p.DB == nil {
		return identity.User{}, ErrUnavailable
	}
	return scanUser(p.DB.QueryRow(ctx, userSelectByID, int64(id)))
}

// GetByEmail returns the full internal User record for a normalized email.
func (p *Postgres) GetByEmail(ctx context.Context, email string) (identity.User, error) {
	if p == nil || p.DB == nil {
		return identity.User{}, ErrUnavailable
	}
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		return identity.User{}, ErrInput
	}
	return scanUser(p.DB.QueryRow(ctx, userSelectByEmail, normalized))
}

const userSelectColumns = `id, normalized_email, display_name, role, coalesce(scope, ''), status,
    coalesce(password_hash, ''), password_updated_at, last_login_at, coalesce(created_by, 0), created_at, updated_at`

var (
	userSelectByID    = `SELECT ` + userSelectColumns + ` FROM users WHERE id = $1`
	userSelectByEmail = `SELECT ` + userSelectColumns + ` FROM users WHERE normalized_email = $1`
)

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (identity.User, error) {
	var (
		u                              identity.User
		id, createdBy                  int64
		role, scope, status, pwHash    string
		passwordUpdatedAt, lastLoginAt *time.Time
	)
	err := row.Scan(&id, &u.NormalizedEmail, &u.DisplayName, &role, &scope, &status,
		&pwHash, &passwordUpdatedAt, &lastLoginAt, &createdBy, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return identity.User{}, safeDB(err)
	}
	u.ID = identity.UserID(id)
	u.Role = identity.Role(role)
	u.Scope = identity.Scope(scope)
	u.Status = identity.AccountState(status)
	u.PasswordHash = pwHash
	u.CreatedByID = identity.UserID(createdBy)
	if passwordUpdatedAt != nil {
		u.PasswordUpdatedAt = *passwordUpdatedAt
	}
	if lastLoginAt != nil {
		u.LastLoginAt = *lastLoginAt
	}
	return u, nil
}

// EstablishPassword sets a permanent password hash for an account currently
// in password_change_required (Section 2, step 4) and advances it to active
// only once every required condition is met: a permanent password is now
// set, and — for a role Section 5 requires MFA for — at least one
// non-revoked mfa_credentials row already exists. Step 9B implements no MFA
// enrollment path, so an administrator role will remain in
// password_change_required (now with its permanent password already set)
// until a future, separately authorized phase adds enrollment; this is the
// contract's own required gate, not a bug. Returns the account's resulting
// state and ErrInvalidTransition if the account was not in
// password_change_required.
func (p *Postgres) EstablishPassword(ctx context.Context, userID identity.UserID, passwordHash string, now time.Time) (identity.AccountState, error) {
	if p == nil || p.DB == nil {
		return "", ErrUnavailable
	}
	if passwordHash == "" || now.IsZero() {
		return "", ErrInput
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer rollback(tx)

	tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $1, password_updated_at = $2
        WHERE id = $3 AND status = $4`,
		passwordHash, now, int64(userID), string(identity.StatePasswordChangeRequired))
	if err != nil {
		return "", safeDB(err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrInvalidTransition
	}
	newStatus, err := activateIfEligible(ctx, tx, userID)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", ErrUnavailable
	}
	return newStatus, nil
}

// activateIfEligible moves userID from password_change_required to active
// if, and only if, every Section 5 requirement is already satisfied: a role
// that does not require MFA, or one that does and already has at least one
// non-revoked mfa_credentials row. It is shared by EstablishPassword and
// CompletePasswordReset so both paths apply the identical MFA gate. If
// userID is not currently password_change_required, it is left untouched
// and its current status is returned as-is (a no-op for an
// already-active caller).
func activateIfEligible(ctx context.Context, tx Conn, userID identity.UserID) (identity.AccountState, error) {
	var status, role string
	if err := tx.QueryRow(ctx, `SELECT status, role FROM users WHERE id = $1 FOR UPDATE`, int64(userID)).
		Scan(&status, &role); err != nil {
		return "", safeDB(err)
	}
	if identity.AccountState(status) != identity.StatePasswordChangeRequired {
		return identity.AccountState(status), nil
	}
	if identity.Role(role).RequiresMFA() {
		var hasMFA bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mfa_credentials WHERE user_id = $1 AND revoked_at IS NULL)`,
			int64(userID)).Scan(&hasMFA); err != nil {
			return "", safeDB(err)
		}
		if !hasMFA {
			return identity.StatePasswordChangeRequired, nil
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status = $1 WHERE id = $2`, string(identity.StateActive), int64(userID)); err != nil {
		return "", safeDB(err)
	}
	return identity.StateActive, nil
}

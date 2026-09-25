package identitystore

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

// mfaCredentialTypePasskey mirrors mfa.CredentialTypePasskey and migration
// 000008's mfa_credentials.credential_type CHECK constraint. It is
// duplicated as a literal, rather than importing the mfa package, so this
// lower-level store package never depends on a specific MFA provider
// implementation; TOTP (the constraint's other allowed value) is a
// separately authorized, not-yet-implemented credential type this package
// deliberately does not accept here (Step 9F implements passkeys only).
const mfaCredentialTypePasskey = "passkey"

const maxMFACredentialDataLen = 4096

// MFACredential is the store's read view of one mfa_credentials row. It
// never carries private key material: CredentialData mirrors the column
// exactly (public key material only for a passkey, per migration 000008's
// own comment), and this package does not interpret that blob — see the
// mfa package's MarshalCredential/UnmarshalCredential for its structure.
type MFACredential struct {
	ID             int64
	CredentialType string
	CredentialData []byte
	Label          string
	CreatedAt      time.Time
	LastUsedAt     time.Time
}

// EnrollMFACredential persists a new, non-revoked passkey credential for
// userID (Step 9F requirement F) and, in the same transaction, applies the
// exact activation gate EstablishPassword already applies via
// activateIfEligible: an administrator whose only remaining Section 5
// requirement was MFA becomes active in the same call that records the
// credential (AAX-07: no separate "now go activate" step to skip). An
// account not currently password_change_required or active is rejected
// outright — enrollment is never meaningful for invited, suspended,
// disabled, or expired accounts. Returns ErrConflict if this exact
// credential is already enrolled for this user (the mfa_credentials unique
// constraint on (user_id, credential_type, credential_data)), which a
// caller must treat as a failed enrollment, never as success.
func (p *Postgres) EnrollMFACredential(ctx context.Context, userID identity.UserID, credentialType string, credentialData []byte, label string) (identity.AccountState, error) {
	if p == nil || p.DB == nil {
		return "", ErrUnavailable
	}
	if userID == 0 || credentialType != mfaCredentialTypePasskey ||
		len(credentialData) == 0 || len(credentialData) > maxMFACredentialDataLen {
		return "", ErrInput
	}

	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return "", ErrUnavailable
	}
	defer rollback(tx)

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id = $1 FOR UPDATE`, int64(userID)).Scan(&status); err != nil {
		return "", safeDB(err)
	}
	st := identity.AccountState(status)
	if st != identity.StatePasswordChangeRequired && st != identity.StateActive {
		return "", ErrInvalidTransition
	}

	var labelArg *string
	if label != "" {
		labelArg = &label
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mfa_credentials (user_id, credential_type, credential_data, label)
        VALUES ($1, $2, $3, $4)`, int64(userID), credentialType, credentialData, labelArg); err != nil {
		return "", safeDB(err)
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

// ListMFACredentials returns every non-revoked MFA credential for userID,
// oldest first. It never returns a revoked credential: a caller building a
// WebAuthn registration exclusion list, or an "is MFA already enrolled"
// check, must only ever see currently valid credentials.
func (p *Postgres) ListMFACredentials(ctx context.Context, userID identity.UserID) ([]MFACredential, error) {
	if p == nil || p.DB == nil {
		return nil, ErrUnavailable
	}
	if userID == 0 {
		return nil, ErrInput
	}
	rows, err := p.DB.Query(ctx, `SELECT id, credential_type, credential_data, coalesce(label, ''), created_at, last_used_at
        FROM mfa_credentials WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at`, int64(userID))
	if err != nil {
		return nil, safeDB(err)
	}
	defer rows.Close()

	var out []MFACredential
	for rows.Next() {
		var (
			c          MFACredential
			lastUsedAt *time.Time
		)
		if err := rows.Scan(&c.ID, &c.CredentialType, &c.CredentialData, &c.Label, &c.CreatedAt, &lastUsedAt); err != nil {
			return nil, safeDB(err)
		}
		if lastUsedAt != nil {
			c.LastUsedAt = *lastUsedAt
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDB(err)
	}
	return out, nil
}

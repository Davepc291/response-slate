package identityservice

import (
	"context"
	"errors"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/invitation"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
	"greenwich-fire-responder/backend/internal/passwordreset"
)

// checkNewPassword applies Section 4's length policy and breach check to a
// password the caller is establishing. It is shared by invitation
// redemption and password-reset completion, the only two operations that
// ever set a password value (Section 4: the user is already authenticated
// to the account by holding a valid single-use token at this point, so a
// specific, safe policy-violation message is permitted here, distinct from
// the generic login failure text).
func (s *Service) checkNewPassword(ctx context.Context, password string) error {
	if err := s.cfg.PasswordPolicy.Validate(password); err != nil {
		return ErrPasswordTooWeak
	}
	breached, err := s.cfg.BreachChecker.IsBreached(ctx, password)
	if err != nil {
		return ErrInternal
	}
	if breached {
		return ErrPasswordBreached
	}
	return nil
}

// RedeemResult is the outcome of a successful first-time-login completion.
type RedeemResult struct {
	UserID identity.UserID
	Status identity.AccountState
}

// RedeemInvitationAndSetPassword completes Section 2's first-time-login
// flow in one operation: it redeems rawToken (single-use; a second
// redemption of the same token always fails, AAX-06) and, only if
// redemption succeeds, establishes newPassword as the account's permanent
// password. It never auto-creates a session: Section 2 step 5 states that
// "normal login applies going forward," so the caller must sign in
// separately afterward once the account is active (or remains in
// password_change_required awaiting administrator-driven MFA enrollment
// for a role that requires it, per Section 5 — Step 9B's own documented,
// intentional gate, unimplemented further here).
func (s *Service) RedeemInvitationAndSetPassword(ctx context.Context, rawToken, newPassword string, now time.Time) (RedeemResult, error) {
	if rawToken == "" {
		return RedeemResult{}, ErrTokenInvalid
	}
	if err := s.checkNewPassword(ctx, newPassword); err != nil {
		return RedeemResult{}, err
	}

	digest := invitation.Digest(rawToken)
	userID, err := s.store.RedeemInvitation(ctx, digest, now)
	switch {
	case errors.Is(err, identitystore.ErrTokenExpired):
		// identitystore.RedeemInvitation does not return the account id on
		// this path even when it briefly resolved one internally, so this
		// audit event necessarily has no known account (accountID 0),
		// mirroring login_failure's own "attempted... not proof an account
		// exists" allowance (see identityaudit.InvitationRedemptionFailed).
		s.recordAudit(ctx, identityaudit.InvitationRedemptionFailed, 0, 0, "", identityaudit.Metadata{"reason_code": "token_expired"}, now)
		return RedeemResult{}, ErrTokenExpired
	case errors.Is(err, identitystore.ErrTokenInvalid):
		s.recordAudit(ctx, identityaudit.InvitationRedemptionFailed, 0, 0, "", identityaudit.Metadata{"reason_code": "token_invalid"}, now)
		return RedeemResult{}, ErrTokenInvalid
	case err != nil:
		return RedeemResult{}, ErrInternal
	}

	hash, err := passwordpolicy.Hash(newPassword, s.cfg.HashParams)
	if err != nil {
		return RedeemResult{}, ErrInternal
	}
	newStatus, err := s.store.EstablishPassword(ctx, userID, hash, now)
	if err != nil {
		// The invitation is already consumed at this point; a failure here
		// (for example, an unexpected concurrent state change) cannot be
		// rolled back through this service's own two-call sequence. This
		// mirrors identitystore's own two-step design and is reported as a
		// server error rather than silently retried or hidden.
		return RedeemResult{}, ErrInternal
	}

	s.recordAudit(ctx, identityaudit.InvitationRedeemed, userID, 0, "", nil, now)
	return RedeemResult{UserID: userID, Status: newStatus}, nil
}

// RequestPasswordReset issues a single-use password-reset token for email
// if, and only if, it matches an eligible account (Section 4). The raw
// token is returned only to this trusted, in-process caller — never
// serialized into an HTTP response — matching Section 3/4's
// account-enumeration resistance rule: the caller (backend/internal/authhttp)
// must render the identical public response whether matched is true or
// false, and must discard rawToken before building that response.
func (s *Service) RequestPasswordReset(ctx context.Context, email string, now time.Time) (rawToken string, matched bool, err error) {
	rawToken, genErr := passwordreset.GenerateToken()
	if genErr != nil {
		return "", false, ErrInternal
	}
	digest := passwordreset.Digest(rawToken)
	expiresAt := now.Add(s.cfg.PasswordResetTTL)

	matched, userID, storeErr := s.store.RequestPasswordReset(ctx, email, 0, digest, expiresAt)
	if storeErr != nil {
		if errors.Is(storeErr, identitystore.ErrInput) {
			// Malformed email syntax: treated identically to "no match,"
			// never distinguished (Section 3/4 enumeration resistance).
			return "", false, nil
		}
		return "", false, ErrInternal
	}
	if !matched {
		return "", false, nil
	}
	s.recordAudit(ctx, identityaudit.PasswordResetRequested, userID, 0, "", nil, now)
	return rawToken, true, nil
}

// CompletePasswordReset redeems rawToken and sets newPassword as the
// account's permanent password (Section 4). Completing a reset always
// revokes every existing session for that account — the underlying
// identitystore.CompletePasswordReset call does this atomically as part of
// the same transaction, not as a separate step here.
func (s *Service) CompletePasswordReset(ctx context.Context, rawToken, newPassword string, now time.Time) (identity.UserID, error) {
	if rawToken == "" {
		return 0, ErrTokenInvalid
	}
	if err := s.checkNewPassword(ctx, newPassword); err != nil {
		return 0, err
	}
	hash, err := passwordpolicy.Hash(newPassword, s.cfg.HashParams)
	if err != nil {
		return 0, ErrInternal
	}

	digest := passwordreset.Digest(rawToken)
	userID, storeErr := s.store.CompletePasswordReset(ctx, digest, hash, now)
	switch {
	case errors.Is(storeErr, identitystore.ErrTokenExpired):
		return 0, ErrTokenExpired
	case errors.Is(storeErr, identitystore.ErrTokenInvalid), errors.Is(storeErr, identitystore.ErrInvalidTransition):
		// ErrInvalidTransition here means the account's status changed
		// (for example, suspended) between token issuance and completion;
		// this is folded into the generic invalid/used response rather
		// than revealed distinctly, since revealing it would confirm
		// current account state to a caller holding only a reset link.
		return 0, ErrTokenInvalid
	case storeErr != nil:
		return 0, ErrInternal
	}

	s.recordAudit(ctx, identityaudit.PasswordResetCompleted, userID, 0, "", nil, now)
	return userID, nil
}

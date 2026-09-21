package identityservice

import (
	"context"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityaudit"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/session"
)

// ResolveSession looks up rawToken and, if it currently authorizes access,
// touches its idle-timeout window and returns the resolved principal. This
// is the operation backend/internal/authhttp's session middleware calls on
// every authenticated request. Any failure — unknown, revoked, idle- or
// absolute-expired token, or a non-active owning account — returns the
// identical ErrSessionInvalid (Section 8: "fail closed").
func (s *Service) ResolveSession(ctx context.Context, rawToken string, now time.Time) (identitystore.Authorized, error) {
	if rawToken == "" {
		return identitystore.Authorized{}, ErrSessionInvalid
	}
	digest := session.Digest(rawToken)
	auth, err := s.store.ResolveSession(ctx, digest, s.cfg.Session, now)
	if err != nil {
		return identitystore.Authorized{}, ErrSessionInvalid
	}
	// Best-effort idle-window refresh; a failure here must not fail the
	// request that is otherwise already authorized.
	if err := s.store.TouchSession(ctx, auth.Session.ID, now); err != nil {
		s.logger.Warn("session_touch_failed")
	}
	return auth, nil
}

// Logout ends exactly one session — the current one — for the calling
// account (Section 8: "Logout"). sessionID must already be known to belong
// to userID (the caller resolved it from their own cookie), so no separate
// ownership check is performed here; see RevokeOwnedSession for the
// arbitrary-session-ID case.
func (s *Service) Logout(ctx context.Context, userID identity.UserID, sessionID session.ID, now time.Time) error {
	if err := s.store.RevokeSession(ctx, sessionID, session.RevokedLogout, now); err != nil {
		return ErrInternal
	}
	s.recordAudit(ctx, identityaudit.SessionRevoked, userID, 0, "", identityaudit.Metadata{"reason": string(session.RevokedLogout)}, now)
	return nil
}

// LogoutAll ends every active session for userID (Section 8: "Logout of all
// devices").
func (s *Service) LogoutAll(ctx context.Context, userID identity.UserID, now time.Time) (int64, error) {
	count, err := s.store.RevokeAllSessionsForUser(ctx, userID, session.RevokedLogoutAll, now)
	if err != nil {
		return 0, ErrInternal
	}
	s.recordAudit(ctx, identityaudit.SessionRevoked, userID, 0, "", identityaudit.Metadata{"reason": string(session.RevokedLogoutAll)}, now)
	return count, nil
}

// ListSessions returns every currently active session for userID (Section
// 8: "device/session listing"). It never returns another account's
// sessions: the store query itself is scoped by userID.
func (s *Service) ListSessions(ctx context.Context, userID identity.UserID) ([]session.Session, error) {
	sessions, err := s.store.ListActiveSessions(ctx, userID)
	if err != nil {
		return nil, ErrInternal
	}
	return sessions, nil
}

// RevokeOwnedSession revokes exactly one session, but only if it currently
// belongs to userID (Section 8: a user may revoke any ONE OF THEIR OWN
// devices, never another account's). Ownership is checked by listing the
// caller's own active sessions and requiring targetSessionID to appear
// among them, since the underlying identitystore.RevokeSession takes no
// owner parameter and would otherwise revoke any session by ID regardless
// of caller.
func (s *Service) RevokeOwnedSession(ctx context.Context, userID identity.UserID, targetSessionID session.ID, now time.Time) error {
	owned, err := s.store.ListActiveSessions(ctx, userID)
	if err != nil {
		return ErrInternal
	}
	found := false
	for _, sess := range owned {
		if sess.ID == targetSessionID {
			found = true
			break
		}
	}
	if !found {
		return ErrSessionNotOwned
	}
	if err := s.store.RevokeSession(ctx, targetSessionID, session.RevokedLogout, now); err != nil {
		return ErrInternal
	}
	s.recordAudit(ctx, identityaudit.SessionRevoked, userID, 0, "", identityaudit.Metadata{"reason": string(session.RevokedLogout)}, now)
	return nil
}

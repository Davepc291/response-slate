package authhttp

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"greenwich-fire-responder/backend/internal/identityservice"
	"greenwich-fire-responder/backend/internal/session"
)

type sessionView struct {
	ID         int64     `json:"id"`
	DeviceHint string    `json:"device_hint,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

// handleListSessions implements GET /api/auth/sessions (Section 8: "device/
// session listing"). It returns only the caller's own sessions: the
// underlying identityservice.ListSessions call is scoped by the caller's
// own user id, never a client-supplied one.
func (h *Handlers) handleListSessions(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
		return
	}
	sessions, err := h.svc.ListSessions(r.Context(), p.UserID)
	if err != nil {
		h.respondUnavailable(w)
		return
	}
	out := make([]sessionView, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionView{
			ID: int64(s.ID), DeviceHint: s.DeviceHint, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt,
			ExpiresAt: s.ExpiresAt, Current: s.ID == p.SessionID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevokeSession implements DELETE /api/auth/sessions/{id}. A session
// id that does not belong to the caller responds 404, identically to an
// unknown id, so a caller can never distinguish "not yours" from "does not
// exist" (Section 6/9's insecure-direct-object-reference principle).
func (h *Handlers) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "Session not found.")
		return
	}
	switch err := h.svc.RevokeOwnedSession(r.Context(), p.UserID, session.ID(id), h.clock()); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	case errors.Is(err, identityservice.ErrSessionNotOwned):
		writeError(w, http.StatusNotFound, "not_found", "Session not found.")
	default:
		h.respondUnavailable(w)
	}
}

package authhttp

import (
	"errors"
	"net/http"

	"greenwich-fire-responder/backend/internal/identityservice"
)

type passwordResetRequestBody struct {
	Email string `json:"email"`
}

// handlePasswordResetRequest implements POST /api/auth/password-reset
// (Section 3, Section 4). It always returns the identical public response
// whether or not the email matches an account: the raw token
// identityservice.RequestPasswordReset returns is discarded here and never
// serialized into the response.
func (h *Handlers) handlePasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	if !h.checkOrigin(w, r) {
		return
	}
	now := h.clock()
	ip := h.ipResolver.Resolve(r)
	if !h.resetRequest.Allow(ip, now) {
		h.respondLocked(w)
		return
	}

	var req passwordResetRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	if _, _, err := h.svc.RequestPasswordReset(r.Context(), req.Email, now); err != nil {
		h.respondUnavailable(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "If that email has an account, a reset link has been sent."})
}

type passwordResetCompleteBody struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// handlePasswordResetComplete implements POST
// /api/auth/password-reset/complete (Section 4). The raw token is supplied
// only inside the bounded JSON request body, never in the URL — see this
// package's Routes doc comment for the same no-tokens-in-URLs rule applied
// to first-time login.
func (h *Handlers) handlePasswordResetComplete(w http.ResponseWriter, r *http.Request) {
	if !h.checkOrigin(w, r) {
		return
	}
	now := h.clock()
	ip := h.ipResolver.Resolve(r)
	if !h.resetComplete.Allow(ip, now) {
		h.respondLocked(w)
		return
	}

	var req passwordResetCompleteBody
	if !decodeJSON(w, r, &req) {
		return
	}

	_, err := h.svc.CompletePasswordReset(r.Context(), req.Token, req.Password, now)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "password_reset"})
	case errors.Is(err, identityservice.ErrTokenExpired):
		writeError(w, http.StatusGone, "reset_link_expired", "This password reset link has expired. Request a new one.")
	case errors.Is(err, identityservice.ErrTokenInvalid):
		writeError(w, http.StatusBadRequest, "reset_link_invalid", "This password reset link is invalid or has already been used.")
	case errors.Is(err, identityservice.ErrPasswordTooWeak):
		writeError(w, http.StatusUnprocessableEntity, "password_too_weak", "Choose a longer password.")
	case errors.Is(err, identityservice.ErrPasswordBreached):
		writeError(w, http.StatusUnprocessableEntity, "password_breached", "This password has appeared in a data breach. Choose a different one.")
	default:
		h.respondUnavailable(w)
	}
}

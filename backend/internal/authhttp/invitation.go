package authhttp

import (
	"errors"
	"net/http"

	"greenwich-fire-responder/backend/internal/identityservice"
)

type firstTimeLoginRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// handleFirstTimeLogin implements POST /api/auth/first-time-login (Section
// 2, Section 11.3): invitation redemption plus mandatory permanent-password
// establishment, in one call. The raw token is supplied only inside the
// bounded JSON request body, never in the URL — see this package's Routes
// doc comment for why the contract's general no-tokens-in-URLs rule
// controls over Section 11.3's illustrative path-segment table.
func (h *Handlers) handleFirstTimeLogin(w http.ResponseWriter, r *http.Request) {
	if !h.checkOrigin(w, r) {
		return
	}
	now := h.clock()
	ip := h.ipResolver.Resolve(r)
	if !h.invitation.Allow(ip, now) {
		h.respondLocked(w)
		return
	}

	var req firstTimeLoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := h.svc.RedeemInvitationAndSetPassword(r.Context(), req.Token, req.Password, now)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": string(result.Status)})
	case errors.Is(err, identityservice.ErrTokenExpired):
		// Section 3's exact, deliberate exception-case wording.
		writeError(w, http.StatusGone, "invitation_expired", "This invitation has expired. Ask your administrator to resend it.")
	case errors.Is(err, identityservice.ErrTokenInvalid):
		writeError(w, http.StatusBadRequest, "invitation_invalid", "This invitation link is invalid or has already been used.")
	case errors.Is(err, identityservice.ErrPasswordTooWeak):
		writeError(w, http.StatusUnprocessableEntity, "password_too_weak", "Choose a longer password.")
	case errors.Is(err, identityservice.ErrPasswordBreached):
		writeError(w, http.StatusUnprocessableEntity, "password_breached", "This password has appeared in a data breach. Choose a different one.")
	default:
		h.respondUnavailable(w)
	}
}

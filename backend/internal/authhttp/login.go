package authhttp

import (
	"errors"
	"net/http"
	"strings"

	"greenwich-fire-responder/backend/internal/identityservice"
)

type loginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceHint string `json:"device_hint,omitempty"`
}

type meResponse struct {
	UserID      int64  `json:"user_id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Scope       string `json:"scope,omitempty"`
	Status      string `json:"status"`
}

// handleLogin implements POST /api/auth/login (Section 3, Section 11.3).
func (h *Handlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !h.checkOrigin(w, r) {
		return
	}
	now := h.clock()
	ip := h.ipResolver.Resolve(r)

	// Rate limiting happens before any body is parsed: an attacker already
	// over the threshold gets the identical locked response regardless of
	// what they send, and never causes extra password-hashing work.
	if !h.loginPerIP.Allow(ip, now) {
		h.respondLocked(w)
		return
	}

	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// The per-account key is a lowercase, trimmed best-effort form of the
	// submitted email — deliberately not identity.NormalizeEmail's stricter
	// validation, so a malformed email still consumes its own bounded quota
	// rather than falling through unlimited.
	accountKey := strings.ToLower(strings.TrimSpace(req.Email))
	if accountKey != "" && !h.loginPerAccount.Allow(accountKey, now) {
		h.respondLocked(w)
		return
	}

	result, err := h.svc.Login(r.Context(), req.Email, req.Password, req.DeviceHint, ip, now)
	switch {
	case err == nil:
		h.setSessionCookies(w, result.RawToken, result.ExpiresAt)
		writeJSON(w, http.StatusOK, meResponse{
			UserID: int64(result.Account.ID), Email: result.Account.Email, DisplayName: result.Account.DisplayName,
			Role: string(result.Account.Role), Scope: string(result.Account.Scope), Status: string(result.Account.Status),
		})
	case errors.Is(err, identityservice.ErrAccountSuspended):
		writeError(w, http.StatusForbidden, "account_suspended", "This account is suspended. Contact your administrator.")
	case errors.Is(err, identityservice.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "That email or password is incorrect.")
	default:
		h.respondUnavailable(w)
	}
}

// handleMe implements GET /api/auth/me. This exact route is not listed in
// the approved contract's Section 11.3 table; it is added here, narrowly,
// because requirement 2's "current authenticated user/session" operation
// has no other route to serve it from (see this package's Routes doc
// comment and the Step 9C implementation report for the explicit
// deviation).
func (h *Handlers) handleMe(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		UserID: int64(p.UserID), Email: p.Email, DisplayName: p.DisplayName,
		Role: string(p.Role), Scope: string(p.Scope), Status: string(p.Status),
	})
}

// handleLogout implements POST /api/auth/logout.
func (h *Handlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
		return
	}
	if err := h.svc.Logout(r.Context(), p.UserID, p.SessionID, h.clock()); err != nil {
		h.respondUnavailable(w)
		return
	}
	h.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

// handleLogoutAll implements POST /api/auth/logout-all.
func (h *Handlers) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
		return
	}
	if _, err := h.svc.LogoutAll(r.Context(), p.UserID, h.clock()); err != nil {
		h.respondUnavailable(w)
		return
	}
	// LogoutAll revokes the caller's own current session too, so the
	// browser's cookies are cleared exactly as for a single logout.
	h.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out_everywhere"})
}

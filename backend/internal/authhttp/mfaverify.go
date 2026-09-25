package authhttp

import (
	"encoding/json"
	"errors"
	"net/http"

	"greenwich-fire-responder/backend/internal/identityservice"
)

type mfaVerifyRequest struct {
	// Action is either "begin" or "finish", mirroring handleMFAEnroll's
	// two-phase, bounded-body shape for the identical reason: no challenge
	// or credential response ever appears in a URL.
	Action string `json:"action"`
	// Credential is the raw WebAuthn authentication response JSON, present
	// only for action "finish". It is forwarded byte-for-byte to
	// identityservice/mfa for verification and is never itself logged,
	// stored beyond that verification call, or placed in audit metadata.
	Credential json.RawMessage `json:"credential,omitempty"`
}

// handleMFAVerify implements POST /api/auth/mfa/verify (Section 5, Section
// 11.3; AAX-07): a WebAuthn authentication ceremony that upgrades the
// caller's own CURRENT session to MFA-verified. Unlike handleMFAEnroll,
// this route is reachable only through a normal, already-established
// session (requireSession) — verification is only ever meaningful for an
// already-active account that already holds a valid session; the Step
// 9F-3 enrollment bridging credential is never accepted here. A
// successful "finish" call takes effect only for the session that
// performed both halves of this ceremony (identityservice enforces the
// binding); no other session belonging to this account is affected.
func (h *Handlers) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
		return
	}
	var req mfaVerifyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rawToken, _ := r.Context().Value(ctxSessionToken).(string)
	if rawToken == "" {
		h.respondUnavailable(w)
		return
	}
	now := h.clock()
	switch req.Action {
	case "begin":
		assertion, err := h.svc.BeginMFALogin(r.Context(), p.UserID, rawToken, now)
		if err != nil {
			writeMFAVerifyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, assertion)
	case "finish":
		if len(req.Credential) == 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "credential is required to finish verification.")
			return
		}
		if err := h.svc.FinishMFALogin(r.Context(), p.UserID, p.SessionID, rawToken, req.Credential, now); err != nil {
			writeMFAVerifyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "mfa_verified"})
	default:
		writeError(w, http.StatusBadRequest, "invalid_action", `action must be "begin" or "finish".`)
	}
}

// writeMFAVerifyError maps every identityservice MFA sentinel to a safe,
// generic response, never echoing the underlying error's text.
func writeMFAVerifyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identityservice.ErrMFANotEligible):
		writeError(w, http.StatusConflict, "mfa_not_eligible", "This account has no enrolled passkey to verify.")
	case errors.Is(err, identityservice.ErrMFACeremonyInvalid):
		writeError(w, http.StatusBadRequest, "mfa_ceremony_invalid", "Start verification again before completing it.")
	case errors.Is(err, identityservice.ErrMFAResponseInvalid):
		writeError(w, http.StatusBadRequest, "mfa_response_invalid", "That passkey response could not be verified.")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable", "MFA verification is temporarily unavailable.")
	}
}

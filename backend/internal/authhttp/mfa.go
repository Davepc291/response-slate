package authhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"greenwich-fire-responder/backend/internal/identityservice"
)

type mfaEnrollRequest struct {
	// Action is either "begin" or "finish": both halves of the ceremony
	// share one route with a bounded, action-dispatched body, per the
	// contract's proposed POST /api/auth/mfa/enroll and its own general
	// rule against putting a challenge/credential/token in a URL.
	Action string `json:"action"`
	// Credential is the raw WebAuthn registration response JSON, present
	// only for action "finish". It is forwarded byte-for-byte to
	// identityservice/mfa for verification and is never itself logged,
	// stored beyond that verification call, or placed in audit metadata.
	Credential json.RawMessage `json:"credential,omitempty"`
}

// handleMFAEnroll implements POST /api/auth/mfa/enroll (Section 5, Section
// 11.3; AAX-07): both halves of a WebAuthn passkey registration ceremony
// behind one route. See requireSessionOrMFAEnrollment for the two distinct
// authenticated contexts this route accepts, and identityservice's mfa.go
// for the ceremony/eligibility rules enforced beneath it.
func (h *Handlers) handleMFAEnroll(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
		return
	}
	var req mfaEnrollRequest
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
		h.handleMFAEnrollBegin(w, r, p, rawToken, now)
	case "finish":
		h.handleMFAEnrollFinish(w, r, p, rawToken, req.Credential, now)
	default:
		writeError(w, http.StatusBadRequest, "invalid_action", `action must be "begin" or "finish".`)
	}
}

func (h *Handlers) handleMFAEnrollBegin(w http.ResponseWriter, r *http.Request, p Principal, rawToken string, now time.Time) {
	creation, err := h.svc.BeginMFAEnrollment(r.Context(), p.UserID, rawToken, now)
	if err != nil {
		writeMFAEnrollError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, creation)
}

func (h *Handlers) handleMFAEnrollFinish(w http.ResponseWriter, r *http.Request, p Principal, rawToken string, credential json.RawMessage, now time.Time) {
	if len(credential) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input", "credential is required to finish enrollment.")
		return
	}
	status, err := h.svc.FinishMFAEnrollment(r.Context(), p.UserID, rawToken, credential, now)
	if err != nil {
		writeMFAEnrollError(w, err)
		return
	}
	// The bridging enrollment cookie (if this request used one) has
	// fulfilled its purpose the moment enrollment succeeds, whether or not
	// it also activated the account; clearing it is always safe even when
	// this request was actually authenticated by a normal session instead.
	h.clearMFAEnrollCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": string(status)})
}

// writeMFAEnrollError maps every identityservice MFA sentinel to a safe,
// generic response, never echoing the underlying error's text.
func writeMFAEnrollError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identityservice.ErrMFANotEligible):
		writeError(w, http.StatusConflict, "mfa_not_eligible", "This account is not eligible for MFA enrollment.")
	case errors.Is(err, identityservice.ErrMFACeremonyInvalid):
		writeError(w, http.StatusBadRequest, "mfa_ceremony_invalid", "Start enrollment again before completing it.")
	case errors.Is(err, identityservice.ErrMFAResponseInvalid):
		writeError(w, http.StatusBadRequest, "mfa_response_invalid", "That passkey response could not be verified.")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable", "MFA enrollment is temporarily unavailable.")
	}
}

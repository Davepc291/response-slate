package authhttp

import (
	"context"
	"net/http"

	"greenwich-fire-responder/backend/internal/authcookie"
	"greenwich-fire-responder/backend/internal/authorization"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/session"
)

// requireSession resolves the secure session cookie, hashing/digesting the
// presented token before any store lookup happens inside
// identityservice.ResolveSession (Section 8, Section 6: authorization
// middleware). Every failure — missing cookie, unknown/revoked/expired
// token, or a non-active owning account — fails closed with the identical
// 401 response, never distinguishing why.
func (h *Handlers) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawToken, ok := authcookie.ReadSessionToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
			return
		}
		now := h.clock()
		auth, err := h.svc.ResolveSession(r.Context(), rawToken, now)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
			return
		}
		principal := Principal{
			UserID: auth.Account.ID, SessionID: auth.Session.ID, Role: auth.Role, Scope: auth.Scope,
			Email: auth.Account.Email, DisplayName: auth.Account.DisplayName, Status: auth.Account.Status,
		}
		ctx := context.WithValue(r.Context(), ctxPrincipal, principal)
		ctx = context.WithValue(ctx, ctxSessionToken, rawToken)
		next(w, r.WithContext(ctx))
	}
}

// checkOrigin validates the request's Origin header against the configured
// allow-list (Section 8's CSRF requirement: "SameSite alone is not the only
// defense... Validate allowed Origin/Host behavior"). It is applied to
// every state-changing route, including the pre-session login/first-time-
// login/password-reset endpoints, not only to already-authenticated ones:
// an attacker-controlled page attempting a cross-origin login or reset
// request is rejected here regardless of whether a session cookie exists
// yet.
func (h *Handlers) checkOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || !h.allowedOrigins[origin] {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "Request origin is not permitted.")
		return false
	}
	return true
}

// requireCSRF enforces the signed double-submit CSRF check for a
// state-changing, already-authenticated request. It must be applied after
// requireSession in the handler chain (so ctxSessionToken is present) and
// combines three independent checks: an allowed Origin, a CSRF header value
// cryptographically derived from the current session, and (defense in
// depth) agreement between that header and the readable CSRF cookie. A
// missing, malformed, or mismatched value at any of these fails the request
// with the same 403 response.
func (h *Handlers) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.checkOrigin(w, r) {
			return
		}
		rawToken, _ := r.Context().Value(ctxSessionToken).(string)
		if rawToken == "" {
			writeError(w, http.StatusForbidden, "csrf_validation_failed", "Request could not be verified.")
			return
		}
		digest := session.Digest(rawToken)
		header := r.Header.Get("X-CSRF-Token")
		if header == "" || !h.csrf.Verify(digest, header) {
			writeError(w, http.StatusForbidden, "csrf_validation_failed", "Request could not be verified.")
			return
		}
		cookieValue, ok := authcookie.ReadCSRFCookie(r)
		if !ok || cookieValue != header {
			writeError(w, http.StatusForbidden, "csrf_validation_failed", "Request could not be verified.")
			return
		}
		next(w, r)
	}
}

// RequirePermission is a reusable authorization middleware for a future
// protected route: it re-checks the Step 9B authorization engine on every
// request, defaulting to deny for an unknown role, permission, or scope,
// exactly like every other check in that package. It must be applied after
// requireSession. No route in Step 9C uses this yet — existing public
// synthetic routes are deliberately left unconverted — but it is exported
// here so a future phase can protect a real endpoint without re-implementing
// this check. Auditing an authorization denial from this middleware is
// deferred to whichever future route actually adopts it: the exact target
// resource/scope metadata a denial audit should carry depends on that
// route, which does not exist yet.
func (h *Handlers) RequirePermission(permission authorization.Permission, targetScope func(*http.Request) identity.Scope, targetUserID func(*http.Request) identity.UserID) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
				return
			}
			actor := authorization.Actor{UserID: p.UserID, Role: p.Role, Scope: p.Scope}
			var scope identity.Scope
			var target identity.UserID
			if targetScope != nil {
				scope = targetScope(r)
			}
			if targetUserID != nil {
				target = targetUserID(r)
			}
			if !authorization.Allowed(actor, permission, scope, target) {
				writeError(w, http.StatusForbidden, "forbidden", "You are not authorized to perform this action.")
				return
			}
			next(w, r)
		}
	}
}

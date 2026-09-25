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
			MFAVerified: auth.Session.MFAVerified(),
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

// requireSessionOrMFAEnrollment authenticates POST /api/auth/mfa/enroll
// under either of two distinct credentials: the narrow, short-lived
// MFA-enrollment cookie identityservice issues only when an administrator
// has just established a permanent password but has not yet completed MFA
// (see identityservice's mfa.go package doc comment for why the ordinary
// session machinery can never authenticate that account), or the normal
// browser session cookie (an already-active account adding another
// passkey). No other route accepts the first credential; requireSession
// itself is completely unmodified and used everywhere else. Both branches,
// and the final fallthrough, return the identical 401 response, never
// distinguishing which credential (if any) was presented.
//
// Step 9F-6 live-validation finding: the bridging cookie is checked FIRST,
// deliberately — not for symmetry with anything, but because both
// credentials share the single __Host-gfr_csrf cookie slot
// (setMFAEnrollCookies overwrites it, deriving the CSRF value from the
// bridging token's digest). A browser that happens to still hold a valid
// normal session cookie at the exact moment a bridging cookie is issued
// (for example: an administrator who redeemed a second account's
// invitation without signing out of their own session first) previously
// hit an unconditional session-cookie-first check here, which silently
// authenticated the request as the OTHER, unrelated session — producing a
// CSRF digest mismatch (since the CSRF cookie now carries the bridging
// token's derivation) that surfaced as a generic "session expired" error,
// and which would have silently misattributed the enrollment to the wrong
// account had the CSRF values coincidentally lined up. The bridging
// credential is narrow, single-purpose, and short-lived (15 minutes,
// mfaEnrollmentCredentialTTL) — a far stronger signal of specific intent
// than a general-purpose session cookie that persists for hours across
// unrelated activity — so it takes precedence whenever both are present
// and valid.
func (h *Handlers) requireSessionOrMFAEnrollment(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := h.clock()
		if rawToken, ok := authcookie.ReadMFAEnrollToken(r); ok {
			if mp, err := h.svc.ResolveMFAEnrollmentCredential(r.Context(), rawToken, now); err == nil {
				principal := Principal{
					UserID: mp.UserID, Role: mp.Role, Scope: mp.Scope,
					Email: mp.Email, DisplayName: mp.DisplayName, Status: mp.Status,
				}
				ctx := context.WithValue(r.Context(), ctxPrincipal, principal)
				ctx = context.WithValue(ctx, ctxSessionToken, rawToken)
				next(w, r.WithContext(ctx))
				return
			}
		}
		if rawToken, ok := authcookie.ReadSessionToken(r); ok {
			if auth, err := h.svc.ResolveSession(r.Context(), rawToken, now); err == nil {
				principal := Principal{
					UserID: auth.Account.ID, SessionID: auth.Session.ID, Role: auth.Role, Scope: auth.Scope,
					Email: auth.Account.Email, DisplayName: auth.Account.DisplayName, Status: auth.Account.Status,
					MFAVerified: auth.Session.MFAVerified(),
				}
				ctx := context.WithValue(r.Context(), ctxPrincipal, principal)
				ctx = context.WithValue(ctx, ctxSessionToken, rawToken)
				next(w, r.WithContext(ctx))
				return
			}
		}
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
	}
}

// requireAdminMFAVerified enforces Step 9F-4's second, independent
// condition for every /api/admin/* route, beyond the existing
// authenticated-session and role/scope authorization checks: an
// administrator's CURRENT SESSION must itself have completed a WebAuthn
// authentication ceremony (Section 5, AAX-07). An enrolled credential's
// mere existence is never sufficient — the contract requires "an
// MFA-verified current session," not merely "an MFA-capable account" — and
// neither is having merely supplied the correct password at login; both of
// those are necessary but explicitly not sufficient conditions this
// middleware refuses to conflate. Must be applied after requireSession (so
// a Principal is present) and before every admin handler.
//
// A non-administrator's request passes through unaffected here: it still
// reaches, and is denied by, the exact same adminservice authorization
// check as before this task, so its response is byte-for-byte unchanged.
// An administrator whose current session has not completed the ceremony
// fails closed with a distinct, safe-to-reveal 403 — safe because the
// caller has already authenticated as this specific administrator, so
// naming the missing step leaks nothing an attacker didn't already know
// merely by holding this session.
func (h *Handlers) requireAdminMFAVerified(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
			return
		}
		if p.Role.IsAdministrator() && !p.MFAVerified {
			writeError(w, http.StatusForbidden, "mfa_verification_required", "This action requires a passkey-verified session. Verify your passkey and try again.")
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

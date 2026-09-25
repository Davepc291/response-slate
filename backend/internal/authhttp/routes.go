package authhttp

import "net/http"

// Mux builds the authentication API's routes. Every pattern is registered
// with an explicit HTTP method, so Go's net/http ServeMux itself enforces
// strict method matching (a request to a registered path with the wrong
// method receives an automatic 405 with an Allow header, never silently
// falling through to a handler that assumed one method).
//
// Route surface deviations from docs/authentication-authorization-v1.md
// Section 11.3's illustrative proposed table:
//
//  1. GET /api/auth/me is added. Section 11.3's table has no "current
//     session" route at all, but this task's requirement 2 explicitly
//     requires a "current authenticated user/session" operation. This is
//     the narrowest addition that satisfies it without inventing anything
//     else Section 11.3 does not already describe elsewhere (an
//     authenticated identity/role/status lookup).
//  2. Section 11.3's table shows the invitation and password-reset tokens
//     as URL path segments (/api/auth/first-time-login/{token} and
//     /api/auth/password-reset/{token}). The contract's own general rule
//     ("Do not put credentials or tokens in URLs or query strings")
//     controls over that illustrative table, so both routes instead take
//     the token only inside the bounded JSON request body:
//     POST /api/auth/first-time-login (body: {token, password}) and
//     POST /api/auth/password-reset/complete (body: {token, password}).
//     Neither token ever appears in a URL, so it cannot reach browser
//     history or an intermediate proxy's access log through the request
//     line; this package's handlers also never log a request path, body,
//     or token value.
//
// MFA enrollment (Step 9F-3) and verification (Step 9F-4) are registered
// below as POST /api/auth/mfa/enroll and POST /api/auth/mfa/verify, but
// both stay functionally inert until a future, separately authorized
// deployment step calls identityservice.Service.SetMFAProvider: every call
// fails closed with a 503 until then, so this registration changes no
// observable behavior for a caller that never configures a provider (see
// requireSessionOrMFAEnrollment for the two distinct authenticated
// contexts /mfa/enroll accepts, and identityservice's mfa.go for why one
// of them is not the normal session cookie; /mfa/verify accepts only a
// normal session). Administrator user-management routes
// (/api/admin/users*) are a Step 9E addition, registered by
// registerAdminRoutes only when SetAdmin has been called: a caller that
// never calls SetAdmin (every Step 9C/9D test, and any deployment that has
// not separately wired up adminservice) serves the identical route set as
// before Step 9E. Every /api/admin/* route additionally requires an
// MFA-verified current session for an administrator caller (Step 9F-4,
// AAX-07; see requireAdminMFAVerified) — an enrolled credential's mere
// existence, or a correct password alone, has never been sufficient.
func (h *Handlers) Mux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/auth/first-time-login", h.handleFirstTimeLogin)
	mux.HandleFunc("POST /api/auth/password-reset", h.handlePasswordResetRequest)
	mux.HandleFunc("POST /api/auth/password-reset/complete", h.handlePasswordResetComplete)

	mux.HandleFunc("GET /api/auth/me", h.requireSession(h.handleMe))
	mux.HandleFunc("POST /api/auth/logout", h.requireSession(h.requireCSRF(h.handleLogout)))
	mux.HandleFunc("POST /api/auth/logout-all", h.requireSession(h.requireCSRF(h.handleLogoutAll)))
	mux.HandleFunc("GET /api/auth/sessions", h.requireSession(h.handleListSessions))
	mux.HandleFunc("DELETE /api/auth/sessions/{id}", h.requireSession(h.requireCSRF(h.handleRevokeSession)))

	mux.HandleFunc("POST /api/auth/mfa/enroll", h.requireSessionOrMFAEnrollment(h.requireCSRF(h.handleMFAEnroll)))
	mux.HandleFunc("POST /api/auth/mfa/verify", h.requireSession(h.requireCSRF(h.handleMFAVerify)))

	if h.admin != nil {
		h.registerAdminRoutes(mux)
	}

	return mux
}

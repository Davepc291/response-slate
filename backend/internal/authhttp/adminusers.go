package authhttp

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"greenwich-fire-responder/backend/internal/adminservice"
	"greenwich-fire-responder/backend/internal/authorization"
	"greenwich-fire-responder/backend/internal/identity"
)

// adminDefaultPageSize mirrors identitystore's own defaultListLimit, kept
// here only to echo a sensible "limit" value back in a list response when
// the caller did not specify one; the actual bound is enforced inside
// identitystore.ListUsersFilter.normalized, not here.
const adminDefaultPageSize = 25

// registerAdminRoutes wires the Step 9E administrator user-management
// surface (docs/authentication-authorization-v1.md Section 7, Section
// 11.3): explicit, narrow action routes rather than one unrestricted
// generic update endpoint, mirroring this file's own doc comment rationale.
// Every route requires, in order: a resolved session (requireSession), an
// MFA-verified current session for an administrator caller
// (requireAdminMFAVerified, Step 9F-4/AAX-07 — a non-administrator passes
// through unaffected and is denied by adminservice's own role/scope check
// exactly as before), and — for every state-changing route — CSRF
// (requireCSRF). No route here accepts an invitation, reset, password,
// session, or CSRF token in a path or query string — only a numeric,
// opaque user id.
func (h *Handlers) registerAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/users", h.requireSession(h.requireAdminMFAVerified(h.handleAdminListUsers)))
	mux.HandleFunc("POST /api/admin/users", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminCreateUser))))
	mux.HandleFunc("GET /api/admin/users/{id}", h.requireSession(h.requireAdminMFAVerified(h.handleAdminGetUser)))
	mux.HandleFunc("POST /api/admin/users/{id}/role", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminChangeRole))))
	mux.HandleFunc("POST /api/admin/users/{id}/resend-invitation", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminResendInvitation))))
	mux.HandleFunc("POST /api/admin/users/{id}/suspend", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminSuspend))))
	mux.HandleFunc("POST /api/admin/users/{id}/disable", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminDisable))))
	mux.HandleFunc("POST /api/admin/users/{id}/restore", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminRestore))))
	mux.HandleFunc("POST /api/admin/users/{id}/revoke-sessions", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminRevokeSessions))))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-credential", h.requireSession(h.requireAdminMFAVerified(h.requireCSRF(h.handleAdminResetCredential))))
}

// --- response/request shapes -------------------------------------------

type adminUserView struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	Scope       string     `json:"scope,omitempty"`
	Status      string     `json:"status"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func newAdminUserView(v identity.View) adminUserView {
	out := adminUserView{
		ID: int64(v.ID), Email: v.Email, DisplayName: v.DisplayName,
		Role: string(v.Role), Scope: string(v.Scope), Status: string(v.Status),
		CreatedAt: v.CreatedAt,
	}
	if !v.LastLoginAt.IsZero() {
		t := v.LastLoginAt
		out.LastLoginAt = &t
	}
	return out
}

type adminListUsersResponse struct {
	Users  []adminUserView `json:"users"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// oneTimeCodeWarning is the fixed, contract-required label (Step 9E
// requirement 3: "Clearly label the one-time response as sensitive") shown
// on every invitation/reset code response. The raw code itself is never
// returned by any other endpoint (list, get) after this one response.
const oneTimeCodeWarning = "Sensitive — shown once. This code will not be displayed again."

type adminInvitationCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	Sensitive bool      `json:"sensitive"`
	Warning   string    `json:"warning"`
}

func newInvitationCode(raw string, expiresAt time.Time) adminInvitationCode {
	return adminInvitationCode{Code: raw, ExpiresAt: expiresAt, Sensitive: true, Warning: oneTimeCodeWarning}
}

type adminCreateUserRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Scope       string `json:"scope"`
}

type adminCreateUserResponse struct {
	User       adminUserView       `json:"user"`
	Invitation adminInvitationCode `json:"invitation"`
}

type adminInvitationResponse struct {
	Invitation adminInvitationCode `json:"invitation"`
}

type adminChangeRoleRequest struct {
	Role  string `json:"role"`
	Scope string `json:"scope"`
}

type adminRevokeSessionsResponse struct {
	RevokedCount int64 `json:"revoked_count"`
}

type adminStatusResponse struct {
	Status string `json:"status"`
}

// --- shared helpers ------------------------------------------------------

func actorFromPrincipal(p Principal) authorization.Actor {
	return authorization.Actor{UserID: p.UserID, Role: p.Role, Scope: p.Scope}
}

// parsePathUserID validates the {id} path segment as a positive, opaque
// integer. A malformed value is rejected identically to an unknown id
// (both eventually surface as the same 404), so this function itself never
// distinguishes "not a number" from "no such account."
func parsePathUserID(r *http.Request) (identity.UserID, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return identity.UserID(id), true
}

// writeAdminError maps every adminservice sentinel to a safe, generic
// response. It never echoes the underlying error's text and never reveals
// which specific check failed beyond what the contract's own proposed
// validation messages already say safely (Section 7's screens table).
func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, adminservice.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "User not found.")
	case errors.Is(err, adminservice.ErrSelfAction):
		writeError(w, http.StatusForbidden, "self_action_forbidden", "You cannot perform this action on your own account.")
	case errors.Is(err, adminservice.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "You are not authorized to perform this action.")
	case errors.Is(err, adminservice.ErrConflict):
		writeError(w, http.StatusConflict, "email_conflict", "This email already has an account.")
	case errors.Is(err, adminservice.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "invalid_state", "This action is not permitted for the account's current state.")
	case errors.Is(err, adminservice.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "Check the submitted values and try again.")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable", "This action is temporarily unavailable.")
	}
}

func requirePrincipal(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	p, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_authenticated", "Sign-in required.")
	}
	return p, ok
}

func requireTargetID(w http.ResponseWriter, r *http.Request) (identity.UserID, bool) {
	id, ok := parsePathUserID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "User not found.")
	}
	return id, ok
}

// --- handlers --------------------------------------------------------------

// handleAdminListUsers implements GET /api/admin/users (Section 7: "Users
// (list)... Search/filter by name, email, role, status"). Pagination and
// every filter are bounded server-side (adminservice.ListUsers /
// identitystore.ListUsersFilter.normalized); a department administrator's
// results are always pinned to their own scope regardless of any
// caller-supplied scope query parameter.
func (h *Handlers) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	if v := q.Get("role"); v != "" && !identity.Role(v).Valid() {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "Unknown role filter.")
		return
	}
	if v := q.Get("status"); v != "" && !identity.AccountState(v).Valid() {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input", "Unknown status filter.")
		return
	}

	filter := adminservice.ListFilter{
		Scope:  identity.Scope(q.Get("scope")),
		Role:   identity.Role(q.Get("role")),
		Status: identity.AccountState(q.Get("status")),
		Search: q.Get("search"),
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_input", "limit must be a positive integer.")
			return
		}
		filter.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_input", "offset must be a non-negative integer.")
			return
		}
		filter.Offset = n
	}

	views, err := h.admin.ListUsers(r.Context(), actorFromPrincipal(p), filter)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	out := make([]adminUserView, 0, len(views))
	for _, v := range views {
		out = append(out, newAdminUserView(v))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = adminDefaultPageSize
	}
	writeJSON(w, http.StatusOK, adminListUsersResponse{Users: out, Limit: limit, Offset: filter.Offset})
}

// handleAdminGetUser implements GET /api/admin/users/{id} (Section 7: "User
// detail"). A target outside the caller's authorized scope renders
// identically to an unknown id (adminservice.ErrNotFound), never
// distinguished.
func (h *Handlers) handleAdminGetUser(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	view, err := h.admin.GetUser(r.Context(), actorFromPrincipal(p), id)
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newAdminUserView(view))
}

// handleAdminCreateUser implements POST /api/admin/users (Section 7: "Add
// user"). The raw invitation code is returned exactly once, in this
// response only, clearly labeled sensitive; no delivery of any kind is
// attempted (no approved provider exists).
func (h *Handlers) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var req adminCreateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := h.admin.CreateUser(r.Context(), actorFromPrincipal(p), adminservice.CreateUserParams{
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Role:        identity.Role(req.Role),
		Scope:       identity.Scope(req.Scope),
	}, h.clock())
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, adminCreateUserResponse{
		User:       newAdminUserView(result.View),
		Invitation: newInvitationCode(result.RawInvitationCode, result.InvitationExpiresAt),
	})
}

// handleAdminResendInvitation implements POST
// /api/admin/users/{id}/resend-invitation (Section 2/7: "Resend"). The new
// raw code is returned exactly once, identically to creation.
func (h *Handlers) handleAdminResendInvitation(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	result, err := h.admin.ResendInvitation(r.Context(), actorFromPrincipal(p), id, h.clock())
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminInvitationResponse{
		Invitation: newInvitationCode(result.RawInvitationCode, result.InvitationExpiresAt),
	})
}

// handleAdminSuspend implements POST /api/admin/users/{id}/suspend (Section
// 7: "Suspend"). An administrator can never suspend their own account.
func (h *Handlers) handleAdminSuspend(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	if err := h.admin.Suspend(r.Context(), actorFromPrincipal(p), id, h.clock()); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminStatusResponse{Status: string(identity.StateSuspended)})
}

// handleAdminDisable implements POST /api/admin/users/{id}/disable (Section
// 7: "Disable"). An administrator can never disable their own account.
func (h *Handlers) handleAdminDisable(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	if err := h.admin.Disable(r.Context(), actorFromPrincipal(p), id, h.clock()); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminStatusResponse{Status: string(identity.StateDisabled)})
}

// handleAdminRestore implements POST /api/admin/users/{id}/restore (Section
// 7: "Restore").
func (h *Handlers) handleAdminRestore(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	newState, err := h.admin.Restore(r.Context(), actorFromPrincipal(p), id, h.clock())
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminStatusResponse{Status: string(newState)})
}

// handleAdminRevokeSessions implements POST
// /api/admin/users/{id}/revoke-sessions (Section 7: "Revoke"). It never
// changes the account's enabled/disabled state. An administrator can never
// revoke their own sessions through this action.
func (h *Handlers) handleAdminRevokeSessions(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	count, err := h.admin.RevokeSessions(r.Context(), actorFromPrincipal(p), id, h.clock())
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminRevokeSessionsResponse{RevokedCount: count})
}

// handleAdminResetCredential implements POST
// /api/admin/users/{id}/reset-credential (Section 2/7: administrator
// "Reset"). Revokes every session and issues a fresh single-use credential,
// returned exactly once. An administrator can never reset their own
// account through this action.
func (h *Handlers) handleAdminResetCredential(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	result, err := h.admin.AdminReset(r.Context(), actorFromPrincipal(p), id, h.clock())
	if err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminInvitationResponse{
		Invitation: newInvitationCode(result.RawInvitationCode, result.InvitationExpiresAt),
	})
}

// handleAdminChangeRole implements POST /api/admin/users/{id}/role (Section
// 7, Section 10: "Role or scope change"). An administrator can never
// change their own role/scope through this action.
func (h *Handlers) handleAdminChangeRole(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	id, ok := requireTargetID(w, r)
	if !ok {
		return
	}
	var req adminChangeRoleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := h.admin.ChangeRole(r.Context(), actorFromPrincipal(p), id, identity.Role(req.Role), identity.Scope(req.Scope), h.clock()); err != nil {
		writeAdminError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, adminStatusResponse{Status: "updated"})
}

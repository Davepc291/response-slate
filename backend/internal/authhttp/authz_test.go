package authhttp

import (
	"net/http"
	"testing"

	"greenwich-fire-responder/backend/internal/authorization"
	"greenwich-fire-responder/backend/internal/identity"
)

// TestRequirePermissionDefaultDenies exercises the reusable
// RequirePermission middleware (not currently wired to any live route —
// see routes.go) against a protected stand-in handler, confirming it
// denies a role/permission combination absent from authorization's matrix
// and allows one that is present, matching that package's own
// default-deny posture (Section 6).
func TestRequirePermissionDefaultDenies(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("readonly@example.test", identity.RoleReadOnlyAuditor)
	sessionCookie, _ := hn.loginCookies("readonly@example.test")

	protected := hn.h.RequirePermission(authorization.ManageUsers, nil, nil)(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /protected-test-only", hn.h.requireSession(protected))

	req := hn.newRawRequest(http.MethodGet, "/protected-test-only", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	rec := hn.recordOn(mux, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected read-only auditor to be denied manage_users (default deny), got %d", rec.Code)
	}
}

func TestRequirePermissionAllowsGrantedRole(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("admin@example.test", identity.RoleSystemAdministrator)
	sessionCookie, _ := hn.loginCookies("admin@example.test")

	protected := hn.h.RequirePermission(authorization.ManageUsers, nil, nil)(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /protected-test-only", hn.h.requireSession(protected))

	req := hn.newRawRequest(http.MethodGet, "/protected-test-only", nil, reqOpts{cookies: []*http.Cookie{sessionCookie}})
	rec := hn.recordOn(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected a system administrator to be allowed manage_users, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequirePermissionRejectsUnauthenticated(t *testing.T) {
	hn := newHarness(t)
	protected := hn.h.RequirePermission(authorization.ManageUsers, nil, nil)(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /protected-test-only", protected) // no requireSession: simulates a missing principal

	req := hn.newRawRequest(http.MethodGet, "/protected-test-only", nil, reqOpts{})
	rec := hn.recordOn(mux, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no principal in context, got %d", rec.Code)
	}
}

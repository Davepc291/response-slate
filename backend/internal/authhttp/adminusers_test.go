package authhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityservice"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
)

func (hn *harness) seedUserWithScope(email string, role identity.Role, scope identity.Scope, status identity.AccountState) identity.UserID {
	hn.t.Helper()
	normalized, err := identity.NormalizeEmail(email)
	if err != nil {
		hn.t.Fatal(err)
	}
	var hash string
	if status == identity.StateActive {
		hash, err = passwordpolicy.Hash(testPassword, testHashParams())
		if err != nil {
			hn.t.Fatal(err)
		}
	}
	return hn.store.seedUser(identity.User{
		NormalizedEmail: normalized, DisplayName: "Synthetic Test User", Role: role, Scope: scope, Status: status,
		PasswordHash: hash,
	})
}

func TestAdminUsersRequireSession(t *testing.T) {
	hn := newHarness(t)
	routes := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/admin/users"},
		{http.MethodPost, "/api/admin/users"},
		{http.MethodGet, "/api/admin/users/1"},
		{http.MethodPost, "/api/admin/users/1/suspend"},
	}
	for _, r := range routes {
		rec := hn.request(r.method, r.path, nil, reqOpts{})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401 without a session, got %d: %s", r.method, r.path, rec.Code, rec.Body.String())
		}
	}
}

func TestAdminUsersDenyNonAdministrator(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("responder@example.test", identity.RoleResponder)
	sessionCookie, csrfCookie := hn.loginCookies("responder@example.test")

	rec := hn.request(http.MethodGet, "/api/admin/users", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-administrator, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminCreateUserRequiresCSRF(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("admin@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "admin@example.test")

	body := []byte(`{"email":"new@example.test","display_name":"New","role":"responder","scope":"engine-1"}`)
	rec := hn.request(http.MethodPost, "/api/admin/users", body, reqOpts{
		cookies: []*http.Cookie{sessionCookie, csrfCookie},
		// Deliberately no X-CSRF-Token header.
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without a CSRF header, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminCreateUserSuccessReturnsCodeOnceOnly(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("admin2@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "admin2@example.test")

	body := []byte(`{"email":"new2@example.test","display_name":"New Two","role":"responder","scope":"engine-1"}`)
	rec := hn.request(http.MethodPost, "/api/admin/users", body, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control: no-store, got %q", got)
	}

	var created adminCreateUserResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if created.Invitation.Code == "" {
		t.Fatal("expected a non-empty one-time invitation code")
	}
	if !created.Invitation.Sensitive {
		t.Fatal("expected the invitation code to be labeled sensitive")
	}

	// The raw code must never reappear on a subsequent GET.
	getRec := hn.request(http.MethodGet, "/api/admin/users/"+userIDStr(created.User.ID), nil, hn.authedOpts(sessionCookie, csrfCookie))
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on get, got %d: %s", getRec.Code, getRec.Body.String())
	}
	if strings.Contains(getRec.Body.String(), created.Invitation.Code) {
		t.Fatal("the raw invitation code must never be returned by GET /api/admin/users/{id}")
	}
}

func TestAdminCreateUserDuplicateEmailConflict(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("admin3@example.test", identity.RoleSystemAdministrator)
	hn.seedUserWithScope("dup@example.test", identity.RoleResponder, "engine-1", identity.StateActive)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "admin3@example.test")

	body := []byte(`{"email":"dup@example.test","display_name":"Dup","role":"responder","scope":"engine-1"}`)
	rec := hn.request(http.MethodPost, "/api/admin/users", body, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminGetUserOutOfScopeIsNotFound(t *testing.T) {
	hn := newHarness(t)
	deptAdmin := hn.seedUserWithScope("deptadmin@example.test", identity.RoleDepartmentAdministrator, "engine-1", identity.StateActive)
	other := hn.seedUserWithScope("other@example.test", identity.RoleResponder, "engine-2", identity.StateActive)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(deptAdmin, "deptadmin@example.test")

	rec := hn.request(http.MethodGet, "/api/admin/users/"+userIDStr(int64(other)), nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an out-of-scope target, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSuspendSelfForbidden(t *testing.T) {
	hn := newHarness(t)
	self := hn.seedActiveUser("self@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(self, "self@example.test")

	rec := hn.request(http.MethodPost, "/api/admin/users/"+userIDStr(int64(self))+"/suspend", []byte(`{}`), hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a self-suspend attempt, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSuspendAndRestore(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("admin4@example.test", identity.RoleSystemAdministrator)
	target := hn.seedUserWithScope("target@example.test", identity.RoleResponder, "engine-1", identity.StateActive)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "admin4@example.test")

	rec := hn.request(http.MethodPost, "/api/admin/users/"+userIDStr(int64(target))+"/suspend", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 suspending, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = hn.request(http.MethodPost, "/api/admin/users/"+userIDStr(int64(target))+"/restore", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 restoring, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminListUsersBoundsInvalidLimit(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("admin5@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "admin5@example.test")

	rec := hn.request(http.MethodGet, "/api/admin/users?limit=-1", nil, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a negative limit, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminUsersRejectUnknownFields(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("admin6@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "admin6@example.test")

	body := []byte(`{"email":"x@example.test","display_name":"X","role":"responder","scope":"engine-1","unexpected_field":true}`)
	rec := hn.request(http.MethodPost, "/api/admin/users", body, hn.authedOpts(sessionCookie, csrfCookie))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown JSON field, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminUsersWrongContentType(t *testing.T) {
	hn := newHarness(t)
	id := hn.seedActiveUser("admin7@example.test", identity.RoleSystemAdministrator)
	sessionCookie, csrfCookie := hn.loginAndVerifyMFACookies(id, "admin7@example.test")

	rec := hn.request(http.MethodPost, "/api/admin/users", []byte(`{}`), reqOpts{
		cookies:     []*http.Cookie{sessionCookie, csrfCookie},
		headers:     map[string]string{"X-CSRF-Token": csrfCookie.Value},
		contentType: "text/plain",
	})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminUsersNoRoutesWithoutSetAdmin confirms the additive-rollout
// guarantee documented on Handlers.SetAdmin: a Handlers value that never
// calls SetAdmin serves the identical Step 9C/9D route set as before Step
// 9E, with no /api/admin/* route registered at all.
func TestAdminUsersNoRoutesWithoutSetAdmin(t *testing.T) {
	store := newFakeStore()
	audit := &fakeAudit{}
	svc := identityservice.New(store, audit, testServiceConfig(), nil)
	handlers, err := New(svc, testAuthOptions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	handlers.clock = func() time.Time { return fixedNow }
	mux := handlers.Mux()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unregistered admin route, got %d", rec.Code)
	}
}

func userIDStr(id int64) string {
	return strconv.FormatInt(id, 10)
}

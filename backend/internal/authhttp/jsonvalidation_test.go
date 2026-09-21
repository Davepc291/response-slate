package authhttp

import (
	"net/http"
	"strings"
	"testing"

	"greenwich-fire-responder/backend/internal/identity"
)

func TestMalformedJSONRejected(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/login", []byte(`{"email":`), reqOpts{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d", rec.Code)
	}
}

func TestUnknownJSONFieldsRejected(t *testing.T) {
	hn := newHarness(t)
	hn.seedActiveUser("fields@example.test", identity.RoleResponder)
	rec := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"fields@example.test","password":"`+testPassword+`","is_admin":true}`), reqOpts{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown field, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOversizedRequestBodyRejected(t *testing.T) {
	hn := newHarness(t)
	oversized := `{"email":"a@example.test","password":"` + strings.Repeat("x", maxRequestBodyBytes+1024) + `"}`
	rec := hn.request(http.MethodPost, "/api/auth/login", []byte(oversized), reqOpts{})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for an oversized body, got %d", rec.Code)
	}
}

func TestWrongContentTypeRejected(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"a@example.test","password":"x"}`), reqOpts{contentType: "text/plain"})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415 for wrong Content-Type, got %d", rec.Code)
	}
}

func TestMissingContentTypeRejected(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"a@example.test","password":"x"}`), reqOpts{noContentType: true})
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415 for missing Content-Type, got %d", rec.Code)
	}
}

func TestMultipleJSONValuesRejected(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/login",
		[]byte(`{"email":"a@example.test","password":"x"}{"email":"b@example.test","password":"y"}`), reqOpts{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for multiple JSON values, got %d", rec.Code)
	}
}

func TestWrongMethodRejected(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodGet, "/api/auth/login", nil, reqOpts{})
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET on a POST-only route, got %d", rec.Code)
	}
}

func TestErrorResponsesNeverLeakInternalDetail(t *testing.T) {
	hn := newHarness(t)
	rec := hn.request(http.MethodPost, "/api/auth/login", []byte(`not json at all`), reqOpts{})
	body := rec.Body.String()
	for _, forbidden := range []string{"sql", "SELECT", "pgx", "panic", "goroutine", "stack trace", "database"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("error body leaked internal detail %q: %s", forbidden, body)
		}
	}
}

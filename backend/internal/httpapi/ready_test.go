package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/httpapi"
)

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReady(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   httpapi.DatabaseChecker
		code int
		body map[string]string
	}{
		{"ready", pingFunc(func(context.Context) error { return nil }), 200, map[string]string{"status": "ready", "database": "ok"}},
		{"unavailable", pingFunc(func(context.Context) error {
			return errors.New("postgres://user:secret@private-host/db: internal error")
		}), 503, map[string]string{"status": "not_ready", "database": "unavailable"}},
		{"unconfigured", nil, 503, map[string]string{"status": "not_ready", "database": "unavailable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			httpapi.NewHandler(tc.db).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ready", nil))
			if recorder.Code != tc.code {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.code)
			}
			if recorder.Header().Get("Content-Type") != "application/json" || recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("incorrect readiness headers")
			}
			var got map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.body) {
				t.Fatalf("unexpected readiness body: %s", recorder.Body.String())
			}
		})
	}
}

func TestReadyDeadlineAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db := pingFunc(func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("readiness ping needs a bounded deadline")
		}
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("request cancellation was not propagated")
		}
		return ctx.Err()
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/ready", nil).WithContext(ctx)
	httpapi.NewHandler(db).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}

func TestHealthDoesNotPingDatabase(t *testing.T) {
	db := pingFunc(func(context.Context) error {
		t.Fatal("liveness must not call the database")
		return nil
	})
	recorder := httptest.NewRecorder()
	httpapi.NewHandler(db).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestReadyRejectsPost(t *testing.T) {
	recorder := httptest.NewRecorder()
	httpapi.NewHandler(nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/ready", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

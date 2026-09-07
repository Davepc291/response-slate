package httpapi

import (
	"context"
	"encoding/json"
	"greenwich-fire-responder/backend/internal/operations"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type operationsFake struct{ value operations.Snapshot }

func (f operationsFake) Snapshot(context.Context) operations.Snapshot { return f.value }
func TestOperationsShapeAndFailure(t *testing.T) {
	for _, database := range []string{"ok", "unavailable"} {
		snapshot := operations.Snapshot{Status: "worker_disabled", Database: database, Worker: "disabled", Provider: "unknown", ObservedAt: time.Now().UTC()}
		if database == "ok" {
			snapshot.Metrics = &operations.Metrics{}
		}
		rec := httptest.NewRecorder()
		NewHandler(nil, operationsFake{snapshot}).ServeHTTP(rec, httptest.NewRequest("GET", "/api/operations/transcription", nil))
		code := 200
		if database == "unavailable" {
			code = 503
		}
		if rec.Code != code || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec)
		}
		var result map[string]any
		if json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result) != 7 {
			t.Fatal(rec.Body.String())
		}
		for _, key := range []string{"status", "database", "worker_state", "provider_state", "consecutive_provider_failures", "metrics", "observed_at"} {
			if _, ok := result[key]; !ok {
				t.Fatal(key)
			}
		}
		for _, forbidden := range []string{"transcript\"", "source_path", "filename", "radio_id", "\"rid\"", "password", "http://", "https://"} {
			if strings.Contains(rec.Body.String(), forbidden) {
				t.Fatal("leak", rec.Body.String())
			}
		}
	}
	rec := httptest.NewRecorder()
	NewHandler(nil).ServeHTTP(rec, httptest.NewRequest("POST", "/api/operations/transcription", nil))
	if rec.Code != 405 {
		t.Fatal(rec.Code)
	}
}

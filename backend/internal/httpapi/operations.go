package httpapi

import (
	"context"
	"encoding/json"
	"greenwich-fire-responder/backend/internal/operations"
	"net/http"
	"time"
)

type OperationsReader interface {
	Snapshot(context.Context) operations.Snapshot
}

func transcriptionOperations(reader OperationsReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := operations.Snapshot{Status: "database_unavailable", Database: "unavailable", Worker: "disabled", Provider: "unknown", ObservedAt: time.Now().UTC()}
		if reader != nil {
			s = reader.Snapshot(r.Context())
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if s.Database == "unavailable" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(s)
	}
}

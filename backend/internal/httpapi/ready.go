package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// DatabaseChecker keeps handlers independent of a PostgreSQL implementation.
type DatabaseChecker interface {
	Ping(context.Context) error
}

const readinessTimeout = 2 * time.Second

func ready(db DatabaseChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		response := struct {
			Status   string `json:"status"`
			Database string `json:"database"`
		}{Status: "not_ready", Database: "unavailable"}
		code := http.StatusServiceUnavailable
		if db != nil && db.Ping(ctx) == nil {
			response.Status = "ready"
			response.Database = "ok"
			code = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		// Do not expose database errors in responses or logs.
		_ = json.NewEncoder(w).Encode(response)
	}
}

// Package httpapi provides the API's HTTP routes and handlers.
package httpapi

import "net/http"

// NewHandler builds an independent HTTP handler for the API.
func NewHandler(db DatabaseChecker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", health)
	mux.HandleFunc("GET /api/ready", ready(db))
	return mux
}

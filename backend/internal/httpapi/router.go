// Package httpapi provides the API's HTTP routes and handlers.
package httpapi

import "net/http"

// NewHandler builds an independent HTTP handler for the API.
func NewHandler(db DatabaseChecker, readers ...OperationsReader) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", health)
	mux.HandleFunc("GET /api/ready", ready(db))
	var reader OperationsReader
	if len(readers) > 0 {
		reader = readers[0]
	}
	mux.HandleFunc("GET /api/operations/transcription", transcriptionOperations(reader))
	return mux
}

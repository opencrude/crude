// Package api serves the launch schedule over HTTP.
package api

import (
	"encoding/json"
	"net/http"
)

// NewRouter returns the HTTP routes of the service.
func NewRouter(s *Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/launches", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Upcoming(r.Context()))
	})
	return mux
}

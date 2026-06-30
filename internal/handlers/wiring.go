package handlers

import (
	"net/http"

	"github.com/mdhender/ecv4/internal/api"
)

// NewHTTPHandler registers the generated API routes on mux, wiring the strict
// server implementation through oapi-codegen's strict handler, and returns mux
// as an http.Handler. Callers may pre-register non-API routes (for example,
// GET /openapi.yaml) on mux before calling.
func NewHTTPHandler(server *Server, mux *http.ServeMux) http.Handler {
	strict := api.NewStrictHandler(server, nil)
	return api.HandlerFromMux(strict, mux)
}

package handlers

import (
	"net/http"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
)

// NewHTTPHandler registers the generated API routes on mux, wiring the strict
// server implementation through oapi-codegen's strict handler, and returns mux
// as an http.Handler. Callers may pre-register non-API routes (for example,
// GET /openapi.yaml) on mux before calling.
//
// verifier authenticates bearer tokens; it is applied only to operations the
// spec marks as secured (see requireBearer).
func NewHTTPHandler(server *Server, mux *http.ServeMux, verifier auth.Verifier) http.Handler {
	strict := api.NewStrictHandler(server, nil)
	return api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: []api.MiddlewareFunc{requireBearer(verifier)},
	})
}

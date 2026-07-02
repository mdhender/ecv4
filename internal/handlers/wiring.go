package handlers

import (
	"errors"
	"net/http"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/httputil"
)

// NewHTTPHandler registers the generated API routes on mux, wiring the strict
// server implementation through oapi-codegen's strict handler, and returns mux
// as an http.Handler. Callers may pre-register non-API routes (for example,
// GET /openapi.yaml) on mux before calling.
//
// verifier authenticates bearer tokens; it is applied only to operations the
// spec marks as secured (see requireBearer).
//
// The strict error handlers render the standard error envelope: a malformed
// request body becomes a 400, an unimplemented handler (errNotImplemented)
// becomes a 501, and any other handler error becomes a 500 without leaking its
// internal message.
func NewHTTPHandler(server *Server, mux *http.ServeMux, verifier auth.Verifier) http.Handler {
	strict := api.NewStrictHandlerWithOptions(server, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			httputil.WriteError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, errNotImplemented) {
				httputil.WriteError(w, r, http.StatusNotImplemented, "not_implemented", "this endpoint is not implemented yet")
				return
			}
			httputil.WriteError(w, r, http.StatusInternalServerError, "internal", "internal server error")
		},
	})
	apiHandler := api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: []api.MiddlewareFunc{requireBearer(verifier)},
	})

	// POST /admin/shutdown is a development-only capability that must be
	// *invisible* in a deployment that did not opt in with --development. The
	// operation is secured in the spec, so requireBearer would otherwise answer
	// an unauthenticated probe with 401 (and a wrong-method probe with 405) —
	// both of which leak that the route exists, unlike a genuinely unknown path
	// which 404s at the mux. When the route is disabled, hide it ahead of the
	// router so every method and every caller sees the same 404 as any unknown
	// path. When enabled, the normal secured flow (401/403/202) applies.
	if server.shutdown == nil {
		return hideRoute(apiHandler, "/admin/shutdown")
	}
	return apiHandler
}

// hideRoute makes requests to an exact path indistinguishable from requests to
// an unregistered one: it answers with the same 404 the mux gives any unknown
// path, for every method and regardless of authentication, before next (the API
// router and its auth middleware) can run. This keeps a gated-off route from
// leaking its existence via a 401/403/405 to unauthenticated probes.
func hideRoute(next http.Handler, path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

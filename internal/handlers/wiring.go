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
			httputil.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, errNotImplemented) {
				httputil.WriteError(w, http.StatusNotImplemented, "not_implemented", "this endpoint is not implemented yet")
				return
			}
			httputil.WriteError(w, http.StatusInternalServerError, "internal", "internal server error")
		},
	})
	return api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: []api.MiddlewareFunc{requireBearer(verifier)},
	})
}

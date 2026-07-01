package handlers

import (
	"net/http"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
)

// requireBearer returns a per-operation middleware that enforces bearer-token
// authentication on exactly the operations the OpenAPI spec marks as secured.
//
// The generated operation wrappers set the api.BearerAuthScopes context value
// before these middlewares run, but only for operations that carry a security
// requirement. Public operations (for example /healthz and /version, which
// declare `security: []`) never set it, so they pass straight through. Reading
// the marker here keeps the spec the single source of truth for which routes
// require a token, rather than maintaining a separate allow-list.
func requireBearer(verifier auth.Verifier) api.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, secured := r.Context().Value(api.BearerAuthScopes).([]string); secured {
				auth.RequireBearerJWT(verifier, next).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

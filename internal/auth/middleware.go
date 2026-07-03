package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/mdhender/ecv4/internal/httputil"
)

// Verifier verifies a raw Bearer token and returns application claims.
// Implement this with your chosen JWT package.
type Verifier interface {
	Verify(rawToken string) (Claims, error)
}

// VerifierFunc adapts a function to Verifier.
type VerifierFunc func(rawToken string) (Claims, error)

func (fn VerifierFunc) Verify(rawToken string) (Claims, error) { return fn(rawToken) }

// RequireBearerJWT verifies Authorization: Bearer <token> and attaches Claims
// to the request context.
func RequireBearerJWT(verifier Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		if authz == "" {
			httputil.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "missing Authorization header")
			return
		}

		prefix := "Bearer "
		if !strings.HasPrefix(authz, prefix) {
			httputil.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Authorization header must use Bearer scheme")
			return
		}

		claims, err := verifier.Verify(strings.TrimSpace(strings.TrimPrefix(authz, prefix)))
		if err != nil {
			httputil.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "invalid access token")
			return
		}
		if !claims.ExpiresAt.IsZero() && time.Now().After(claims.ExpiresAt) {
			httputil.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "expired access token")
			return
		}

		// An impersonation token acts as claims.UserID on behalf of claims.Actor.
		// Advertise the effective subject on every response so the impersonation
		// is visible (even from curl), and record actor+subject for the request
		// log. Authorization downstream still uses claims.UserID (the target).
		if claims.Impersonated() {
			w.Header().Set(httputil.ImpersonatedSubjectHeader, claims.Subject)
			httputil.SetImpersonation(r.Context(), claims.Actor, claims.UserID)
		}

		next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
	})
}

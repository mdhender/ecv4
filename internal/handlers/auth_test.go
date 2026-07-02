package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mdhender/ecv4/internal/auth"
)

// rejectAll is a verifier that fails every token, standing in for the real JWT
// verifier that is not wired yet.
var rejectAll = auth.VerifierFunc(func(string) (auth.Claims, error) {
	return auth.Claims{}, errors.New("reject")
})

// newTestHandler builds the routed handler with a nil store; the auth tests
// exercise only the middleware, which runs before any handler touches the store.
func newTestHandler(verifier auth.Verifier) http.Handler {
	return NewHTTPHandler(NewServer(nil, nil), http.NewServeMux(), verifier)
}

func get(t *testing.T, h http.Handler, path, authz string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestPublicRouteSkipsAuth(t *testing.T) {
	// /healthz is public (security: []) and needs no store, so it answers 200
	// with no token even though the verifier would reject one.
	if rr := get(t, newTestHandler(rejectAll), "/healthz", ""); rr.Code != http.StatusOK {
		t.Fatalf("GET /healthz: got %d, want 200", rr.Code)
	}
}

func TestSecuredRouteRequiresToken(t *testing.T) {
	if rr := get(t, newTestHandler(rejectAll), "/me", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me without token: got %d, want 401", rr.Code)
	}
}

func TestSecuredRouteRejectsInvalidToken(t *testing.T) {
	if rr := get(t, newTestHandler(rejectAll), "/me", "Bearer whatever"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me with rejected token: got %d, want 401", rr.Code)
	}
}

func TestSecuredRouteAcceptsValidToken(t *testing.T) {
	accept := auth.VerifierFunc(func(string) (auth.Claims, error) {
		return auth.Claims{UserID: 1, Username: "demo", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	// A valid token clears the middleware. Target /games/1/turns — a still-stubbed
	// secured route that does not touch the store — so this stays a test of the
	// auth middleware, not of any handler. The point is that it is NOT 401.
	if rr := get(t, newTestHandler(accept), "/games/1/turns", "Bearer good"); rr.Code == http.StatusUnauthorized {
		t.Fatalf("GET /games/1/turns with valid token was rejected (401); auth should have passed it through")
	}
}

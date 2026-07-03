package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
)

// newAuthLifecycle wires a handler and its Server over one shared store and
// token service, seeds an active account, and returns both: the handler for the
// public/secured HTTP routes, the Server for driving Login directly.
func newAuthLifecycle(t *testing.T) (http.Handler, *Server) {
	t.Helper()
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 5, "user@example.com", "s3cret-pass", true)
	tokens := testTokens()
	srv := NewServer(st, tokens)
	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)
	return handler, srv
}

// loginTokens logs the seeded account in and returns the issued tokens.
func loginTokens(t *testing.T, srv *Server) api.Login200JSONResponse {
	t.Helper()
	resp, is := login(t, srv, "user@example.com", "s3cret-pass").(api.Login200JSONResponse)
	if !is {
		t.Fatalf("login did not succeed: %T", resp)
	}
	return resp
}

// refreshHTTP posts refreshToken to the public /auth/refresh route.
func refreshHTTP(t *testing.T, handler http.Handler, refreshToken string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, handler, http.MethodPost, "/auth/refresh", "", map[string]any{"refreshToken": refreshToken})
}

func TestRefreshRotatesTokens(t *testing.T) {
	handler, srv := newAuthLifecycle(t)
	tok := loginTokens(t, srv)

	rr := refreshHTTP(t, handler, tok.RefreshToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("refresh: got %d, want 200 (body %q)", rr.Code, rr.Body.String())
	}
	var out api.AuthTokens
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		t.Fatal("expected non-empty rotated tokens")
	}
	if out.RefreshToken == tok.RefreshToken {
		t.Fatal("refresh token should be rotated, not reissued unchanged")
	}
	if out.TokenType != api.AuthTokensTokenTypeBearer {
		t.Fatalf("tokenType = %q, want Bearer", out.TokenType)
	}
}

func TestRefreshRotatedTokenStaysValid(t *testing.T) {
	handler, srv := newAuthLifecycle(t)
	tok := loginTokens(t, srv)

	// The freshly rotated token can itself be used to rotate again.
	rr := refreshHTTP(t, handler, tok.RefreshToken)
	var rotated api.AuthTokens
	if err := json.Unmarshal(rr.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rr2 := refreshHTTP(t, handler, rotated.RefreshToken); rr2.Code != http.StatusOK {
		t.Fatalf("second refresh: got %d, want 200 (body %q)", rr2.Code, rr2.Body.String())
	}
}

func TestRefreshReusedTokenRevokesFamily(t *testing.T) {
	handler, srv := newAuthLifecycle(t)
	tok := loginTokens(t, srv)

	// Rotate once, capturing the new (currently valid) token.
	rr := refreshHTTP(t, handler, tok.RefreshToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("first refresh: got %d, want 200", rr.Code)
	}
	var rotated api.AuthTokens
	if err := json.Unmarshal(rr.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Presenting the old, already-rotated token is the reuse signal: 401.
	if rr2 := refreshHTTP(t, handler, tok.RefreshToken); rr2.Code != http.StatusUnauthorized {
		t.Fatalf("reused token: got %d, want 401 (body %q)", rr2.Code, rr2.Body.String())
	}

	// And the whole family is now revoked, so even the valid rotated token is
	// rejected.
	if rr3 := refreshHTTP(t, handler, rotated.RefreshToken); rr3.Code != http.StatusUnauthorized {
		t.Fatalf("token after family revocation: got %d, want 401 (body %q)", rr3.Code, rr3.Body.String())
	}
}

func TestRefreshGarbageTokenIs401(t *testing.T) {
	handler, _ := newAuthLifecycle(t)
	if rr := refreshHTTP(t, handler, "not-a-jwt"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("garbage token: got %d, want 401", rr.Code)
	}
}

func TestRefreshMissingTokenIs401(t *testing.T) {
	handler, _ := newAuthLifecycle(t)
	if rr := refreshHTTP(t, handler, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("empty token: got %d, want 401", rr.Code)
	}
}

func TestRefreshExpiredTokenIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 5, "user@example.com", "pw-secret", true)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := base
	tokens := auth.NewTokenService([]byte("0123456789abcdef0123456789abcdef"),
		15*time.Minute, time.Hour, auth.WithClock(func() time.Time { return clock }))
	srv := NewServer(st, tokens)

	resp := login(t, srv, "user@example.com", "pw-secret").(api.Login200JSONResponse)

	clock = base.Add(2 * time.Hour) // past the 1-hour refresh TTL
	out, err := srv.RefreshToken(context.Background(), api.RefreshTokenRequestObject{
		Body: &api.RefreshTokenJSONRequestBody{RefreshToken: resp.RefreshToken},
	})
	if err != nil {
		t.Fatalf("RefreshToken returned error: %v", err)
	}
	if _, is := out.(api.RefreshToken401JSONResponse); !is {
		t.Fatalf("expired refresh: got %T, want RefreshToken401JSONResponse", out)
	}
}

func TestLogoutRevokesPresentedFamily(t *testing.T) {
	handler, srv := newAuthLifecycle(t)
	tok := loginTokens(t, srv)

	rr := doJSON(t, handler, http.MethodPost, "/auth/logout", tok.AccessToken,
		map[string]any{"refreshToken": tok.RefreshToken})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("logout: got %d, want 204 (body %q)", rr.Code, rr.Body.String())
	}

	// After logout the refresh token no longer refreshes.
	if rr2 := refreshHTTP(t, handler, tok.RefreshToken); rr2.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout: got %d, want 401 (body %q)", rr2.Code, rr2.Body.String())
	}
}

func TestLogoutWithoutTokenRevokesEverySession(t *testing.T) {
	handler, srv := newAuthLifecycle(t)
	// Two independent logins are two families for the same account.
	first := loginTokens(t, srv)
	second := loginTokens(t, srv)

	// Logout with no refresh token in the body logs the caller out everywhere.
	rr := doJSON(t, handler, http.MethodPost, "/auth/logout", first.AccessToken, map[string]any{})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("logout-everywhere: got %d, want 204 (body %q)", rr.Code, rr.Body.String())
	}

	for label, rt := range map[string]string{"first": first.RefreshToken, "second": second.RefreshToken} {
		if r := refreshHTTP(t, handler, rt); r.Code != http.StatusUnauthorized {
			t.Fatalf("%s session refresh after logout-everywhere: got %d, want 401", label, r.Code)
		}
	}
}

func TestLogoutRequiresAuth(t *testing.T) {
	handler, _ := newAuthLifecycle(t)
	// No bearer token: the secured route rejects the request at the middleware.
	rr := doJSON(t, handler, http.MethodPost, "/auth/logout", "", map[string]any{})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("logout without token: got %d, want 401", rr.Code)
	}
}

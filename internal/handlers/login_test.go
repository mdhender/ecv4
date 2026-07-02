package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
)

// insertAccountWithPassword inserts an account whose stored secret is the bcrypt
// hash of password (using the cheapest cost, for test speed).
func insertAccountWithPassword(t *testing.T, pool *sqlitemigration.Pool, id int64, email, password string, isActive bool) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer pool.Put(conn)
	err = sqlitex.Execute(conn,
		"INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(?, ?, 0, ?, ?);",
		&sqlitex.ExecOptions{Args: []any{id, email, b2i(isActive), string(hash)}})
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
}

func testTokens() *auth.TokenService {
	return auth.NewTokenService([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute, time.Hour)
}

func login(t *testing.T, srv *Server, username, password string) api.LoginResponseObject {
	t.Helper()
	resp, err := srv.Login(context.Background(), api.LoginRequestObject{
		Body: &api.LoginJSONRequestBody{Username: username, Password: password},
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	return resp
}

func TestLoginSuccessIssuesTokens(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 5, "user@example.com", "s3cret-pass", true)
	srv := NewServer(st, testTokens())

	// Mixed-case username exercises the lower-casing normalization.
	resp := login(t, srv, "USER@Example.com", "s3cret-pass")
	ok, is := resp.(api.Login200JSONResponse)
	if !is {
		t.Fatalf("got %T, want Login200JSONResponse (401?)", resp)
	}
	if ok.AccessToken == "" || ok.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens")
	}
	if ok.TokenType != api.Bearer {
		t.Fatalf("tokenType = %q, want Bearer", ok.TokenType)
	}
	if ok.ExpiresInSeconds != 900 {
		t.Fatalf("expiresInSeconds = %d, want 900", ok.ExpiresInSeconds)
	}
}

func TestLoginWrongPasswordIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 5, "user@example.com", "right", true)
	srv := NewServer(st, testTokens())
	if _, is := login(t, srv, "user@example.com", "wrong").(api.Login401JSONResponse); !is {
		t.Fatal("expected 401 for wrong password")
	}
}

func TestLoginUnknownUserIs401(t *testing.T) {
	st, _ := seedStore(t)
	srv := NewServer(st, testTokens())
	if _, is := login(t, srv, "nobody@example.com", "x").(api.Login401JSONResponse); !is {
		t.Fatal("expected 401 for unknown user")
	}
}

func TestLoginInactiveAccountIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 5, "user@example.com", "pw", false)
	srv := NewServer(st, testTokens())
	if _, is := login(t, srv, "user@example.com", "pw").(api.Login401JSONResponse); !is {
		t.Fatal("expected 401 for inactive account")
	}
}

// TestLoginRejectionsAreIndistinguishable documents the anti-enumeration
// property: unknown email, inactive account, and wrong password all return the
// same 401 with the same generic message. The handler additionally runs a
// throwaway bcrypt comparison on the unknown/inactive paths so their timing
// matches a real login (timing itself is not asserted here — the equalized code
// path is reviewed in Login — but this locks the observable response in place).
func TestLoginRejectionsAreIndistinguishable(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 1, "active@example.com", "correct-pw", true)
	insertAccountWithPassword(t, pool, 2, "inactive@example.com", "correct-pw", false)
	srv := NewServer(st, testTokens())

	cases := []struct {
		name, username, password string
	}{
		{"unknown user", "nobody@example.com", "correct-pw"},
		{"inactive account", "inactive@example.com", "correct-pw"},
		{"wrong password", "active@example.com", "wrong-pw"},
	}
	for _, tc := range cases {
		resp := login(t, srv, tc.username, tc.password)
		got, is := resp.(api.Login401JSONResponse)
		if !is {
			t.Fatalf("%s: got %T, want Login401JSONResponse", tc.name, resp)
		}
		if got.Code != "unauthorized" || got.Message != "invalid username or password" {
			t.Fatalf("%s: got code=%q message=%q, want the generic 401", tc.name, got.Code, got.Message)
		}
	}
}

// TestLoginThenMe is the end-to-end proof: log in to get a real token, then use
// it against /me through the full HTTP handler (routing + auth middleware +
// verifier + GetMe), which must return the account.
func TestLoginThenMe(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 7, "player@example.com", "hunter2", true)
	tokens := testTokens()
	srv := NewServer(st, tokens)

	resp := login(t, srv, "player@example.com", "hunter2").(api.Login200JSONResponse)

	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+resp.AccessToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /me with issued token: got %d, want 200 (body %q)", rr.Code, rr.Body.String())
	}
	var me api.MeResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /me body: %v", err)
	}
	if me.User.Id != 7 || me.User.Username != "player@example.com" {
		t.Fatalf("unexpected /me user: %+v", me.User)
	}
}

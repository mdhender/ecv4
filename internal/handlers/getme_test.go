package handlers

import (
	"context"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

// seedStore builds an isolated in-memory database, applies migrations, and
// returns a Store plus a helper to insert accounts.
func seedStore(t *testing.T) (*store.Store, *sqlitemigration.Pool) {
	t.Helper()
	pool, err := database.CreateSharedMemory(context.Background(), "")
	if err != nil {
		t.Fatalf("CreateSharedMemory: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return store.New(pool), pool
}

func insertAccount(t *testing.T, pool *sqlitemigration.Pool, id int64, email string, isAdmin, isActive bool) {
	t.Helper()
	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer pool.Put(conn)
	err = sqlitex.Execute(conn,
		"INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(?, ?, ?, ?, 'x');",
		&sqlitex.ExecOptions{Args: []any{id, email, b2i(isAdmin), b2i(isActive)}})
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// callGetMe invokes the handler directly with claims placed in the context,
// bypassing the HTTP/auth layer to exercise GetMe against the store.
func callGetMe(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool) api.GetMeResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).GetMe(ctx, api.GetMeRequestObject{})
	if err != nil {
		t.Fatalf("GetMe returned error: %v", err)
	}
	return resp
}

func TestGetMeReturnsAccount(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 42, "player@example.com", false, true)

	resp := callGetMe(t, st, auth.Claims{UserID: 42}, true)
	ok, is := resp.(api.GetMe200JSONResponse)
	if !is {
		t.Fatalf("got %T, want GetMe200JSONResponse (401?)", resp)
	}
	if ok.User.Id != 42 || ok.User.Username != "player@example.com" {
		t.Fatalf("unexpected user: %+v", ok.User)
	}
	if len(ok.User.Roles) != 1 || ok.User.Roles[0] != api.Player {
		t.Fatalf("roles = %v, want [player]", ok.User.Roles)
	}
}

func TestGetMeAdminRole(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "admin@example.com", true, true)

	resp := callGetMe(t, st, auth.Claims{UserID: 1}, true)
	ok, is := resp.(api.GetMe200JSONResponse)
	if !is {
		t.Fatalf("got %T, want 200", resp)
	}
	if len(ok.User.Roles) != 1 || ok.User.Roles[0] != api.Admin {
		t.Fatalf("roles = %v, want [admin]", ok.User.Roles)
	}
}

func TestGetMeNoClaimsIs401(t *testing.T) {
	st, _ := seedStore(t)
	if _, is := callGetMe(t, st, auth.Claims{}, false).(api.GetMe401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

func TestGetMeUnknownAccountIs401(t *testing.T) {
	st, _ := seedStore(t)
	if _, is := callGetMe(t, st, auth.Claims{UserID: 999}, true).(api.GetMe401JSONResponse); !is {
		t.Fatal("expected 401 when the account does not exist")
	}
}

func TestGetMeInactiveAccountIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 7, "gone@example.com", false, false)
	if _, is := callGetMe(t, st, auth.Claims{UserID: 7}, true).(api.GetMe401JSONResponse); !is {
		t.Fatal("expected 401 when the account is inactive")
	}
}

package handlers

import (
	"context"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// seedExec runs a statement against the pool, failing the test on error. It lets
// these tests seed games and memberships, which the store has no writer for.
func seedExec(t *testing.T, pool *sqlitemigration.Pool, query string, args ...any) {
	t.Helper()
	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer pool.Put(conn)
	if err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args}); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// callListMyGames invokes the handler directly with claims placed in the
// context, bypassing the HTTP/auth layer to exercise ListMyGames.
func callListMyGames(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool) api.ListMyGamesResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).ListMyGames(ctx, api.ListMyGamesRequestObject{})
	if err != nil {
		t.Fatalf("ListMyGames returned error: %v", err)
	}
	return resp
}

func TestListMyGamesReturnsMemberships(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "me@example.com", false, true)
	insertAccount(t, pool, 2, "other@example.com", false, true)

	seedExec(t, pool, "INSERT INTO games(id, code, is_active) VALUES(10, 'alpha', 1);")
	seedExec(t, pool, "INSERT INTO games(id, code, is_active) VALUES(20, 'beta', 0);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 1, 'Overlord', 1, 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(20, 1, 'Rome', 0, 1);")
	// A dropped membership and a bystander's membership must not appear.
	seedExec(t, pool, "INSERT INTO games(id, code, is_active) VALUES(30, 'gamma', 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(30, 1, 'Carthage', 0, 0);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 2, 'Egypt', 0, 1);")

	resp := callListMyGames(t, st, auth.Claims{UserID: 1}, true)
	ok, is := resp.(api.ListMyGames200JSONResponse)
	if !is {
		t.Fatalf("got %T, want ListMyGames200JSONResponse", resp)
	}
	if len(ok.Games) != 2 {
		t.Fatalf("got %d games, want 2: %+v", len(ok.Games), ok.Games)
	}
	want := []api.MyGame{
		{Id: 10, Slug: "alpha", IsActive: true, Handle: "Overlord", IsGm: true},
		{Id: 20, Slug: "beta", IsActive: false, Handle: "Rome", IsGm: false},
	}
	for i := range want {
		if ok.Games[i] != want[i] {
			t.Fatalf("game %d = %+v, want %+v", i, ok.Games[i], want[i])
		}
	}
}

func TestListMyGamesEmpty(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 5, "loner@example.com", false, true)

	resp := callListMyGames(t, st, auth.Claims{UserID: 5}, true)
	ok, is := resp.(api.ListMyGames200JSONResponse)
	if !is {
		t.Fatalf("got %T, want 200", resp)
	}
	if len(ok.Games) != 0 {
		t.Fatalf("got %d games, want 0", len(ok.Games))
	}
}

func TestListMyGamesNoClaimsIs401(t *testing.T) {
	st, _ := seedStore(t)
	if _, is := callListMyGames(t, st, auth.Claims{}, false).(api.ListMyGames401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

func TestListMyGamesUnknownAccountIs401(t *testing.T) {
	st, _ := seedStore(t)
	if _, is := callListMyGames(t, st, auth.Claims{UserID: 999}, true).(api.ListMyGames401JSONResponse); !is {
		t.Fatal("expected 401 when the account does not exist")
	}
}

func TestListMyGamesInactiveAccountIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 7, "gone@example.com", false, false)
	if _, is := callListMyGames(t, st, auth.Claims{UserID: 7}, true).(api.ListMyGames401JSONResponse); !is {
		t.Fatal("expected 401 when the account is inactive")
	}
}

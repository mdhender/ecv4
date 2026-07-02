package handlers

import (
	"context"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

func gsptr(s api.GameStatus) *api.GameStatus { return &s }

// callUpdateGame invokes the UpdateGame handler directly with claims in context.
func callUpdateGame(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool, gameID int64, body *api.UpdateGameRequest) api.UpdateGameResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).UpdateGame(ctx, api.UpdateGameRequestObject{GameId: gameID, Body: body})
	if err != nil {
		t.Fatalf("UpdateGame returned error: %v", err)
	}
	return resp
}

// gameUpdateWorld seeds accounts 1 admin, 2 GM, 3 active player, 4 non-member.
func gameUpdateWorld(t *testing.T) (*store.Store, *sqlitemigration.Pool) {
	t.Helper()
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "admin@example.com", true, true)
	insertAccount(t, pool, 2, "gm@example.com", false, true)
	insertAccount(t, pool, 3, "player@example.com", false, true)
	insertAccount(t, pool, 4, "stranger@example.com", false, true)
	return st, pool
}

// seedGameStatus creates game 10 in the given status (active/visible) with account
// 2 as an active GM and account 3 as an active player.
func seedGameStatus(t *testing.T, pool *sqlitemigration.Pool, status string) {
	t.Helper()
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', ?, 1);", status)
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 2, 'Gm', 1, 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 3, 'Rome', 0, 1);")
}

func game200(t *testing.T, resp api.UpdateGameResponseObject) api.Game {
	t.Helper()
	ok, is := resp.(api.UpdateGame200JSONResponse)
	if !is {
		t.Fatalf("got %T, want UpdateGame200JSONResponse", resp)
	}
	return api.Game(ok)
}

// --- status: forward / skip / backward ---

func TestUpdateGameGMAdvancesForward(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "draft")
	g := game200(t, callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Recruiting)}))
	if g.Status != api.Recruiting {
		t.Fatalf("status = %q, want recruiting", g.Status)
	}
}

func TestUpdateGameGMSkipForward(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "draft")
	// A skip (draft → active) is allowed.
	if g := game200(t, callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Active)})); g.Status != api.Active {
		t.Fatalf("status = %q, want active", g.Status)
	}
}

func TestUpdateGameGMCanArchive(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "complete")
	// Advancing to archived is an ordinary forward move for a GM.
	if g := game200(t, callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Archived)})); g.Status != api.Archived {
		t.Fatalf("status = %q, want archived", g.Status)
	}
}

func TestUpdateGameBackwardIs409(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "active")
	for _, back := range []api.GameStatus{api.Recruiting, api.Draft} {
		if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(back)}).(api.UpdateGame409JSONResponse); !is {
			t.Fatalf("active → %q: expected 409", back)
		}
	}
}

func TestUpdateGamePlayerCannotAdvance(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "draft")
	// A plain player is neither GM nor admin.
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 3}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Recruiting)}).(api.UpdateGame403JSONResponse); !is {
		t.Fatal("expected 403 for a plain player advancing status")
	}
}

// --- un-pause (paused → active): admin only ---

func TestUpdateGameAdminUnpause(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "paused")
	if g := game200(t, callUpdateGame(t, st, auth.Claims{UserID: 1}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Active)})); g.Status != api.Active {
		t.Fatalf("status = %q, want active", g.Status)
	}
}

func TestUpdateGameGMCannotUnpause(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "paused")
	// paused → active is the one backward move, and it is admin-only.
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Active)}).(api.UpdateGame403JSONResponse); !is {
		t.Fatal("expected 403 for a GM un-pausing")
	}
}

// --- archived: frozen, admin-only out-of-archived ---

func TestUpdateGameAdminUnarchives(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "archived")
	if g := game200(t, callUpdateGame(t, st, auth.Claims{UserID: 1}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Active)})); g.Status != api.Active {
		t.Fatalf("status = %q, want active", g.Status)
	}
}

func TestUpdateGameGMCannotUnarchive(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "archived")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Active)}).(api.UpdateGame403JSONResponse); !is {
		t.Fatal("expected 403 for a GM changing an archived game's status")
	}
}

func TestUpdateGameArchivedFreezesNonStatusFields(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "archived")
	// Metadata / visibility are frozen while archived, even for an admin, and even
	// alongside a valid status change.
	cases := []*api.UpdateGameRequest{
		{Name: strptr("New")},
		{Description: strptr("x")},
		{IsActive: boolptr(true)},
		{Status: gsptr(api.Active), Name: strptr("New")},
	}
	for i, body := range cases {
		if _, is := callUpdateGame(t, st, auth.Claims{UserID: 1}, true, 10, body).(api.UpdateGame403JSONResponse); !is {
			t.Fatalf("case %d: expected 403 (archived freeze)", i)
		}
	}
}

// --- metadata ---

func TestUpdateGameGMEditsMetadata(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	g := game200(t, callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{
		Name: strptr("Alpha Campaign"), Description: strptr("A blurb."),
	}))
	if g.Name != "Alpha Campaign" || g.Description == nil || *g.Description != "A blurb." {
		t.Fatalf("game = %+v, want renamed with description", g)
	}
}

func TestUpdateGamePlayerCannotEditMetadata(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 3}, true, 10, &api.UpdateGameRequest{Name: strptr("New")}).(api.UpdateGame403JSONResponse); !is {
		t.Fatal("expected 403 for a plain player editing metadata")
	}
}

func TestUpdateGameEmptyNameIs400(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Name: strptr("   ")}).(api.UpdateGame400JSONResponse); !is {
		t.Fatal("expected 400 for a blank name")
	}
}

// --- isActive (admin-only hard-hide) ---

func TestUpdateGameAdminHides(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "active")
	// is_active is not exposed on api.Game, so assert the hide by its effect: the
	// admin succeeds, and a non-admin member can no longer see the game.
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 1}, true, 10, &api.UpdateGameRequest{IsActive: boolptr(false)}).(api.UpdateGame200JSONResponse); !is {
		t.Fatal("expected 200 for an admin hiding the game")
	}
	if _, is := callGetGame(t, st, auth.Claims{UserID: 3}, true, 10).(api.GetGame404JSONResponse); !is {
		t.Fatal("expected the hidden game to disappear for a non-admin member")
	}
}

func TestUpdateGameGMCannotSetIsActive(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "active")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{IsActive: boolptr(false)}).(api.UpdateGame403JSONResponse); !is {
		t.Fatal("expected 403 for a GM setting isActive")
	}
}

func TestUpdateGameAdminActsOnHiddenGame(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	// A hidden (is_active=0) but non-archived game: only the admin can see/act.
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(20, 'BETA', 'Beta', 'active', 0);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(20, 2, 'Gm', 1, 1);")

	// The GM cannot even see the hidden game.
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 20, &api.UpdateGameRequest{Name: strptr("New")}).(api.UpdateGame404JSONResponse); !is {
		t.Fatal("expected 404 for a GM on an admin-hidden game")
	}
	// The admin can unhide it (isActive orthogonal to status) ...
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 1}, true, 20, &api.UpdateGameRequest{IsActive: boolptr(true)}).(api.UpdateGame200JSONResponse); !is {
		t.Fatal("expected 200 for the admin unhiding the game")
	}
	// ... after which the GM can see it.
	if _, is := callGetGame(t, st, auth.Claims{UserID: 2}, true, 20).(api.GetGame200JSONResponse); !is {
		t.Fatal("expected the GM to see the now-visible game")
	}
}

// --- validation / visibility / auth ---

func TestUpdateGameUnknownStatusIs400(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "draft")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.GameStatus("bogus"))}).(api.UpdateGame400JSONResponse); !is {
		t.Fatal("expected 400 for an unknown status value")
	}
}

func TestUpdateGameNoOpReturns200(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	// Setting the status to its current value is a no-op, not a backward-move 409.
	if g := game200(t, callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Recruiting)})); g.Status != api.Recruiting {
		t.Fatalf("status = %q, want recruiting (no-op)", g.Status)
	}
}

func TestUpdateGameNonMemberIs404(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 4}, true, 10, &api.UpdateGameRequest{Status: gsptr(api.Active)}).(api.UpdateGame404JSONResponse); !is {
		t.Fatal("expected 404 for a caller who cannot see the game")
	}
}

func TestUpdateGameUnknownGameIs404(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 1}, true, 999, &api.UpdateGameRequest{Status: gsptr(api.Active)}).(api.UpdateGame404JSONResponse); !is {
		t.Fatal("expected 404 for an unknown game id")
	}
}

func TestUpdateGameEmptyBodyIs400(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, &api.UpdateGameRequest{}).(api.UpdateGame400JSONResponse); !is {
		t.Fatal("expected 400 for a body with no fields")
	}
	if _, is := callUpdateGame(t, st, auth.Claims{UserID: 2}, true, 10, nil).(api.UpdateGame400JSONResponse); !is {
		t.Fatal("expected 400 for a missing body")
	}
}

func TestUpdateGameNoClaimsIs401(t *testing.T) {
	st, pool := gameUpdateWorld(t)
	seedGameStatus(t, pool, "recruiting")
	if _, is := callUpdateGame(t, st, auth.Claims{}, false, 10, &api.UpdateGameRequest{Status: gsptr(api.Active)}).(api.UpdateGame401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

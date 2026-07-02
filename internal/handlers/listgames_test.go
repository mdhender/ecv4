package handlers

import (
	"context"
	"testing"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// callListGames invokes the ListGames handler directly with claims placed in the
// context, bypassing the HTTP/auth layer, like callListMyGames.
func callListGames(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool, params api.ListGamesParams) api.ListGamesResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).ListGames(ctx, api.ListGamesRequestObject{Params: params})
	if err != nil {
		t.Fatalf("ListGames returned error: %v", err)
	}
	return resp
}

// callGetGame invokes the GetGame handler directly with claims in the context.
func callGetGame(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool, gameID int64) api.GetGameResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).GetGame(ctx, api.GetGameRequestObject{GameId: gameID})
	if err != nil {
		t.Fatalf("GetGame returned error: %v", err)
	}
	return resp
}

// listGamesFixture builds the world shared by the ListGames/GetGame handler
// tests and returns the store plus the non-admin (account 1) and admin
// (account 9, never a member) ids. ALPHA is an active membership, BETA an
// active-but-dropped membership, GAMMA an admin-hidden membership, and DELTA an
// active game account 1 was never assigned to.
func listGamesFixture(t *testing.T) (*store.Store, int64, int64) {
	t.Helper()
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "me@example.com", false, true)
	insertAccount(t, pool, 9, "admin@example.com", true, true)

	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(20, 'BETA', 'Beta', 'active', 1);")
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(30, 'GAMMA', 'Gamma', 'active', 0);")
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(40, 'DELTA', 'Delta', 'recruiting', 1);")

	// account 1: active in ALPHA, dropped in BETA, active in (hidden) GAMMA.
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 1, 'Overlord', 1, 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(20, 1, 'Rome', 0, 0);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(30, 1, 'Carthage', 0, 1);")
	// DELTA has a bystander member only, and it is not account 1 nor the admin.
	insertAccount(t, pool, 2, "other@example.com", false, true)
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(40, 2, 'Egypt', 0, 1);")

	return st, 1, 9
}

// gameIDs projects the ids of a game slice in order.
func gameIDs(games []api.Game) []int64 {
	ids := make([]int64, len(games))
	for i, g := range games {
		ids[i] = g.Id
	}
	return ids
}

func TestListGamesMemberSeesAssignedIncludingDropped(t *testing.T) {
	st, me, _ := listGamesFixture(t)

	resp := callListGames(t, st, auth.Claims{UserID: me}, true, api.ListGamesParams{})
	ok, is := resp.(api.ListGames200JSONResponse)
	if !is {
		t.Fatalf("got %T, want ListGames200JSONResponse", resp)
	}
	// ALPHA (active member) and BETA (dropped member still sees metadata); not the
	// admin-hidden GAMMA, and not DELTA (never assigned).
	if ids := gameIDs(ok.Games); !intsEqual(ids, []int64{10, 20}) {
		t.Fatalf("member ids = %v, want [10 20]", ids)
	}
}

func TestListGamesAdminSeesAllIncludingHidden(t *testing.T) {
	st, _, admin := listGamesFixture(t)

	resp := callListGames(t, st, auth.Claims{UserID: admin}, true, api.ListGamesParams{})
	ok, is := resp.(api.ListGames200JSONResponse)
	if !is {
		t.Fatalf("got %T, want ListGames200JSONResponse", resp)
	}
	// Admin sees every game, including hidden GAMMA and the unassigned DELTA.
	if ids := gameIDs(ok.Games); !intsEqual(ids, []int64{10, 20, 30, 40}) {
		t.Fatalf("admin ids = %v, want [10 20 30 40]", ids)
	}
}

func TestListGamesStatusFilter(t *testing.T) {
	st, me, admin := listGamesFixture(t)

	recruiting := api.Recruiting
	// Non-admin: only visible 'recruiting' games (ALPHA); BETA is 'active'.
	resp := callListGames(t, st, auth.Claims{UserID: me}, true, api.ListGamesParams{Status: &recruiting})
	ok := resp.(api.ListGames200JSONResponse)
	if ids := gameIDs(ok.Games); !intsEqual(ids, []int64{10}) {
		t.Fatalf("member recruiting ids = %v, want [10]", ids)
	}

	// Admin: 'recruiting' matches ALPHA and the unassigned DELTA.
	respAdmin := callListGames(t, st, auth.Claims{UserID: admin}, true, api.ListGamesParams{Status: &recruiting})
	okAdmin := respAdmin.(api.ListGames200JSONResponse)
	if ids := gameIDs(okAdmin.Games); !intsEqual(ids, []int64{10, 40}) {
		t.Fatalf("admin recruiting ids = %v, want [10 40]", ids)
	}
}

func TestListGamesNoClaimsIs401(t *testing.T) {
	st, _, _ := listGamesFixture(t)
	if _, is := callListGames(t, st, auth.Claims{}, false, api.ListGamesParams{}).(api.ListGames401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

func TestListGamesInactiveAccountIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 7, "gone@example.com", false, false)
	if _, is := callListGames(t, st, auth.Claims{UserID: 7}, true, api.ListGamesParams{}).(api.ListGames401JSONResponse); !is {
		t.Fatal("expected 401 when the account is inactive")
	}
}

func TestGetGameMemberSeesAssigned(t *testing.T) {
	st, me, _ := listGamesFixture(t)

	resp := callGetGame(t, st, auth.Claims{UserID: me}, true, 10)
	ok, is := resp.(api.GetGame200JSONResponse)
	if !is {
		t.Fatalf("got %T, want GetGame200JSONResponse", resp)
	}
	if ok.Id != 10 || ok.Code != "ALPHA" || ok.Status != api.Recruiting {
		t.Fatalf("game = %+v, want id=10 code=ALPHA status=recruiting", ok)
	}
}

func TestGetGameDroppedMemberStillSeesMetadata(t *testing.T) {
	st, me, _ := listGamesFixture(t)

	resp := callGetGame(t, st, auth.Claims{UserID: me}, true, 20)
	if ok, is := resp.(api.GetGame200JSONResponse); !is || ok.Id != 20 {
		t.Fatalf("got %T (%+v), want GetGame200JSONResponse id=20", resp, resp)
	}
}

func TestGetGameHiddenExcludedForNonAdmin(t *testing.T) {
	st, me, admin := listGamesFixture(t)

	// GAMMA is admin-hidden: a non-admin member gets 404 ...
	if _, is := callGetGame(t, st, auth.Claims{UserID: me}, true, 30).(api.GetGame404JSONResponse); !is {
		t.Fatal("expected 404 for a non-admin on an admin-hidden game")
	}
	// ... but the admin can read it.
	if ok, is := callGetGame(t, st, auth.Claims{UserID: admin}, true, 30).(api.GetGame200JSONResponse); !is || ok.Id != 30 {
		t.Fatalf("admin GetGame(30) = %T, want 200 id=30", callGetGame(t, st, auth.Claims{UserID: admin}, true, 30))
	}
}

func TestGetGameNeverAssignedIs404(t *testing.T) {
	st, me, _ := listGamesFixture(t)

	// DELTA exists and is active, but account 1 was never assigned to it.
	if _, is := callGetGame(t, st, auth.Claims{UserID: me}, true, 40).(api.GetGame404JSONResponse); !is {
		t.Fatal("expected 404 for a caller never assigned to the game")
	}
	// An entirely unknown id is also a 404.
	if _, is := callGetGame(t, st, auth.Claims{UserID: me}, true, 999).(api.GetGame404JSONResponse); !is {
		t.Fatal("expected 404 for an unknown game id")
	}
}

func TestGetGameNoClaimsIs401(t *testing.T) {
	st, _, _ := listGamesFixture(t)
	if _, is := callGetGame(t, st, auth.Claims{}, false, 10).(api.GetGame401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

// intsEqual reports whether two int64 slices are element-wise equal.
func intsEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

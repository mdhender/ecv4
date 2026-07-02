package handlers

import (
	"context"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// callAddGameMember invokes the AddGameMember handler directly with claims in the
// context, bypassing the HTTP/auth layer.
func callAddGameMember(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool, gameID int64, body *api.AddMemberRequest) api.AddGameMemberResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).AddGameMember(ctx, api.AddGameMemberRequestObject{GameId: gameID, Body: body})
	if err != nil {
		t.Fatalf("AddGameMember returned error: %v", err)
	}
	return resp
}

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }

// addMemberWorld seeds the accounts and roster shared by the add-member tests and
// returns the store plus the pool for per-test game seeding. Accounts: 1 admin,
// 2 an active GM (of the games seeded per test), 3 an active player, 4 a target
// to add, 5 a second target.
func addMemberWorld(t *testing.T) (*store.Store, *sqlitemigration.Pool) {
	t.Helper()
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "admin@example.com", true, true)
	insertAccount(t, pool, 2, "gm@example.com", false, true)
	insertAccount(t, pool, 3, "player@example.com", false, true)
	insertAccount(t, pool, 4, "target@example.com", false, true)
	insertAccount(t, pool, 5, "target2@example.com", false, true)
	return st, pool
}

// seedGameWithGM creates a game in the given status with account 2 as an active GM
// and account 3 as an active player.
func seedGameWithGM(t *testing.T, pool *sqlitemigration.Pool, gameID int64, code, status string) {
	t.Helper()
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(?, ?, ?, ?, 1);", gameID, code, code, status)
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(?, 2, 'Gm', 1, 1);", gameID)
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(?, 3, 'Player', 0, 1);", gameID)
}

func TestAddMemberAdminAssignsFirstGM(t *testing.T) {
	st, pool := addMemberWorld(t)
	// A fresh draft game with no members: the admin assigns the first GM.
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'draft', 1);")

	resp := callAddGameMember(t, st, auth.Claims{UserID: 1}, true, 10, &api.AddMemberRequest{
		AccountId: 4, Handle: strptr("Overlord"), IsGm: boolptr(true),
	})
	ok, is := resp.(api.AddGameMember201JSONResponse)
	if !is {
		t.Fatalf("got %T, want 201", resp)
	}
	if ok.AccountId != 4 || ok.Handle != "Overlord" || !ok.IsGm || !ok.IsActive {
		t.Fatalf("member = %+v, want account 4 Overlord GM active", ok)
	}
}

func TestAddMemberGMAddsGMInDraft(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "draft")

	// A GM may add another GM while draft (GM adds allowed in any non-archived).
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{
		AccountId: 4, IsGm: boolptr(true),
	}).(api.AddGameMember201JSONResponse); !is {
		t.Fatal("expected 201 for a GM adding a GM in draft")
	}
}

func TestAddMemberGMAddsPlayerInDraftIsForbidden(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "draft")

	// Adding a net-new player is recruiting-only; draft is rejected for a GM.
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{
		AccountId: 4,
	}).(api.AddGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for a GM adding a player in draft")
	}
}

func TestAddMemberGMAddsPlayerInRecruiting(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	// Omitted handle → player_N. There are already 2 members (GM + player), so N=3.
	resp := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{AccountId: 4})
	ok, is := resp.(api.AddGameMember201JSONResponse)
	if !is {
		t.Fatalf("got %T, want 201", resp)
	}
	if ok.Handle != "player_3" || ok.IsGm {
		t.Fatalf("member = %+v, want handle player_3, not GM", ok)
	}
}

func TestAddMemberAdminAddsPlayerOutsideRecruiting(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "active")

	// The admin bypasses the recruiting-only window for players.
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 1}, true, 10, &api.AddMemberRequest{
		AccountId: 4,
	}).(api.AddGameMember201JSONResponse); !is {
		t.Fatal("expected 201 for an admin adding a player in active")
	}
}

func TestAddMemberArchivedIsForbidden(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "archived")

	// Archived freezes updates, even a GM adding a GM and even for the admin.
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{
		AccountId: 4, IsGm: boolptr(true),
	}).(api.AddGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for a GM adding to an archived game")
	}
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 1}, true, 10, &api.AddMemberRequest{
		AccountId: 4, IsGm: boolptr(true),
	}).(api.AddGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for an admin adding to an archived game")
	}
}

func TestAddMemberPlayerCallerIsForbidden(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	// Account 3 is an active player (not a GM): may see the game but not add.
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 3}, true, 10, &api.AddMemberRequest{
		AccountId: 4,
	}).(api.AddGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for a non-GM player adding a member")
	}
}

func TestAddMemberNonMemberIs404(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	// Account 5 was never assigned: the game is not visible → 404, not 403.
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 5}, true, 10, &api.AddMemberRequest{
		AccountId: 4,
	}).(api.AddGameMember404JSONResponse); !is {
		t.Fatal("expected 404 for a caller who cannot see the game")
	}
}

func TestAddMemberDuplicateHandleIs409(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	// 'Player' is account 3's handle; reusing it is a 409.
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{
		AccountId: 4, Handle: strptr("Player"),
	}).(api.AddGameMember409JSONResponse); !is {
		t.Fatal("expected 409 for a duplicate handle")
	}
}

func TestAddMemberAlreadyMemberIs409(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	// Account 3 is already a member; re-adding points the caller at reactivate.
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{
		AccountId: 3, Handle: strptr("Rome"),
	}).(api.AddGameMember409JSONResponse); !is {
		t.Fatal("expected 409 when adding an account that is already a member")
	}
}

func TestAddMemberBadHandleIs400(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	for _, h := range []string{"a", "1abc", "has space", "bad!", ""} {
		if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{
			AccountId: 4, Handle: strptr(h),
		}).(api.AddGameMember400JSONResponse); !is {
			t.Fatalf("handle %q: expected 400", h)
		}
	}
}

func TestAddMemberMissingBodyOrAccountIs400(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, nil).(api.AddGameMember400JSONResponse); !is {
		t.Fatal("expected 400 for a missing body")
	}
	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{AccountId: 0}).(api.AddGameMember400JSONResponse); !is {
		t.Fatal("expected 400 for a missing accountId")
	}
}

func TestAddMemberUnknownTargetAccountIs400(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")

	if _, is := callAddGameMember(t, st, auth.Claims{UserID: 2}, true, 10, &api.AddMemberRequest{
		AccountId: 999, Handle: strptr("Ghost"),
	}).(api.AddGameMember400JSONResponse); !is {
		t.Fatal("expected 400 for a nonexistent target account")
	}
}

func TestAddMemberNoClaimsIs401(t *testing.T) {
	st, pool := addMemberWorld(t)
	seedGameWithGM(t, pool, 10, "ALPHA", "recruiting")
	if _, is := callAddGameMember(t, st, auth.Claims{}, false, 10, &api.AddMemberRequest{AccountId: 4}).(api.AddGameMember401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

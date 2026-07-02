package handlers

import (
	"context"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// callUpdateGameMember invokes the UpdateGameMember handler directly with claims
// in the context, bypassing the HTTP/auth layer.
func callUpdateGameMember(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool, gameID, accountID int64, body *api.UpdateMemberRequest) api.UpdateGameMemberResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).UpdateGameMember(ctx, api.UpdateGameMemberRequestObject{
		GameId: gameID, AccountId: accountID, Body: body,
	})
	if err != nil {
		t.Fatalf("UpdateGameMember returned error: %v", err)
	}
	return resp
}

// updateMemberWorld seeds accounts (1 admin, 2 GM, 3 active player, 4 dropped
// player, 5 non-member) and returns the store and pool. Per-test the game is
// seeded with seedRoster below in the desired status.
func updateMemberWorld(t *testing.T) (*store.Store, *sqlitemigration.Pool) {
	t.Helper()
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "admin@example.com", true, true)
	insertAccount(t, pool, 2, "gm@example.com", false, true)
	insertAccount(t, pool, 3, "player@example.com", false, true)
	insertAccount(t, pool, 4, "dropped@example.com", false, true)
	insertAccount(t, pool, 5, "stranger@example.com", false, true)
	return st, pool
}

// seedRoster creates game 10 in the given status with account 2 as an active GM,
// account 3 as an active player 'Rome', and account 4 as a dropped player 'Punic'.
func seedRoster(t *testing.T, pool *sqlitemigration.Pool, status string) {
	t.Helper()
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', ?, 1);", status)
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 2, 'Gm', 1, 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 3, 'Rome', 0, 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 4, 'Punic', 0, 0);")
}

func upd200(t *testing.T, resp api.UpdateGameMemberResponseObject) api.Member {
	t.Helper()
	ok, is := resp.(api.UpdateGameMember200JSONResponse)
	if !is {
		t.Fatalf("got %T, want UpdateGameMember200JSONResponse", resp)
	}
	return api.Member(ok)
}

// --- reactivate ---

func TestUpdateMemberGMReactivatesInActive(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active") // reactivation is NOT recruiting-only

	m := upd200(t, callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 4, &api.UpdateMemberRequest{
		IsActive: boolptr(true),
	}))
	if m.AccountId != 4 || !m.IsActive || m.IsGm || m.Handle != "Punic" {
		t.Fatalf("member = %+v, want account 4 reactivated player Punic", m)
	}
}

func TestUpdateMemberDeactivateIsRejected(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// Dropping (isActive:false) is a separate operation; rejected here as a 400.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 3, &api.UpdateMemberRequest{
		IsActive: boolptr(false),
	}).(api.UpdateGameMember400JSONResponse); !is {
		t.Fatal("expected 400 for isActive:false (deactivation not supported here)")
	}
}

// --- promote ---

func TestUpdateMemberPromoteInRecruiting(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")

	m := upd200(t, callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 3, &api.UpdateMemberRequest{
		IsGm: boolptr(true),
	}))
	if !m.IsGm {
		t.Fatalf("member = %+v, want promoted to GM", m)
	}
}

func TestUpdateMemberPromoteRejectedAfterRecruiting(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// Promotion is recruiting-only for a GM.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 3, &api.UpdateMemberRequest{
		IsGm: boolptr(true),
	}).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 promoting a player outside recruiting")
	}
}

func TestUpdateMemberAdminPromotesOutsideRecruiting(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// An admin bypasses the recruiting-only window for promotion.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 1}, true, 10, 3, &api.UpdateMemberRequest{
		IsGm: boolptr(true),
	}).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("expected 200 for an admin promoting outside recruiting")
	}
}

func TestUpdateMemberDemotionAlwaysRejected(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	// Demotion (isGm:false) is out of scope even for an admin.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 1}, true, 10, 2, &api.UpdateMemberRequest{
		IsGm: boolptr(false),
	}).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 demoting a GM, even as admin")
	}
}

// --- self-edit handle ---

func TestUpdateMemberSelfRenameInRecruiting(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")

	m := upd200(t, callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, &api.UpdateMemberRequest{
		Handle: strptr("Carthage"),
	}))
	if m.Handle != "Carthage" {
		t.Fatalf("member = %+v, want handle Carthage", m)
	}
}

func TestUpdateMemberSelfRenameRejectedAfterRecruiting(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, &api.UpdateMemberRequest{
		Handle: strptr("Carthage"),
	}).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for a self-rename outside recruiting")
	}
}

func TestUpdateMemberSelfRenamePlayerPrefixIs400(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, &api.UpdateMemberRequest{
		Handle: strptr("player_9"),
	}).(api.UpdateGameMember400JSONResponse); !is {
		t.Fatal("expected 400 for a self-chosen 'player_' handle")
	}
}

func TestUpdateMemberRenameDuplicateIs409(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	// Account 3 renames onto the GM's existing handle 'Gm'.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, &api.UpdateMemberRequest{
		Handle: strptr("Gm"),
	}).(api.UpdateGameMember409JSONResponse); !is {
		t.Fatal("expected 409 renaming onto an in-use handle")
	}
}

func TestUpdateMemberGMCannotRenameOtherMember(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	// A GM renaming another member's handle is out of scope (403); only the member
	// or an admin may.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 3, &api.UpdateMemberRequest{
		Handle: strptr("Carthage"),
	}).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for a GM renaming another member")
	}
}

func TestUpdateMemberAdminRenamesAnyStatus(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// An admin may rename in a non-recruiting status, and may use a 'player_' handle.
	m := upd200(t, callUpdateGameMember(t, st, auth.Claims{UserID: 1}, true, 10, 3, &api.UpdateMemberRequest{
		Handle: strptr("player_42"),
	}))
	if m.Handle != "player_42" {
		t.Fatalf("member = %+v, want handle player_42", m)
	}
}

// --- gates common to every field ---

func TestUpdateMemberSelfPromoteForbidden(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	// A player cannot promote themselves.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, &api.UpdateMemberRequest{
		IsGm: boolptr(true),
	}).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for a player self-promoting")
	}
}

func TestUpdateMemberArchivedIsForbidden(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "archived")
	// Archived freezes updates, even for an admin.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 1}, true, 10, 4, &api.UpdateMemberRequest{
		IsActive: boolptr(true),
	}).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 updating a member of an archived game, even as admin")
	}
}

func TestUpdateMemberNonMemberCallerIs404(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	// Account 5 cannot see the game → 404, not 403.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 5}, true, 10, 3, &api.UpdateMemberRequest{
		Handle: strptr("Carthage"),
	}).(api.UpdateGameMember404JSONResponse); !is {
		t.Fatal("expected 404 for a caller who cannot see the game")
	}
}

func TestUpdateMemberUnknownTargetIs404(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	// Account 5 exists but is not a member of the game.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 5, &api.UpdateMemberRequest{
		IsActive: boolptr(true),
	}).(api.UpdateGameMember404JSONResponse); !is {
		t.Fatal("expected 404 for a target that is not a member")
	}
}

func TestUpdateMemberEmptyBodyIs400(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 3, &api.UpdateMemberRequest{}).(api.UpdateGameMember400JSONResponse); !is {
		t.Fatal("expected 400 for a body with no fields set")
	}
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 3, nil).(api.UpdateGameMember400JSONResponse); !is {
		t.Fatal("expected 400 for a missing body")
	}
}

func TestUpdateMemberNoClaimsIs401(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "recruiting")
	if _, is := callUpdateGameMember(t, st, auth.Claims{}, false, 10, 3, &api.UpdateMemberRequest{
		IsGm: boolptr(true),
	}).(api.UpdateGameMember401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

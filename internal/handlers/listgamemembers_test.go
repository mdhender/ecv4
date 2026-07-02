package handlers

import (
	"context"
	"testing"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// callListGameMembers invokes the ListGameMembers handler directly with claims in
// the context, bypassing the HTTP/auth layer.
func callListGameMembers(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool, gameID int64) api.ListGameMembersResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).ListGameMembers(ctx, api.ListGameMembersRequestObject{GameId: gameID})
	if err != nil {
		t.Fatalf("ListGameMembers returned error: %v", err)
	}
	return resp
}

// membersFixture builds the world shared by the roster tests. Accounts: 1 is the
// GM, 2 an active player, 3 a dropped player, 8 an account never assigned to
// ALPHA, and 9 an admin (never a member). ALPHA (10) is active and holds the
// three memberships; GAMMA (30) is admin-hidden with account 1 as a member.
func membersFixture(t *testing.T) *store.Store {
	t.Helper()
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "gm@example.com", false, true)
	insertAccount(t, pool, 2, "rome@example.com", false, true)
	insertAccount(t, pool, 3, "carthage@example.com", false, true)
	insertAccount(t, pool, 8, "stranger@example.com", false, true)
	insertAccount(t, pool, 9, "admin@example.com", true, true)

	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(30, 'GAMMA', 'Gamma', 'active', 0);")

	seedExec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(100, 10, 1, 'Overlord', 1, 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(101, 10, 2, 'Rome', 0, 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(102, 10, 3, 'Carthage', 0, 0);")
	seedExec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(300, 30, 1, 'Hider', 1, 1);")

	return st
}

// wantAlphaRoster is ALPHA's full roster, GM first, including the dropped player.
var wantAlphaRoster = []api.Member{
	{AccountId: 1, Handle: "Overlord", IsGm: true, IsActive: true},
	{AccountId: 2, Handle: "Rome", IsGm: false, IsActive: true},
	{AccountId: 3, Handle: "Carthage", IsGm: false, IsActive: false},
}

func assertRoster(t *testing.T, resp api.ListGameMembersResponseObject, want []api.Member) {
	t.Helper()
	ok, is := resp.(api.ListGameMembers200JSONResponse)
	if !is {
		t.Fatalf("got %T, want ListGameMembers200JSONResponse", resp)
	}
	if len(ok.Members) != len(want) {
		t.Fatalf("got %d members, want %d: %+v", len(ok.Members), len(want), ok.Members)
	}
	for i := range want {
		if ok.Members[i] != want[i] {
			t.Fatalf("member %d = %+v, want %+v", i, ok.Members[i], want[i])
		}
	}
}

func TestListGameMembersGMSeesFullRoster(t *testing.T) {
	st := membersFixture(t)
	// The GM sees the whole roster, including the dropped player (isActive=false).
	assertRoster(t, callListGameMembers(t, st, auth.Claims{UserID: 1}, true, 10), wantAlphaRoster)
}

func TestListGameMembersPlayerSeesRoster(t *testing.T) {
	st := membersFixture(t)
	// An active (non-GM) player can also list the roster.
	assertRoster(t, callListGameMembers(t, st, auth.Claims{UserID: 2}, true, 10), wantAlphaRoster)
}

func TestListGameMembersDroppedMemberSeesRoster(t *testing.T) {
	st := membersFixture(t)
	// A dropped player was still ever-assigned, so they retain visibility.
	assertRoster(t, callListGameMembers(t, st, auth.Claims{UserID: 3}, true, 10), wantAlphaRoster)
}

func TestListGameMembersAdminSeesRoster(t *testing.T) {
	st := membersFixture(t)
	// The admin is never a member but sees any game's roster.
	assertRoster(t, callListGameMembers(t, st, auth.Claims{UserID: 9}, true, 10), wantAlphaRoster)
}

func TestListGameMembersNonMemberIs404(t *testing.T) {
	st := membersFixture(t)
	// Account 8 was never assigned to ALPHA: the game must be indistinguishable
	// from a nonexistent one — a 404, not a 403 or an empty roster.
	if _, is := callListGameMembers(t, st, auth.Claims{UserID: 8}, true, 10).(api.ListGameMembers404JSONResponse); !is {
		t.Fatal("expected 404 for a caller never assigned to the game")
	}
}

func TestListGameMembersUnknownGameIs404(t *testing.T) {
	st := membersFixture(t)
	if _, is := callListGameMembers(t, st, auth.Claims{UserID: 9}, true, 999).(api.ListGameMembers404JSONResponse); !is {
		t.Fatal("expected 404 for an unknown game id, even for an admin")
	}
}

func TestListGameMembersHiddenGame(t *testing.T) {
	st := membersFixture(t)
	// GAMMA is admin-hidden: its member (account 1) gets a 404 ...
	if _, is := callListGameMembers(t, st, auth.Claims{UserID: 1}, true, 30).(api.ListGameMembers404JSONResponse); !is {
		t.Fatal("expected 404 for a non-admin member of an admin-hidden game")
	}
	// ... but the admin can still read its roster.
	assertRoster(t, callListGameMembers(t, st, auth.Claims{UserID: 9}, true, 30), []api.Member{
		{AccountId: 1, Handle: "Hider", IsGm: true, IsActive: true},
	})
}

func TestListGameMembersNoClaimsIs401(t *testing.T) {
	st := membersFixture(t)
	if _, is := callListGameMembers(t, st, auth.Claims{}, false, 10).(api.ListGameMembers401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

func TestListGameMembersInactiveAccountIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 7, "gone@example.com", false, false)
	if _, is := callListGameMembers(t, st, auth.Claims{UserID: 7}, true, 10).(api.ListGameMembers401JSONResponse); !is {
		t.Fatal("expected 401 when the account is inactive")
	}
}

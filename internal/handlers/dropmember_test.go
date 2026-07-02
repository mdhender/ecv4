package handlers

import (
	"testing"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
)

// Drop / self-deactivate is folded into PATCH as isActive:false. These tests use
// the helpers from updategamemember_test.go: updateMemberWorld (accounts 1 admin,
// 2 GM, 3 active player, 4 dropped player, 5 non-member), seedRoster, and
// callUpdateGameMember.

func dropReq() *api.UpdateMemberRequest { return &api.UpdateMemberRequest{IsActive: boolptr(false)} }

func TestDropPlayerSelfDeactivates(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active") // self-drop is allowed in any non-archived status

	m := upd200(t, callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, dropReq()))
	if m.AccountId != 3 || m.IsActive {
		t.Fatalf("member = %+v, want account 3 dropped (isActive false)", m)
	}
}

func TestDropGMSelfDeactivates(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// A GM may drop their own role too.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 2, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("expected 200 for a GM self-dropping")
	}
}

func TestDropGMDropsPlayer(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 3, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("expected 200 for a GM dropping a player")
	}
}

func TestDropAdminDropsAnyone(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// Admin drops the GM ...
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 1}, true, 10, 2, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("expected 200 for an admin dropping a GM")
	}
	// ... and the player.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 1}, true, 10, 3, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("expected 200 for an admin dropping a player")
	}
}

func TestDropPlayerCannotDropAnother(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// Account 3 (plain player) tries to drop the GM (account 2): forbidden.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 2, dropReq()).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 for a plain player dropping another member")
	}
}

func TestDropCanEmptyTheGame(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active") // GM=2 active, player=3 active, 4 already dropped

	// Both active members self-drop, leaving the game with no active GM or player.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("player self-drop should succeed")
	}
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 2}, true, 10, 2, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("GM self-drop should succeed even if it empties the game")
	}

	// The roster still lists all three, every one now inactive.
	roster := callListGameMembers(t, st, auth.Claims{UserID: 1}, true, 10).(api.ListGameMembers200JSONResponse)
	if len(roster.Members) != 3 {
		t.Fatalf("roster has %d members, want 3 (drops are soft, never deleted)", len(roster.Members))
	}
	for _, m := range roster.Members {
		if m.IsActive {
			t.Fatalf("member %+v still active after the game was emptied", m)
		}
	}
}

func TestDropStillVisibleInRoster(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")

	// Admin drops the player; the dropped member remains on the roster as inactive.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 1}, true, 10, 3, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("admin drop should succeed")
	}
	roster := callListGameMembers(t, st, auth.Claims{UserID: 1}, true, 10).(api.ListGameMembers200JSONResponse)
	var found bool
	for _, m := range roster.Members {
		if m.AccountId == 3 {
			found = true
			if m.IsActive {
				t.Fatalf("dropped member 3 shows active: %+v", m)
			}
		}
	}
	if !found {
		t.Fatal("dropped member 3 no longer appears on the roster")
	}

	// The dropped player can still see the game's metadata (epic visibility rule).
	if _, is := callGetGame(t, st, auth.Claims{UserID: 3}, true, 10).(api.GetGame200JSONResponse); !is {
		t.Fatal("a dropped member should still see the game via GetGame")
	}
}

func TestDropInArchivedIsForbidden(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "archived")
	// Archived freezes every update, including a self-drop (per the locked rule; the
	// only archived exception is an admin changing status out of archived).
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 3}, true, 10, 3, dropReq()).(api.UpdateGameMember403JSONResponse); !is {
		t.Fatal("expected 403 self-dropping in an archived game")
	}
}

func TestDropAlreadyDroppedIsNoOp(t *testing.T) {
	st, pool := updateMemberWorld(t)
	seedRoster(t, pool, "active")
	// Account 4 is already dropped; dropping again is an idempotent no-op (200), and
	// even a plain player re-dropping themselves must not error.
	if _, is := callUpdateGameMember(t, st, auth.Claims{UserID: 4}, true, 10, 4, dropReq()).(api.UpdateGameMember200JSONResponse); !is {
		t.Fatal("expected 200 for an idempotent re-drop of an already-dropped member")
	}
}

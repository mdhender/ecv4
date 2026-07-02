package store_test

import (
	"context"
	"testing"

	"github.com/mdhender/ecv4/internal/store"
)

func TestMembersForGame(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'gm@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'rome@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(3, 'carthage@example.com', 0, 1, 'x');")

	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(20, 'BETA', 'Beta', 'recruiting', 1);")

	// ALPHA has a GM (active), an active player, and a dropped player. The order
	// of insertion is the expected result order (by membership row id).
	exec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(100, 10, 1, 'Overlord', 1, 1);")
	exec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(101, 10, 2, 'Rome', 0, 1);")
	exec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(102, 10, 3, 'Carthage', 0, 0);")
	// A membership in BETA must not leak into ALPHA's roster.
	exec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(200, 20, 2, 'Egypt', 0, 1);")

	got, err := st.MembersForGame(ctx, 10)
	if err != nil {
		t.Fatalf("MembersForGame: %v", err)
	}
	want := []store.Member{
		{AccountID: 1, Handle: "Overlord", IsGM: true, IsActive: true},
		{AccountID: 2, Handle: "Rome", IsGM: false, IsActive: true},
		{AccountID: 3, Handle: "Carthage", IsGM: false, IsActive: false}, // dropped, still listed
	}
	if len(got) != len(want) {
		t.Fatalf("got %d members, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("member %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// A game with no members yields an empty slice, not an error. The nonexistent
	// game id is handled the same way (visibility is the caller's concern).
	for _, id := range []int64{20 + 1, 999} {
		none, err := st.MembersForGame(ctx, id)
		if err != nil {
			t.Fatalf("MembersForGame(%d): %v", id, err)
		}
		if len(none) != 0 {
			t.Fatalf("MembersForGame(%d): got %d members, want 0", id, len(none))
		}
	}
}

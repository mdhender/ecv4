package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mdhender/ecv4/internal/store"
)

// The ListGames/GameByID visibility tests share a small world:
//
//   - account 1 is a non-admin player; account 2 is a bystander.
//   - ALPHA (10) is active; account 1 has an active membership.
//   - BETA (20) is active but account 1's membership is dropped (is_active = 0);
//     under the game-management rules a dropped member still sees the game.
//   - GAMMA (30) is admin-hidden (is_active = 0); account 1 has an active
//     membership but must not see a hidden game as a non-admin.
//   - DELTA (40) is active with no membership for account 1 (only the bystander).
//
// Statuses are set so the optional status filter can be exercised: ALPHA and BETA
// are 'recruiting', GAMMA and DELTA are 'active'.
func TestListGamesVisibility(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'me@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'other@example.com', 0, 1, 'x');")

	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(20, 'BETA', 'Beta', 'recruiting', 1);")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(30, 'GAMMA', 'Gamma', 'active', 0);")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(40, 'DELTA', 'Delta', 'active', 1);")

	// account 1: active member of ALPHA, dropped member of BETA, active member of
	// (hidden) GAMMA. account 2 is the sole member of DELTA.
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 1, 'Overlord', 1, 1);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(20, 1, 'Rome', 0, 0);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(30, 1, 'Carthage', 0, 1);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(40, 2, 'Egypt', 0, 1);")

	// Non-admin: sees ALPHA (active member) and BETA (dropped member still sees
	// metadata), but not GAMMA (admin-hidden) nor DELTA (never assigned).
	got, err := st.ListGames(ctx, 1, false, nil)
	if err != nil {
		t.Fatalf("ListGames(non-admin): %v", err)
	}
	if ids := gameIDs(got); !equalIDs(ids, []int64{10, 20}) {
		t.Fatalf("non-admin ids = %v, want [10 20]", ids)
	}

	// Admin: sees every game, including hidden GAMMA and unassigned DELTA.
	gotAdmin, err := st.ListGames(ctx, 1, true, nil)
	if err != nil {
		t.Fatalf("ListGames(admin): %v", err)
	}
	if ids := gameIDs(gotAdmin); !equalIDs(ids, []int64{10, 20, 30, 40}) {
		t.Fatalf("admin ids = %v, want [10 20 30 40]", ids)
	}

	// status filter, non-admin: only 'recruiting' games among the visible ones.
	recruiting := "recruiting"
	gotFiltered, err := st.ListGames(ctx, 1, false, &recruiting)
	if err != nil {
		t.Fatalf("ListGames(status): %v", err)
	}
	if ids := gameIDs(gotFiltered); !equalIDs(ids, []int64{10, 20}) {
		t.Fatalf("non-admin recruiting ids = %v, want [10 20]", ids)
	}

	// status filter, admin: 'active' matches hidden GAMMA and DELTA.
	active := "active"
	gotActive, err := st.ListGames(ctx, 1, true, &active)
	if err != nil {
		t.Fatalf("ListGames(admin,status): %v", err)
	}
	if ids := gameIDs(gotActive); !equalIDs(ids, []int64{30, 40}) {
		t.Fatalf("admin active ids = %v, want [30 40]", ids)
	}

	// An account in no visible games yields an empty slice, not an error.
	none, err := st.ListGames(ctx, 999, false, nil)
	if err != nil {
		t.Fatalf("ListGames(unknown): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("got %d games for unknown account, want 0", len(none))
	}
}

func TestGameByIDVisibility(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'me@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'other@example.com', 0, 1, 'x');")

	desc := "The first playtest."
	exec(t, pool, "INSERT INTO games(id, code, name, status, description, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', ?, 1);", desc)
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(20, 'BETA', 'Beta', 'recruiting', 1);")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(30, 'GAMMA', 'Gamma', 'active', 0);")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(40, 'DELTA', 'Delta', 'active', 1);")

	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 1, 'Overlord', 1, 1);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(20, 1, 'Rome', 0, 0);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(30, 1, 'Carthage', 0, 1);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(40, 2, 'Egypt', 0, 1);")

	// Non-admin, active membership: visible, and the description round-trips.
	game, err := st.GameByID(ctx, 10, 1, false)
	if err != nil {
		t.Fatalf("GameByID(10, member): %v", err)
	}
	if game.ID != 10 || game.Code != "ALPHA" || game.Name != "Alpha" || game.Status != "recruiting" {
		t.Fatalf("game = %+v, want id=10 code=ALPHA name=Alpha status=recruiting", game)
	}
	if game.Description == nil || *game.Description != desc {
		t.Fatalf("description = %v, want %q", game.Description, desc)
	}

	// Non-admin, dropped membership: still visible (metadata), description is nil.
	if beta, err := st.GameByID(ctx, 20, 1, false); err != nil {
		t.Fatalf("GameByID(20, dropped member): %v", err)
	} else if beta.ID != 20 || beta.Description != nil {
		t.Fatalf("beta = %+v, want id=20 nil description", beta)
	}

	// Non-admin, admin-hidden game they belong to: 404 (ErrNotFound).
	if _, err := st.GameByID(ctx, 30, 1, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GameByID(30, hidden): got %v, want ErrNotFound", err)
	}

	// Non-admin, never assigned: 404 even though the game is active.
	if _, err := st.GameByID(ctx, 40, 1, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GameByID(40, non-member): got %v, want ErrNotFound", err)
	}

	// Admin sees the hidden game.
	if gamma, err := st.GameByID(ctx, 30, 1, true); err != nil {
		t.Fatalf("GameByID(30, admin): %v", err)
	} else if gamma.ID != 30 || gamma.IsActive {
		t.Fatalf("gamma = %+v, want id=30 is_active=false", gamma)
	}

	// A wholly unknown id is ErrNotFound for admin and non-admin alike.
	if _, err := st.GameByID(ctx, 999, 1, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GameByID(999, admin): got %v, want ErrNotFound", err)
	}
	if _, err := st.GameByID(ctx, 999, 1, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GameByID(999, non-admin): got %v, want ErrNotFound", err)
	}
}

// gameIDs projects the ids of a game slice, preserving order.
func gameIDs(games []store.Game) []int64 {
	ids := make([]int64, len(games))
	for i, g := range games {
		ids[i] = g.ID
	}
	return ids
}

// equalIDs reports whether two id slices are element-wise equal.
func equalIDs(a, b []int64) bool {
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

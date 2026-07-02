package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mdhender/ecv4/internal/store"
)

func TestAddMemberDefaultHandle(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'a@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'b@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(3, 'c@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")

	// First member with an omitted handle: player_1 (count 0 + 1).
	m1, err := st.AddMember(ctx, 10, 1, "", true)
	if err != nil {
		t.Fatalf("AddMember(1): %v", err)
	}
	if m1 != (store.Member{AccountID: 1, Handle: "player_1", IsGM: true, IsActive: true}) {
		t.Fatalf("m1 = %+v, want player_1 GM active", m1)
	}

	// Second member with a supplied handle.
	if _, err := st.AddMember(ctx, 10, 2, "Rome", false); err != nil {
		t.Fatalf("AddMember(2): %v", err)
	}

	// Third member with an omitted handle: count is now 2, so player_3.
	m3, err := st.AddMember(ctx, 10, 3, "", false)
	if err != nil {
		t.Fatalf("AddMember(3): %v", err)
	}
	if m3.Handle != "player_3" {
		t.Fatalf("m3.Handle = %q, want player_3", m3.Handle)
	}
}

func TestAddMemberConflicts(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'a@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'b@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")

	if _, err := st.AddMember(ctx, 10, 1, "Rome", false); err != nil {
		t.Fatalf("AddMember(1): %v", err)
	}

	// Re-adding the same account is ErrMemberExists, even with a different handle.
	if _, err := st.AddMember(ctx, 10, 1, "Carthage", false); !errors.Is(err, store.ErrMemberExists) {
		t.Fatalf("re-add account: got %v, want ErrMemberExists", err)
	}

	// A different account reusing the handle is ErrHandleTaken.
	if _, err := st.AddMember(ctx, 10, 2, "Rome", false); !errors.Is(err, store.ErrHandleTaken) {
		t.Fatalf("duplicate handle: got %v, want ErrHandleTaken", err)
	}

	// A nonexistent target account is ErrNotFound (not a raw FK error).
	if _, err := st.AddMember(ctx, 10, 999, "Egypt", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown account: got %v, want ErrNotFound", err)
	}
}

// TestAddMemberDefaultHandleCollision pins the "never auto-bump N" rule: if the
// computed player_N default is already taken, the add fails rather than trying
// player_(N+1).
func TestAddMemberDefaultHandleCollision(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'a@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'b@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")

	// Account 1 takes the handle player_2 explicitly. The membership count is now 1,
	// so the next default handle computes to player_2 — already in use.
	if _, err := st.AddMember(ctx, 10, 1, "player_2", false); err != nil {
		t.Fatalf("AddMember(1): %v", err)
	}
	if _, err := st.AddMember(ctx, 10, 2, "", false); !errors.Is(err, store.ErrHandleTaken) {
		t.Fatalf("computed default collision: got %v, want ErrHandleTaken (never auto-bump)", err)
	}
}

func TestMemberForGame(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'a@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 1, 'Overlord', 1, 0);")

	got, err := st.MemberForGame(ctx, 10, 1)
	if err != nil {
		t.Fatalf("MemberForGame: %v", err)
	}
	// A dropped membership is still returned (is_active = 0), with its role.
	if got != (store.Member{AccountID: 1, Handle: "Overlord", IsGM: true, IsActive: false}) {
		t.Fatalf("got %+v, want dropped GM Overlord", got)
	}

	// An account never assigned to the game is ErrNotFound.
	if _, err := st.MemberForGame(ctx, 10, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown member: got %v, want ErrNotFound", err)
	}
}

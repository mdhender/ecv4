package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mdhender/ecv4/internal/store"
)

func boolp(b bool) *bool    { return &b }
func strp(s string) *string { return &s }

func TestUpdateMember(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'a@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'b@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")
	// Account 1 is a dropped player named 'Rome'; account 2 is an active player.
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 1, 'Rome', 0, 0);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 2, 'Egypt', 0, 1);")

	// Reactivate + promote + rename in one update.
	got, err := st.UpdateMember(ctx, 10, 1, store.MemberUpdate{
		IsActive: boolp(true), IsGM: boolp(true), Handle: strp("Overlord"),
	})
	if err != nil {
		t.Fatalf("UpdateMember: %v", err)
	}
	if got != (store.Member{AccountID: 1, Handle: "Overlord", IsGM: true, IsActive: true}) {
		t.Fatalf("got %+v, want reactivated GM Overlord", got)
	}

	// The change persisted.
	if reread, err := st.MemberForGame(ctx, 10, 1); err != nil || !reread.IsActive || !reread.IsGM || reread.Handle != "Overlord" {
		t.Fatalf("reread = %+v, err %v; want active GM Overlord", reread, err)
	}

	// Renaming onto another member's handle is ErrHandleTaken.
	if _, err := st.UpdateMember(ctx, 10, 1, store.MemberUpdate{Handle: strp("Egypt")}); !errors.Is(err, store.ErrHandleTaken) {
		t.Fatalf("duplicate handle: got %v, want ErrHandleTaken", err)
	}

	// Renaming to the member's own current handle is allowed (self-collision is not
	// a conflict).
	if _, err := st.UpdateMember(ctx, 10, 1, store.MemberUpdate{Handle: strp("Overlord")}); err != nil {
		t.Fatalf("rename to same handle: %v", err)
	}

	// Updating a nonexistent membership is ErrNotFound.
	if _, err := st.UpdateMember(ctx, 10, 999, store.MemberUpdate{IsActive: boolp(true)}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown member: got %v, want ErrNotFound", err)
	}

	// An empty update is an error, not a silent success.
	if _, err := st.UpdateMember(ctx, 10, 1, store.MemberUpdate{}); err == nil {
		t.Fatal("empty update: expected an error")
	}
}

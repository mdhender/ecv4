package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mdhender/ecv4/internal/store"
)

func TestUpdateGame(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	desc := "First playtest."
	exec(t, pool, "INSERT INTO games(id, code, name, status, description, is_active) VALUES(10, 'ALPHA', 'Alpha', 'draft', ?, 1);", desc)

	// Status + name + description + is_active in one update; the merged Game comes back.
	newName, newDesc := "Alpha Campaign", "A revised blurb."
	got, err := st.UpdateGame(ctx, 10, store.GameUpdate{
		Status: strp("recruiting"), Name: strp(newName), Description: strp(newDesc), IsActive: boolp(false),
	})
	if err != nil {
		t.Fatalf("UpdateGame: %v", err)
	}
	want := store.Game{ID: 10, Code: "ALPHA", Name: newName, Status: "recruiting", Description: &newDesc, IsActive: false}
	if got.ID != want.ID || got.Code != want.Code || got.Name != want.Name || got.Status != want.Status ||
		got.Description == nil || *got.Description != newDesc || got.IsActive {
		t.Fatalf("UpdateGame = %+v, want %+v", got, want)
	}

	// The change persisted (admin can still read the hidden game).
	reread, err := st.GameByID(ctx, 10, 0, true)
	if err != nil {
		t.Fatalf("GameByID: %v", err)
	}
	if reread.Status != "recruiting" || reread.Name != newName || reread.IsActive {
		t.Fatalf("reread = %+v, want recruiting / renamed / hidden", reread)
	}

	// A single-field update leaves the rest untouched.
	if _, err := st.UpdateGame(ctx, 10, store.GameUpdate{Status: strp("active")}); err != nil {
		t.Fatalf("UpdateGame(status only): %v", err)
	}
	if g, _ := st.GameByID(ctx, 10, 0, true); g.Status != "active" || g.Name != newName {
		t.Fatalf("after status-only update = %+v, want active and name preserved", g)
	}

	// Unknown id → ErrNotFound; empty update → error.
	if _, err := st.UpdateGame(ctx, 999, store.GameUpdate{Status: strp("active")}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown game: got %v, want ErrNotFound", err)
	}
	if _, err := st.UpdateGame(ctx, 10, store.GameUpdate{}); err == nil {
		t.Fatal("empty update: expected an error")
	}
}

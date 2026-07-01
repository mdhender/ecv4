package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	pool, err := database.CreateSharedMemory(context.Background(), "")
	if err != nil {
		t.Fatalf("CreateSharedMemory: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return store.New(pool)
}

func TestCreateAccountAndLookup(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	id, err := st.CreateAccount(ctx, "admin@example.com", true, true, "bcrypt-hash")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if id <= 0 {
		t.Fatalf("CreateAccount returned id %d, want > 0", id)
	}

	account, err := st.AccountByID(ctx, id)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if account.Email != "admin@example.com" || !account.IsAdmin || !account.IsActive {
		t.Fatalf("unexpected account: %+v", account)
	}

	// Credentials returns the same account plus the stored hash.
	got, hash, err := st.Credentials(ctx, "admin@example.com")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if got.ID != id || hash != "bcrypt-hash" {
		t.Fatalf("Credentials mismatch: id=%d hash=%q", got.ID, hash)
	}
}

func TestCreateAccountDuplicateEmail(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	if _, err := st.CreateAccount(ctx, "dup@example.com", false, true, "h"); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	_, err := st.CreateAccount(ctx, "dup@example.com", false, true, "h")
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second CreateAccount: got %v, want ErrConflict", err)
	}
}

func TestLookupsNotFound(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	if _, err := st.AccountByID(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AccountByID unknown: got %v, want ErrNotFound", err)
	}
	if _, _, err := st.Credentials(ctx, "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Credentials unknown: got %v, want ErrNotFound", err)
	}
}

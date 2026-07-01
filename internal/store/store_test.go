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

func TestUpdateAccountByEmail(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	id, err := st.CreateAccount(ctx, "u@example.com", false, true, "oldhash")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	admin, inactive, newHash := true, false, "newhash"
	err = st.UpdateAccountByEmail(ctx, "u@example.com", store.AccountUpdate{
		IsAdmin: &admin, IsActive: &inactive, HashedSecret: &newHash,
	})
	if err != nil {
		t.Fatalf("UpdateAccountByEmail: %v", err)
	}

	account, err := st.AccountByID(ctx, id)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if !account.IsAdmin || account.IsActive {
		t.Fatalf("after update: %+v, want is_admin=true is_active=false", account)
	}
	if _, hash, _ := st.Credentials(ctx, "u@example.com"); hash != "newhash" {
		t.Fatalf("hash = %q, want newhash", hash)
	}
}

func TestUpdateAccountPartialLeavesOthersUnchanged(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	id, err := st.CreateAccount(ctx, "p@example.com", true, true, "keephash")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Only toggle is_active; is_admin and the secret must be untouched.
	inactive := false
	if err := st.UpdateAccountByEmail(ctx, "p@example.com", store.AccountUpdate{IsActive: &inactive}); err != nil {
		t.Fatalf("UpdateAccountByEmail: %v", err)
	}

	account, _ := st.AccountByID(ctx, id)
	if !account.IsAdmin {
		t.Fatal("is_admin should remain true")
	}
	if account.IsActive {
		t.Fatal("is_active should be false")
	}
	if _, hash, _ := st.Credentials(ctx, "p@example.com"); hash != "keephash" {
		t.Fatalf("secret changed to %q, should be unchanged", hash)
	}
}

func TestUpdateAccountNotFound(t *testing.T) {
	st := newStore(t)
	active := true
	err := st.UpdateAccountByEmail(context.Background(), "ghost@example.com", store.AccountUpdate{IsActive: &active})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestUpdateAccountEmptyIsError(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	if _, err := st.CreateAccount(ctx, "e@example.com", false, true, "h"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := st.UpdateAccountByEmail(ctx, "e@example.com", store.AccountUpdate{}); err == nil {
		t.Fatal("empty update should return an error")
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

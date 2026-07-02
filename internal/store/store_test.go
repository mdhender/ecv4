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

func TestUpdateAccountByID(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	id, err := st.CreateAccount(ctx, "byid@example.com", false, true, "oldhash")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	admin, inactive, newHash := true, false, "newhash"
	err = st.UpdateAccountByID(ctx, id, store.AccountUpdate{
		IsAdmin: &admin, IsActive: &inactive, HashedSecret: &newHash,
	})
	if err != nil {
		t.Fatalf("UpdateAccountByID: %v", err)
	}

	account, err := st.AccountByID(ctx, id)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if !account.IsAdmin || account.IsActive {
		t.Fatalf("after update: %+v, want is_admin=true is_active=false", account)
	}
	if _, hash, _ := st.Credentials(ctx, "byid@example.com"); hash != "newhash" {
		t.Fatalf("hash = %q, want newhash", hash)
	}
}

func TestUpdateAccountByIDPartialLeavesOthersUnchanged(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	id, err := st.CreateAccount(ctx, "byidpartial@example.com", true, true, "keephash")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	inactive := false
	if err := st.UpdateAccountByID(ctx, id, store.AccountUpdate{IsActive: &inactive}); err != nil {
		t.Fatalf("UpdateAccountByID: %v", err)
	}

	account, _ := st.AccountByID(ctx, id)
	if !account.IsAdmin {
		t.Fatal("is_admin should remain true")
	}
	if account.IsActive {
		t.Fatal("is_active should be false")
	}
	if _, hash, _ := st.Credentials(ctx, "byidpartial@example.com"); hash != "keephash" {
		t.Fatalf("secret changed to %q, should be unchanged", hash)
	}
}

func TestUpdateAccountByIDNotFound(t *testing.T) {
	st := newStore(t)
	active := true
	err := st.UpdateAccountByID(context.Background(), 999, store.AccountUpdate{IsActive: &active})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestUpdateAccountByIDEmptyIsError(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	id, err := st.CreateAccount(ctx, "byidempty@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := st.UpdateAccountByID(ctx, id, store.AccountUpdate{}); err == nil {
		t.Fatal("empty update should return an error")
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

func TestListAccounts(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	// Empty table yields an empty slice, not nil and not an error.
	got, err := st.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty table: got %d accounts, want 0", len(got))
	}

	// Create out of id order to confirm results come back ordered by id.
	if _, err := st.CreateAccount(ctx, "second@example.com", false, true, "h2"); err != nil {
		t.Fatalf("CreateAccount second: %v", err)
	}
	if _, err := st.CreateAccount(ctx, "third@example.com", true, false, "h3"); err != nil {
		t.Fatalf("CreateAccount third: %v", err)
	}

	got, err = st.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got))
	}
	if got[0].ID >= got[1].ID {
		t.Fatalf("not ordered by id: %d then %d", got[0].ID, got[1].ID)
	}
	if got[0].Email != "second@example.com" || got[0].IsAdmin || !got[0].IsActive {
		t.Fatalf("first account: %+v", got[0])
	}
	if got[1].Email != "third@example.com" || !got[1].IsAdmin || got[1].IsActive {
		t.Fatalf("second account: %+v", got[1])
	}
}

func TestRefreshTokenCreateLookupRevoke(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	acct, err := st.CreateAccount(ctx, "rt@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	if err := st.CreateRefreshToken(ctx, "jti-1", "fam-1", acct, 100, 200); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}

	got, err := st.RefreshTokenByJTI(ctx, "jti-1")
	if err != nil {
		t.Fatalf("RefreshTokenByJTI: %v", err)
	}
	if got.FamilyID != "fam-1" || got.AccountID != acct || got.IssuedAt != 100 || got.ExpiresAt != 200 || got.Revoked {
		t.Fatalf("unexpected token: %+v", got)
	}

	if err := st.RevokeRefreshToken(ctx, "jti-1"); err != nil {
		t.Fatalf("RevokeRefreshToken: %v", err)
	}
	if got, _ := st.RefreshTokenByJTI(ctx, "jti-1"); !got.Revoked {
		t.Fatalf("token should be revoked after RevokeRefreshToken: %+v", got)
	}

	// Revoking an unknown jti is a no-op, not an error (idempotent).
	if err := st.RevokeRefreshToken(ctx, "does-not-exist"); err != nil {
		t.Fatalf("RevokeRefreshToken unknown: %v", err)
	}
}

func TestRefreshTokenByJTINotFound(t *testing.T) {
	st := newStore(t)
	if _, err := st.RefreshTokenByJTI(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestRefreshTokenDuplicateJTI(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	acct, err := st.CreateAccount(ctx, "dupjti@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "jti-dup", "fam", acct, 1, 2); err != nil {
		t.Fatalf("first CreateRefreshToken: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "jti-dup", "fam", acct, 1, 2); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second CreateRefreshToken: got %v, want ErrConflict", err)
	}
}

func TestRevokeFamily(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	acct, err := st.CreateAccount(ctx, "fam@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Two tokens in one family and one in another; RevokeFamily hits only the
	// first family.
	if err := st.CreateRefreshToken(ctx, "a", "fam-1", acct, 1, 2); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "b", "fam-1", acct, 1, 2); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "c", "fam-2", acct, 1, 2); err != nil {
		t.Fatalf("create c: %v", err)
	}

	if err := st.RevokeFamily(ctx, "fam-1"); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}
	for _, jti := range []string{"a", "b"} {
		if got, _ := st.RefreshTokenByJTI(ctx, jti); !got.Revoked {
			t.Fatalf("%q should be revoked", jti)
		}
	}
	if got, _ := st.RefreshTokenByJTI(ctx, "c"); got.Revoked {
		t.Fatal("c (other family) should not be revoked")
	}
}

func TestRevokeAllForAccount(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	one, err := st.CreateAccount(ctx, "one@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount one: %v", err)
	}
	two, err := st.CreateAccount(ctx, "two@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount two: %v", err)
	}

	if err := st.CreateRefreshToken(ctx, "one-a", "fam-a", one, 1, 2); err != nil {
		t.Fatalf("create one-a: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "one-b", "fam-b", one, 1, 2); err != nil {
		t.Fatalf("create one-b: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "two-a", "fam-c", two, 1, 2); err != nil {
		t.Fatalf("create two-a: %v", err)
	}

	if err := st.RevokeAllForAccount(ctx, one); err != nil {
		t.Fatalf("RevokeAllForAccount: %v", err)
	}
	for _, jti := range []string{"one-a", "one-b"} {
		if got, _ := st.RefreshTokenByJTI(ctx, jti); !got.Revoked {
			t.Fatalf("%q should be revoked", jti)
		}
	}
	if got, _ := st.RefreshTokenByJTI(ctx, "two-a"); got.Revoked {
		t.Fatal("two-a (other account) should not be revoked")
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

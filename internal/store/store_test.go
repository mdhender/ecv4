package store_test

import (
	"context"
	"errors"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, _ := newStorePool(t)
	return st
}

// newStorePool is newStore but also hands back the pool, for tests that seed
// tables the store has no writer for (games, game_account_role).
func newStorePool(t *testing.T) (*store.Store, *sqlitemigration.Pool) {
	t.Helper()
	pool, err := database.CreateSharedMemory(context.Background(), "")
	if err != nil {
		t.Fatalf("CreateSharedMemory: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return store.New(pool), pool
}

// exec runs a statement against the pool, failing the test on error.
func exec(t *testing.T, pool *sqlitemigration.Pool, query string, args ...any) {
	t.Helper()
	conn, err := pool.Get(context.Background())
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer pool.Put(conn)
	if err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args}); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
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

func TestPurgeExpiredRefreshTokens(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	acct, err := st.CreateAccount(ctx, "purge@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// expires_at relative to a cutoff of 100: two already expired (one revoked,
	// one not), one expiring exactly at the cutoff (also purged, since the
	// boundary is inclusive), and one still valid.
	if err := st.CreateRefreshToken(ctx, "old-live", "fam", acct, 1, 50); err != nil {
		t.Fatalf("create old-live: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "old-revoked", "fam", acct, 1, 60); err != nil {
		t.Fatalf("create old-revoked: %v", err)
	}
	if err := st.RevokeRefreshToken(ctx, "old-revoked"); err != nil {
		t.Fatalf("revoke old-revoked: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "at-cutoff", "fam", acct, 1, 100); err != nil {
		t.Fatalf("create at-cutoff: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "future", "fam", acct, 1, 200); err != nil {
		t.Fatalf("create future: %v", err)
	}

	purged, err := st.PurgeExpiredRefreshTokens(ctx, 100)
	if err != nil {
		t.Fatalf("PurgeExpiredRefreshTokens: %v", err)
	}
	if purged != 3 {
		t.Fatalf("purged = %d, want 3", purged)
	}

	for _, jti := range []string{"old-live", "old-revoked", "at-cutoff"} {
		if _, err := st.RefreshTokenByJTI(ctx, jti); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%q should be gone: err=%v", jti, err)
		}
	}
	if _, err := st.RefreshTokenByJTI(ctx, "future"); err != nil {
		t.Fatalf("future token should survive: %v", err)
	}

	// A second sweep at the same cutoff removes nothing (idempotent).
	if purged, err := st.PurgeExpiredRefreshTokens(ctx, 100); err != nil || purged != 0 {
		t.Fatalf("second purge = (%d, %v), want (0, nil)", purged, err)
	}
}

func TestSessionsForAccount(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	me, err := st.CreateAccount(ctx, "me@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount me: %v", err)
	}
	other, err := st.CreateAccount(ctx, "other@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount other: %v", err)
	}

	// fam-live: a rotated family — the old token is revoked, the newer one (bigger
	// issued_at/expires_at) is live. Only the current token's times should surface.
	if err := st.CreateRefreshToken(ctx, "live-old", "fam-live", me, 10, 500); err != nil {
		t.Fatalf("create live-old: %v", err)
	}
	if err := st.RevokeRefreshToken(ctx, "live-old"); err != nil {
		t.Fatalf("revoke live-old: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "live-new", "fam-live", me, 20, 600); err != nil {
		t.Fatalf("create live-new: %v", err)
	}
	// fam-newer: a second live session, issued later, so it sorts ahead of fam-live.
	if err := st.CreateRefreshToken(ctx, "newer", "fam-newer", me, 30, 700); err != nil {
		t.Fatalf("create newer: %v", err)
	}
	// fam-revoked: fully revoked — no live token, so it is not a session.
	if err := st.CreateRefreshToken(ctx, "revoked", "fam-revoked", me, 5, 900); err != nil {
		t.Fatalf("create revoked: %v", err)
	}
	if err := st.RevokeFamily(ctx, "fam-revoked"); err != nil {
		t.Fatalf("revoke fam-revoked: %v", err)
	}
	// fam-expired: un-revoked but expired at the query time, so not a session.
	if err := st.CreateRefreshToken(ctx, "expired", "fam-expired", me, 1, 100); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	// A bystander's live session must not leak into me's list.
	if err := st.CreateRefreshToken(ctx, "bystander", "fam-bystander", other, 15, 800); err != nil {
		t.Fatalf("create bystander: %v", err)
	}

	// now = 200: fam-expired (expires_at 100) is gone; fam-live and fam-newer remain.
	sessions, err := st.SessionsForAccount(ctx, me, 200)
	if err != nil {
		t.Fatalf("SessionsForAccount: %v", err)
	}
	want := []store.Session{
		{FamilyID: "fam-newer", IssuedAt: 30, ExpiresAt: 700},
		{FamilyID: "fam-live", IssuedAt: 20, ExpiresAt: 600},
	}
	if len(sessions) != len(want) {
		t.Fatalf("got %d sessions, want %d: %+v", len(sessions), len(want), sessions)
	}
	for i := range want {
		if sessions[i] != want[i] {
			t.Fatalf("session %d = %+v, want %+v", i, sessions[i], want[i])
		}
	}
}

func TestSessionsForAccountEmpty(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	acct, err := st.CreateAccount(ctx, "loner@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	sessions, err := st.SessionsForAccount(ctx, acct, 0)
	if err != nil {
		t.Fatalf("SessionsForAccount: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got %d sessions, want 0", len(sessions))
	}
}

func TestRevokeFamilyForAccount(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	me, err := st.CreateAccount(ctx, "me@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount me: %v", err)
	}
	other, err := st.CreateAccount(ctx, "other@example.com", false, true, "h")
	if err != nil {
		t.Fatalf("CreateAccount other: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "mine-a", "fam-mine", me, 1, 900); err != nil {
		t.Fatalf("create mine-a: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "mine-b", "fam-mine", me, 2, 900); err != nil {
		t.Fatalf("create mine-b: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "theirs", "fam-theirs", other, 1, 900); err != nil {
		t.Fatalf("create theirs: %v", err)
	}

	// Revoking the caller's own family marks every token in it revoked.
	if err := st.RevokeFamilyForAccount(ctx, "fam-mine", me); err != nil {
		t.Fatalf("RevokeFamilyForAccount own: %v", err)
	}
	for _, jti := range []string{"mine-a", "mine-b"} {
		if got, _ := st.RefreshTokenByJTI(ctx, jti); !got.Revoked {
			t.Fatalf("%q should be revoked", jti)
		}
	}

	// Idempotent: revoking it again still succeeds while the rows persist.
	if err := st.RevokeFamilyForAccount(ctx, "fam-mine", me); err != nil {
		t.Fatalf("RevokeFamilyForAccount idempotent: %v", err)
	}

	// Another account's family is not the caller's to revoke: ErrNotFound, and the
	// bystander's token is left untouched.
	if err := st.RevokeFamilyForAccount(ctx, "fam-theirs", me); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoke other's family: got %v, want ErrNotFound", err)
	}
	if got, _ := st.RefreshTokenByJTI(ctx, "theirs"); got.Revoked {
		t.Fatal("bystander's token should not be revoked")
	}

	// An unknown family is also ErrNotFound.
	if err := st.RevokeFamilyForAccount(ctx, "fam-nope", me); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoke unknown family: got %v, want ErrNotFound", err)
	}
}

func TestGamesForAccount(t *testing.T) {
	st, pool := newStorePool(t)
	ctx := context.Background()

	// Two accounts: 1 is the caller under test, 2 is a bystander whose
	// memberships must not leak into the caller's list.
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(1, 'me@example.com', 0, 1, 'x');")
	exec(t, pool, "INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret) VALUES(2, 'other@example.com', 0, 1, 'x');")

	// alpha (active) and beta (inactive/archived). The caller is a GM in alpha
	// and a player in beta; the archived game must still appear.
	exec(t, pool, "INSERT INTO games(id, code, is_active) VALUES(10, 'ALPHA', 1);")
	exec(t, pool, "INSERT INTO games(id, code, is_active) VALUES(20, 'BETA', 0);")
	exec(t, pool, "INSERT INTO games(id, code, is_active) VALUES(30, 'GAMMA', 1);")

	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 1, 'Overlord', 1, 1);")
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(20, 1, 'Rome', 0, 1);")
	// A dropped membership (is_active = 0) must be excluded.
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(30, 1, 'Carthage', 0, 0);")
	// The bystander is in alpha; it must not surface for the caller.
	exec(t, pool, "INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(10, 2, 'Egypt', 0, 1);")

	got, err := st.GamesForAccount(ctx, 1)
	if err != nil {
		t.Fatalf("GamesForAccount: %v", err)
	}
	want := []store.GameMembership{
		{GameID: 10, Code: "ALPHA", IsActive: true, Handle: "Overlord", IsGM: true},
		{GameID: 20, Code: "BETA", IsActive: false, Handle: "Rome", IsGM: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d memberships, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("membership %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// An account in no games yields an empty slice, not an error.
	none, err := st.GamesForAccount(ctx, 999)
	if err != nil {
		t.Fatalf("GamesForAccount(unknown): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("got %d memberships for unknown account, want 0", len(none))
	}
}

func TestCreateGame(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	desc := "The first playtest game."
	game, err := st.CreateGame(ctx, "ALPHA", "Alpha Campaign", &desc)
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}
	if game.ID == 0 {
		t.Fatal("CreateGame returned a zero id")
	}
	// A new game starts in draft, active, with the given code/name/description.
	want := store.Game{
		ID:          game.ID,
		Code:        "ALPHA",
		Name:        "Alpha Campaign",
		Status:      "draft",
		Description: &desc,
		IsActive:    true,
	}
	if game.Code != want.Code || game.Name != want.Name || game.Status != want.Status ||
		game.Description == nil || *game.Description != desc || !game.IsActive {
		t.Fatalf("CreateGame = %+v, want %+v", game, want)
	}

	// A nil description is stored as NULL and round-trips as nil.
	noDesc, err := st.CreateGame(ctx, "BETA", "Beta", nil)
	if err != nil {
		t.Fatalf("CreateGame(nil desc): %v", err)
	}
	if noDesc.Description != nil {
		t.Fatalf("Description = %v, want nil", *noDesc.Description)
	}

	// A duplicate code is a conflict, not a generic error.
	if _, err := st.CreateGame(ctx, "ALPHA", "Another", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate code: got %v, want ErrConflict", err)
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
	if _, err := st.AccountByEmail(ctx, "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AccountByEmail unknown: got %v, want ErrNotFound", err)
	}
	if _, err := st.GameByCode(ctx, "NOPE"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GameByCode unknown: got %v, want ErrNotFound", err)
	}
}

func TestAccountByEmail(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	id, err := st.CreateAccount(ctx, "found@example.com", true, false, "hash")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	account, err := st.AccountByEmail(ctx, "found@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if account.ID != id || account.Email != "found@example.com" || !account.IsAdmin || account.IsActive {
		t.Fatalf("AccountByEmail = %+v, want id=%d email=found@example.com is_admin=true is_active=false", account, id)
	}
}

func TestGameByCode(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	desc := "The playtest."
	created, err := st.CreateGame(ctx, "GAMMA", "Gamma Campaign", &desc)
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	game, err := st.GameByCode(ctx, "GAMMA")
	if err != nil {
		t.Fatalf("GameByCode: %v", err)
	}
	if game.ID != created.ID || game.Code != "GAMMA" || game.Name != "Gamma Campaign" ||
		game.Status != "draft" || game.Description == nil || *game.Description != desc || !game.IsActive {
		t.Fatalf("GameByCode = %+v, want the created game", game)
	}
}

package database

import (
	"context"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// metaCount returns the number of rows in the meta table, which exists
// only if the initial migration applied to the database conn reaches.
func metaCount(t *testing.T, conn *sqlite.Conn) int {
	t.Helper()
	var n int
	err := sqlitex.Execute(conn, "SELECT count(*) FROM meta;", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			n = stmt.ColumnInt(0)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("query meta: %v", err)
	}
	return n
}

func TestCreateMemoryAppliesMigrations(t *testing.T) {
	ctx := context.Background()

	conn, err := CreateMemory(ctx)
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	defer conn.Close()

	// The migration created the meta table; it should be present and empty.
	if got := metaCount(t, conn); got != 0 {
		t.Fatalf("meta rows = %d, want 0", got)
	}
}

func TestCreateMemoryIsPrivatePerCall(t *testing.T) {
	ctx := context.Background()

	a, err := CreateMemory(ctx)
	if err != nil {
		t.Fatalf("CreateMemory a: %v", err)
	}
	defer a.Close()

	// Writing to a's database must not be visible to a second, independent
	// in-memory database.
	if err := sqlitex.Execute(a, "INSERT INTO meta(key, value) VALUES('k', 'v');", nil); err != nil {
		t.Fatalf("insert into a: %v", err)
	}

	b, err := CreateMemory(ctx)
	if err != nil {
		t.Fatalf("CreateMemory b: %v", err)
	}
	defer b.Close()

	if got := metaCount(t, b); got != 0 {
		t.Fatalf("b sees %d rows from a; the databases are not isolated", got)
	}
}

func TestCreateSharedMemorySharesAcrossConnections(t *testing.T) {
	ctx := context.Background()

	pool, err := CreateSharedMemory(ctx, "")
	if err != nil {
		t.Fatalf("CreateSharedMemory: %v", err)
	}
	defer pool.Close()

	// Write on one pooled connection...
	w, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("get write conn: %v", err)
	}
	if err := sqlitex.Execute(w, "INSERT INTO meta(key, value) VALUES('k', 'v');", nil); err != nil {
		t.Fatalf("insert: %v", err)
	}
	pool.Put(w)

	// ...and read it back on another connection from the same pool.
	r, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("get read conn: %v", err)
	}
	defer pool.Put(r)

	if got := metaCount(t, r); got != 1 {
		t.Fatalf("shared meta rows = %d, want 1", got)
	}
}

func TestCreateSharedMemoryEmptyNameIsolatesCalls(t *testing.T) {
	ctx := context.Background()

	p1, err := CreateSharedMemory(ctx, "")
	if err != nil {
		t.Fatalf("CreateSharedMemory p1: %v", err)
	}
	defer p1.Close()

	c1, err := p1.Get(ctx)
	if err != nil {
		t.Fatalf("get p1 conn: %v", err)
	}
	if err := sqlitex.Execute(c1, "INSERT INTO meta(key, value) VALUES('k', 'v');", nil); err != nil {
		t.Fatalf("insert p1: %v", err)
	}
	p1.Put(c1)

	p2, err := CreateSharedMemory(ctx, "")
	if err != nil {
		t.Fatalf("CreateSharedMemory p2: %v", err)
	}
	defer p2.Close()

	c2, err := p2.Get(ctx)
	if err != nil {
		t.Fatalf("get p2 conn: %v", err)
	}
	defer p2.Put(c2)

	if got := metaCount(t, c2); got != 0 {
		t.Fatalf("p2 sees %d rows from p1; empty-name pools are not isolated", got)
	}
}

// exec runs a statement and returns any error, for tests that assert a
// constraint either accepts or rejects a row.
func exec(conn *sqlite.Conn, query string) error {
	return sqlitex.Execute(conn, query, nil)
}

// mustExec fails the test if the statement does not succeed.
func mustExec(t *testing.T, conn *sqlite.Conn, query string) {
	t.Helper()
	if err := exec(conn, query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// wantErr fails the test if the statement succeeds.
func wantErr(t *testing.T, conn *sqlite.Conn, what, query string) {
	t.Helper()
	if err := exec(conn, query); err == nil {
		t.Fatalf("%s: statement unexpectedly succeeded: %q", what, query)
	}
}

func TestAccountsConstraints(t *testing.T) {
	conn, err := CreateMemory(context.Background())
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	defer conn.Close()

	mustExec(t, conn, `INSERT INTO accounts(email, is_admin, is_active, hashed_secret)
		VALUES('alice@example.com', 1, 1, 'hash');`)

	// Email is unique.
	wantErr(t, conn, "duplicate email", `INSERT INTO accounts(email, is_admin, is_active, hashed_secret)
		VALUES('alice@example.com', 0, 1, 'hash');`)

	// Boolean columns reject values outside {0,1}.
	wantErr(t, conn, "is_admin out of range", `INSERT INTO accounts(email, is_admin, is_active, hashed_secret)
		VALUES('bob@example.com', 2, 1, 'hash');`)
}

func TestGameCodeConstraint(t *testing.T) {
	conn, err := CreateMemory(context.Background())
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	defer conn.Close()

	mustExec(t, conn, `INSERT INTO games(code, is_active) VALUES('alpha-1', 1);`)
	mustExec(t, conn, `INSERT INTO games(code, is_active) VALUES('a.b_c-2', 1);`)

	wantErr(t, conn, "code unique", `INSERT INTO games(code, is_active) VALUES('alpha-1', 1);`)
	wantErr(t, conn, "code too short", `INSERT INTO games(code, is_active) VALUES('a', 1);`)
	wantErr(t, conn, "code leading digit", `INSERT INTO games(code, is_active) VALUES('1alpha', 1);`)
	wantErr(t, conn, "code uppercase", `INSERT INTO games(code, is_active) VALUES('Alpha', 1);`)
	wantErr(t, conn, "code bad char", `INSERT INTO games(code, is_active) VALUES('al pha', 1);`)
}

func TestGameAccountRoleConstraints(t *testing.T) {
	conn, err := CreateMemory(context.Background())
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	defer conn.Close()

	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(1, 'alpha', 1);`)
	mustExec(t, conn, `INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret)
		VALUES(10, 'p1@example.com', 0, 1, 'h'), (11, 'p2@example.com', 0, 1, 'h');`)

	// The by-convention GM handle "GM" is accepted by the storage-layer check.
	mustExec(t, conn, `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(1, 10, 'GM', 1, 1);`)
	mustExec(t, conn, `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(1, 11, 'player_1', 0, 1);`)

	// One account, one membership per game.
	wantErr(t, conn, "duplicate (game,account)", `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(1, 10, 'other', 0, 1);`)

	// Handles are unique within a game.
	mustExec(t, conn, `INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret)
		VALUES(12, 'p3@example.com', 0, 1, 'h');`)
	wantErr(t, conn, "duplicate handle in game", `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(1, 12, 'player_1', 0, 1);`)

	// Foreign keys are enforced (the pragma is on): a missing game is rejected.
	wantErr(t, conn, "missing game fk", `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(99, 12, 'player_2', 0, 1);`)

	// The same handle is free to reuse in a different game.
	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(2, 'beta', 1);`)
	mustExec(t, conn, `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(2, 10, 'player_1', 0, 1);`)
}

func TestForeignKeysEnforcedOnSharedMemory(t *testing.T) {
	ctx := context.Background()
	pool, err := CreateSharedMemory(ctx, "")
	if err != nil {
		t.Fatalf("CreateSharedMemory: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Get(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer pool.Put(conn)

	mustExec(t, conn, `INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret)
		VALUES(1, 'a@example.com', 0, 1, 'h');`)
	wantErr(t, conn, "missing game fk", `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(404, 1, 'player_1', 0, 1);`)
}

func TestCreateMemoryPathSmokeTest(t *testing.T) {
	// Create with the special MemoryPath should apply migrations against an
	// ephemeral database and succeed without touching the filesystem.
	if err := Create(context.Background(), MemoryPath); err != nil {
		t.Fatalf("Create(MemoryPath): %v", err)
	}
}

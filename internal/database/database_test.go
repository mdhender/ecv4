package database

import (
	"context"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
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

	mustExec(t, conn, `INSERT INTO games(code, is_active) VALUES('ALPHA', 1);`)
	mustExec(t, conn, `INSERT INTO games(code, is_active) VALUES('AB', 1);`)

	wantErr(t, conn, "code unique", `INSERT INTO games(code, is_active) VALUES('ALPHA', 1);`)
	wantErr(t, conn, "code too short", `INSERT INTO games(code, is_active) VALUES('A', 1);`)
	wantErr(t, conn, "code lowercase", `INSERT INTO games(code, is_active) VALUES('alpha', 1);`)
	wantErr(t, conn, "code leading digit", `INSERT INTO games(code, is_active) VALUES('1ALPHA', 1);`)
	wantErr(t, conn, "code digit", `INSERT INTO games(code, is_active) VALUES('ALPHA1', 1);`)
	wantErr(t, conn, "code hyphen", `INSERT INTO games(code, is_active) VALUES('ALPHA-1', 1);`)
	wantErr(t, conn, "code bad char", `INSERT INTO games(code, is_active) VALUES('AL PHA', 1);`)
}

// TestGamesCodeUppercaseMigration exercises migration 0006, which rebuilds the
// games table under the strict [A-Z][A-Z]+ code rule. Rows whose code is
// lowercase letters survive with their code upper-cased; rows whose code carries
// digits or punctuation cannot satisfy the new CHECK even upper-cased and are
// thrown away, and the game_account_role rows that referenced them go with them
// so no dangling foreign key remains.
func TestGamesCodeUppercaseMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, FileName)

	// Stand up a database at the schema version just before 0006, where the old
	// games CHECK still accepts lowercase codes with digits and punctuation.
	older := sqlitemigration.NewPool(dbPath, sqlitemigration.Schema{
		AppID:      appID,
		Migrations: migrations[:5],
	}, sqlitemigration.Options{
		Flags:       sqlite.OpenReadWrite | sqlite.OpenCreate | sqlite.OpenWAL | sqlite.OpenURI,
		PrepareConn: enableForeignKeys,
	})
	conn, err := older.Get(ctx)
	if err != nil {
		t.Fatalf("seed older db: %v", err)
	}
	mustExec(t, conn, `INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret)
		VALUES(1, 'p@example.com', 0, 1, 'h');`)
	// alpha and beta are pure lowercase letters: they survive, upper-cased.
	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(1, 'alpha', 1);`)
	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(2, 'beta', 1);`)
	// alpha-1 carries a hyphen and digit: even upper-cased it fails the new CHECK,
	// so it is dropped along with its membership row.
	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(3, 'alpha-1', 1);`)
	mustExec(t, conn, `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(1, 1, 'Rome', 1, 1);`)
	mustExec(t, conn, `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(3, 1, 'Egypt', 0, 1);`)
	older.Put(conn)
	if err := older.Close(); err != nil {
		t.Fatalf("close older db: %v", err)
	}

	// Open runs migration 0006, rebuilding the games table.
	pool, closeDB, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer closeDB()
	conn, err = pool.Get(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer pool.Put(conn)

	// Survivors are upper-cased; the punctuation code is gone.
	var codes []string
	if err := sqlitex.Execute(conn, `SELECT code FROM games ORDER BY id;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			codes = append(codes, stmt.ColumnText(0))
			return nil
		},
	}); err != nil {
		t.Fatalf("select codes: %v", err)
	}
	if want := []string{"ALPHA", "BETA"}; len(codes) != len(want) || codes[0] != want[0] || codes[1] != want[1] {
		t.Fatalf("codes = %v, want %v", codes, want)
	}

	// The membership for the dropped game is gone; the survivor's remains.
	var roleGameIDs []int
	if err := sqlitex.Execute(conn, `SELECT game_id FROM game_account_role ORDER BY game_id;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			roleGameIDs = append(roleGameIDs, stmt.ColumnInt(0))
			return nil
		},
	}); err != nil {
		t.Fatalf("select roles: %v", err)
	}
	if len(roleGameIDs) != 1 || roleGameIDs[0] != 1 {
		t.Fatalf("role game_ids = %v, want [1]", roleGameIDs)
	}

	// The new CHECK is in force: a lowercase code is rejected, and a foreign key
	// into the rebuilt games table still resolves.
	wantErr(t, conn, "lowercase rejected after rebuild", `INSERT INTO games(code, is_active) VALUES('gamma', 1);`)
	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(4, 'GAMMA', 1);`)
	mustExec(t, conn, `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(4, 1, 'Carthage', 0, 1);`)
	wantErr(t, conn, "fk into rebuilt games", `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(99, 1, 'Nowhere', 0, 1);`)
}

func TestGameAccountRoleConstraints(t *testing.T) {
	conn, err := CreateMemory(context.Background())
	if err != nil {
		t.Fatalf("CreateMemory: %v", err)
	}
	defer conn.Close()

	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(1, 'ALPHA', 1);`)
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
	mustExec(t, conn, `INSERT INTO games(id, code, is_active) VALUES(2, 'BETA', 1);`)
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

func TestOpenRoundTripsWithCreate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	if err := Create(ctx, dir); err != nil {
		t.Fatalf("Create: %v", err)
	}

	pool, closeDB, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := closeDB(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()

	conn, err := pool.Take(ctx)
	if err != nil {
		t.Fatalf("take conn: %v", err)
	}
	defer pool.Put(conn)

	// The schema Create applied is visible through the opened pool...
	if got := metaCount(t, conn); got != 0 {
		t.Fatalf("meta rows = %d, want 0", got)
	}
	// ...and foreign keys are enforced on pooled connections.
	mustExec(t, conn, `INSERT INTO accounts(id, email, is_admin, is_active, hashed_secret)
		VALUES(1, 'a@example.com', 0, 1, 'h');`)
	wantErr(t, conn, "missing game fk", `INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active)
		VALUES(404, 1, 'player_1', 0, 1);`)
}

func TestOpenMigratesOlderInstanceForward(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, FileName)

	// Stand up a database at an OLDER schema version: same AppID, but only the
	// first migration applied (the meta table; no games table yet). This is
	// what an existing instance from an earlier release looks like.
	older := sqlitemigration.NewPool(dbPath, sqlitemigration.Schema{
		AppID:      appID,
		Migrations: migrations[:1],
	}, sqlitemigration.Options{
		Flags:       sqlite.OpenReadWrite | sqlite.OpenCreate | sqlite.OpenWAL | sqlite.OpenURI,
		PrepareConn: enableForeignKeys,
	})
	conn, err := older.Get(ctx)
	if err != nil {
		t.Fatalf("seed older db: %v", err)
	}
	wantErr(t, conn, "games table should not exist yet", `SELECT 1 FROM games;`)
	older.Put(conn)
	if err := older.Close(); err != nil {
		t.Fatalf("close older db: %v", err)
	}

	// Open must run the remaining migrations every time, bringing this existing
	// instance current.
	pool, closeDB, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer closeDB()

	conn, err = pool.Get(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	defer pool.Put(conn)

	// The games table from a later migration now exists.
	mustExec(t, conn, `INSERT INTO games(code, is_active) VALUES('ALPHA', 1);`)
}

func TestOpenCloseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := Create(ctx, dir); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, closeDB, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := closeDB(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// A second close must not panic (Pool.Close is not idempotent on its own)
	// and must report the same result.
	if err := closeDB(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestOpenMissingDatabase(t *testing.T) {
	dir := t.TempDir() // directory exists, but no database file inside it
	if _, _, err := Open(context.Background(), dir); err == nil {
		t.Fatal("Open of a directory without a database unexpectedly succeeded")
	}
}

func TestOpenMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if _, _, err := Open(context.Background(), missing); err == nil {
		t.Fatal("Open of a missing directory unexpectedly succeeded")
	}
}

func TestOpenRejectsForeignDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Build a non-ecv4 SQLite file at the expected path: a valid database with
	// no application_id stamp.
	dbPath := filepath.Join(dir, FileName)
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		t.Fatalf("seed foreign db: %v", err)
	}
	mustExec(t, conn, `CREATE TABLE unrelated (x INTEGER);`)
	if err := conn.Close(); err != nil {
		t.Fatalf("close foreign db: %v", err)
	}

	if _, _, err := Open(ctx, dir); err == nil {
		t.Fatal("Open of a non-ecv4 database unexpectedly succeeded")
	}
}

func TestCreateMemoryPathSmokeTest(t *testing.T) {
	// Create with the special MemoryPath should apply migrations against an
	// ephemeral database and succeed without touching the filesystem.
	if err := Create(context.Background(), MemoryPath); err != nil {
		t.Fatalf("Create(MemoryPath): %v", err)
	}
}

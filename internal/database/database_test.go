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

func TestCreateMemoryPathSmokeTest(t *testing.T) {
	// Create with the special MemoryPath should apply migrations against an
	// ephemeral database and succeed without touching the filesystem.
	if err := Create(context.Background(), MemoryPath); err != nil {
		t.Fatalf("Create(MemoryPath): %v", err)
	}
}

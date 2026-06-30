// Package database owns creation and migration of the game's SQLite
// database. By design, only Create (and its in-memory siblings) may bring
// a new database into existence; every other entry point opens an
// existing database.
package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// FileName is the fixed name of the database file within a database
// directory. Callers supply a directory path; the file name is never
// theirs to choose.
const FileName = "ecv4.db"

// MemoryPath is the special directory value that asks Create to build an
// ephemeral in-memory database instead of a file on disk. It is mainly a
// convenience for the CLI to smoke-test that migrations apply cleanly;
// tests that need a usable handle should call CreateMemory or
// CreateSharedMemory instead.
const MemoryPath = ":memory:"

// memCounter makes auto-generated in-memory database names unique within
// a process so that independent CreateSharedMemory callers do not collide.
var memCounter atomic.Uint64

// Create initializes a new database named FileName inside dir and runs the
// initial migrations against it.
//
// Create is the only function permitted to bring a database file into
// existence. It enforces these rules:
//
//   - dir must already exist and be a directory. Create never creates a
//     directory.
//   - The database file must not already exist. Create never overwrites
//     or reuses an existing database.
//
// As a special case, a dir of MemoryPath (":memory:") creates an ephemeral
// in-memory database, applies the migrations, and discards it. Nothing is
// written to disk; this only verifies that the migrations apply.
func Create(ctx context.Context, dir string) (err error) {
	if dir == MemoryPath {
		conn, err := CreateMemory(ctx)
		if err != nil {
			return err
		}
		if err := conn.Close(); err != nil {
			return fmt.Errorf("close in-memory database: %w", err)
		}
		return nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%q: directory does not exist", dir)
		}
		return fmt.Errorf("%q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q: not a directory", dir)
	}

	dbPath := filepath.Join(dir, FileName)
	if _, err := os.Stat(dbPath); err == nil {
		return fmt.Errorf("%q: database already exists", dbPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%q: %w", dbPath, err)
	}

	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenReadWrite|sqlite.OpenCreate|sqlite.OpenWAL|sqlite.OpenURI)
	if err != nil {
		return fmt.Errorf("open %q: %w", dbPath, err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %q: %w", dbPath, closeErr)
		}
	}()

	if err := sqlitemigration.Migrate(ctx, conn, schema()); err != nil {
		return fmt.Errorf("migrate %q: %w", dbPath, err)
	}

	return nil
}

// CreateMemory creates a private, ephemeral in-memory database, runs the
// migrations, and returns the open connection.
//
// The returned connection is the ONLY handle to this database: zombiezen
// gives every plain ":memory:" connection its own private database, so it
// cannot be shared with other connections or a pool. The caller owns the
// connection and must Close it; closing destroys the database.
//
// Use this for unit tests that need a single isolated connection. Use
// CreateSharedMemory when several connections must see the same data.
func CreateMemory(ctx context.Context) (*sqlite.Conn, error) {
	conn, err := sqlite.OpenConn(MemoryPath, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return nil, fmt.Errorf("open in-memory database: %w", err)
	}
	if err := sqlitemigration.Migrate(ctx, conn, schema()); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate in-memory database: %w", err)
	}
	return conn, nil
}

// CreateSharedMemory creates a shared in-memory database, runs the
// migrations, and returns a pool whose connections all see the same data.
//
// The database lives only while the pool is open; closing the pool
// destroys it. The name selects which in-memory database the pool backs:
//
//   - An empty name yields a freshly generated, process-unique name, so
//     each call is isolated from every other in-memory database.
//   - A non-empty name reaches the database of that name, so independent
//     callers using the same name share one database. The name must be a
//     simple identifier (it becomes part of a file: URI).
//
// Use this for tests that need a connection pool or that exercise code
// reaching the database from more than one connection.
func CreateSharedMemory(ctx context.Context, name string) (*sqlitemigration.Pool, error) {
	if name == "" {
		name = fmt.Sprintf("ecv4-mem-%d", memCounter.Add(1))
	}
	uri := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)

	pool := sqlitemigration.NewPool(uri, schema(), sqlitemigration.Options{
		// A shared in-memory database cannot use WAL, so set flags
		// explicitly rather than taking the WAL-enabled default. OpenURI
		// is required so the mode/cache query parameters are honored.
		Flags: sqlite.OpenReadWrite | sqlite.OpenCreate | sqlite.OpenURI,
	})

	// Force the background migration to complete now so any error surfaces
	// from this call rather than from the caller's first query.
	conn, err := pool.Get(ctx)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate shared in-memory database %q: %w", name, err)
	}
	pool.Put(conn)

	return pool, nil
}

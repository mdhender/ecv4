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
	"sync"
	"sync/atomic"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
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

// enableForeignKeys turns on foreign-key enforcement for conn. The pragma is
// per-connection and not persisted, so it must be set on every connection
// that touches data; otherwise the schema's REFERENCES clauses are advisory
// only. It is a no-op inside a transaction, so it is run on freshly opened
// connections before any work begins.
func enableForeignKeys(conn *sqlite.Conn) error {
	if err := sqlitex.ExecuteTransient(conn, "PRAGMA foreign_keys = ON;", nil); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	return nil
}

// requireDir verifies that dir already exists and is a directory, returning a
// descriptive error otherwise. It never creates anything, enforcing the rule
// that callers supply a database directory and this package only ever opens or
// writes the FileName within it.
func requireDir(dir string) error {
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
	return nil
}

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

	if err := requireDir(dir); err != nil {
		return err
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

	if err := enableForeignKeys(conn); err != nil {
		return fmt.Errorf("%q: %w", dbPath, err)
	}

	if err := sqlitemigration.Migrate(ctx, conn, schema()); err != nil {
		return fmt.Errorf("migrate %q: %w", dbPath, err)
	}

	return nil
}

// Open opens the existing database named FileName inside dir, brings its schema
// current by running any pending migrations, and returns a connection pool
// together with a function that closes it.
//
// Open never creates a database. dir must already exist and be a directory, and
// dir/FileName must already exist; a missing database is an error, not an
// invitation to create one. Create is the only entry point that brings a
// database into existence, so Open is the normal way every other caller (the
// server, CLI tools) reaches an established database.
//
// Migrations run on every Open. Create applies the migrations once, when it
// first builds the file; it cannot bring an existing, older instance forward.
// Open is therefore the upgrade path: each time a process opens the database it
// applies whatever migrations have been appended since the file was last
// touched. Because migrations are append-only and tracked, opening an
// already-current database is a no-op. Opening a SQLite file that belongs to a
// different application (a mismatched application_id) is rejected here.
//
// The returned pool is safe for concurrent use by multiple goroutines:
// zombiezen serves many accessors in one process from a pool of connections,
// and WAL mode lets readers run concurrently with a single writer. Acquire a
// connection with pool.Take(ctx) (or pool.Get) and always return it with
// pool.Put.
//
// The returned close function shuts the pool down, interrupting in-flight
// queries, checkpointing the WAL, and closing every connection. The caller
// MUST call it — typically via defer, or from the server's graceful-shutdown
// path — or it leaks connections and risks leaving WAL state behind. The
// returned function is idempotent: calling it more than once is safe and
// returns the same error as the first call.
//
// Context note: ctx governs the open-time work below (waiting for the initial
// migration to finish, and the forced first connection). Per-request
// cancellation is wired per connection by the pool: pool.Take(ctx) binds ctx to
// that connection so a cancelled or timed-out ctx interrupts the running query
// (SQLITE_INTERRUPT) until the connection is Put back. zombiezen does not trap
// OS signals itself; callers turn signals into a cancelable context (as
// cmd/game-server already does with signal.NotifyContext) and pass it down to
// Take and to query execution.
func Open(ctx context.Context, dir string) (*sqlitemigration.Pool, func() error, error) {
	if err := requireDir(dir); err != nil {
		return nil, nil, err
	}

	dbPath := filepath.Join(dir, FileName)
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("%q: database does not exist", dbPath)
		}
		return nil, nil, fmt.Errorf("%q: %w", dbPath, err)
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("%q: is a directory, not a database file", dbPath)
	}

	pool := sqlitemigration.NewPool(dbPath, schema(), sqlitemigration.Options{
		// Deliberately omit OpenCreate so the pool can never bring a database
		// into existence; that is Create's job alone. OpenURI matches Create
		// and permits file: URIs. PrepareConn enforces foreign keys on every
		// pooled connection.
		Flags:       sqlite.OpenReadWrite | sqlite.OpenWAL | sqlite.OpenURI,
		PrepareConn: enableForeignKeys,
	})

	// Force the background migration to complete now so any error — a failed
	// migration, a mismatched application_id, or a database that vanished
	// between the stat above and here — surfaces from Open rather than from the
	// caller's first query.
	conn, err := pool.Get(ctx)
	if err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("open %q: %w", dbPath, err)
	}
	pool.Put(conn)

	var once sync.Once
	var closeErr error
	closeFn := func() error {
		once.Do(func() { closeErr = pool.Close() })
		return closeErr
	}
	return pool, closeFn, nil
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
	if err := enableForeignKeys(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("in-memory database: %w", err)
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
		// Enforce foreign keys on every pooled connection.
		PrepareConn: enableForeignKeys,
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

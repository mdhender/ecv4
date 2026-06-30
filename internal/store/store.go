// Package store is the data-access layer over an open database pool. It wraps a
// zombiezen connection pool and exposes typed query methods; callers hand it a
// pool from internal/database (Open) and never touch SQL directly.
package store

import (
	"context"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Store provides query and transaction methods backed by a connection pool.
type Store struct {
	pool *sqlitemigration.Pool
}

// New returns a Store backed by pool. The pool is owned by the caller (it came
// from database.Open); Store neither opens nor closes it.
func New(pool *sqlitemigration.Pool) *Store {
	return &Store{pool: pool}
}

// SchemaVersion returns the database schema version: the number of migrations
// applied to the open database, tracked by SQLite's user_version pragma and
// maintained by sqlitemigration.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) SchemaVersion(ctx context.Context) (int32, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	defer s.pool.Put(conn)

	var version int32
	err = sqlitex.ExecuteTransient(conn, "PRAGMA user_version;", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			version = stmt.ColumnInt32(0)
			return nil
		},
	})
	if err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	return version, nil
}

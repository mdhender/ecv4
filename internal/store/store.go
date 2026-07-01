// Package store is the data-access layer over an open database pool. It wraps a
// zombiezen connection pool and exposes typed query methods; callers hand it a
// pool from internal/database (Open) and never touch SQL directly.
package store

import (
	"context"
	"errors"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ErrNotFound is returned by lookups that match no row.
var ErrNotFound = errors.New("not found")

// Account is a row from the accounts table, without the hashed secret, which
// never leaves the store.
type Account struct {
	ID       int64
	Email    string
	IsAdmin  bool
	IsActive bool
}

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

// AccountByID returns the account with the given id. It returns ErrNotFound if
// no such account exists. The returned account may be inactive; callers decide
// whether that is acceptable.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) AccountByID(ctx context.Context, id int64) (Account, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("account by id: %w", err)
	}
	defer s.pool.Put(conn)

	account := Account{ID: id}
	found := false
	err = sqlitex.Execute(conn, "SELECT email, is_admin, is_active FROM accounts WHERE id = ?;", &sqlitex.ExecOptions{
		Args: []any{id},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			found = true
			account.Email = stmt.ColumnText(0)
			account.IsAdmin = stmt.ColumnInt(1) != 0
			account.IsActive = stmt.ColumnInt(2) != 0
			return nil
		},
	})
	if err != nil {
		return Account{}, fmt.Errorf("account by id %d: %w", id, err)
	}
	if !found {
		return Account{}, ErrNotFound
	}
	return account, nil
}

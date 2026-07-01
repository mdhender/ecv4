// Package store is the data-access layer over an open database pool. It wraps a
// zombiezen connection pool and exposes typed query methods; callers hand it a
// pool from internal/database (Open) and never touch SQL directly.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ErrNotFound is returned by lookups that match no row.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned by inserts that violate a uniqueness constraint, such
// as creating an account with an email that already exists.
var ErrConflict = errors.New("already exists")

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

// CreateAccount inserts a new account and returns its id. email must already be
// normalized (lower-cased) and hashedSecret must be a bcrypt hash; the store
// stores exactly what it is given. It returns ErrConflict if the email is
// already taken.
//
// ctx bounds acquiring the connection and running the insert.
func (s *Store) CreateAccount(ctx context.Context, email string, isAdmin, isActive bool, hashedSecret string) (int64, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("create account: %w", err)
	}
	defer s.pool.Put(conn)

	err = sqlitex.Execute(conn,
		"INSERT INTO accounts(email, is_admin, is_active, hashed_secret) VALUES(?, ?, ?, ?);",
		&sqlitex.ExecOptions{Args: []any{email, boolToInt(isAdmin), boolToInt(isActive), hashedSecret}})
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.ResultConstraintUnique {
			return 0, fmt.Errorf("create account %q: %w", email, ErrConflict)
		}
		return 0, fmt.Errorf("create account %q: %w", email, err)
	}
	return conn.LastInsertRowID(), nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// AccountUpdate describes a partial update to an account. A nil field is left
// unchanged; a non-nil field is written. HashedSecret, when set, must already
// be a bcrypt hash.
type AccountUpdate struct {
	IsAdmin      *bool
	IsActive     *bool
	HashedSecret *string
}

// empty reports whether the update requests no changes.
func (u AccountUpdate) empty() bool {
	return u.IsAdmin == nil && u.IsActive == nil && u.HashedSecret == nil
}

// UpdateAccountByEmail applies upd to the account with the given (normalized)
// email. It returns ErrNotFound if no account has that email, and an error if
// upd requests no changes. Only the fields set in upd are written.
//
// ctx bounds acquiring the connection and running the update.
func (s *Store) UpdateAccountByEmail(ctx context.Context, email string, upd AccountUpdate) error {
	if upd.empty() {
		return fmt.Errorf("update account %q: no changes requested", email)
	}

	var sets []string
	var args []any
	if upd.IsAdmin != nil {
		sets = append(sets, "is_admin = ?")
		args = append(args, boolToInt(*upd.IsAdmin))
	}
	if upd.IsActive != nil {
		sets = append(sets, "is_active = ?")
		args = append(args, boolToInt(*upd.IsActive))
	}
	if upd.HashedSecret != nil {
		sets = append(sets, "hashed_secret = ?")
		args = append(args, *upd.HashedSecret)
	}
	args = append(args, email)
	query := "UPDATE accounts SET " + strings.Join(sets, ", ") + " WHERE email = ?;"

	conn, err := s.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("update account %q: %w", email, err)
	}
	defer s.pool.Put(conn)

	if err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args}); err != nil {
		return fmt.Errorf("update account %q: %w", email, err)
	}
	if conn.Changes() == 0 {
		return ErrNotFound
	}
	return nil
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

// Credentials returns the account and its stored bcrypt password hash for the
// given email, for verifying a login. It returns ErrNotFound if no account has
// that email. The hash is returned only from this method; it never rides along
// on the general Account type.
//
// ctx bounds acquiring the connection and running the query.
func (s *Store) Credentials(ctx context.Context, email string) (Account, string, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Account{}, "", fmt.Errorf("credentials: %w", err)
	}
	defer s.pool.Put(conn)

	var account Account
	var hashedSecret string
	found := false
	err = sqlitex.Execute(conn,
		"SELECT id, email, is_admin, is_active, hashed_secret FROM accounts WHERE email = ?;",
		&sqlitex.ExecOptions{
			Args: []any{email},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				account.ID = stmt.ColumnInt64(0)
				account.Email = stmt.ColumnText(1)
				account.IsAdmin = stmt.ColumnInt(2) != 0
				account.IsActive = stmt.ColumnInt(3) != 0
				hashedSecret = stmt.ColumnText(4)
				return nil
			},
		})
	if err != nil {
		return Account{}, "", fmt.Errorf("credentials for %q: %w", email, err)
	}
	if !found {
		return Account{}, "", ErrNotFound
	}
	return account, hashedSecret, nil
}

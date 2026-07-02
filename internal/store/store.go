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

// UpdateAccountByID applies upd to the account with the given id. It returns
// ErrNotFound if no account has that id, and an error if upd requests no
// changes. Only the fields set in upd are written. It mirrors
// UpdateAccountByEmail, selecting by id instead of email.
//
// ctx bounds acquiring the connection and running the update.
func (s *Store) UpdateAccountByID(ctx context.Context, id int64, upd AccountUpdate) error {
	if upd.empty() {
		return fmt.Errorf("update account %d: no changes requested", id)
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
	args = append(args, id)
	query := "UPDATE accounts SET " + strings.Join(sets, ", ") + " WHERE id = ?;"

	conn, err := s.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("update account %d: %w", id, err)
	}
	defer s.pool.Put(conn)

	if err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args}); err != nil {
		return fmt.Errorf("update account %d: %w", id, err)
	}
	if conn.Changes() == 0 {
		return ErrNotFound
	}
	return nil
}

// RefreshToken is a row from the refresh_tokens table: a single issued refresh
// token, enough to look it up by its jti and decide whether it is still usable.
type RefreshToken struct {
	JTI       string
	FamilyID  string
	AccountID int64
	IssuedAt  int64
	ExpiresAt int64
	Revoked   bool
}

// CreateRefreshToken persists a newly issued refresh token as un-revoked. jti
// must be unique (it is the JWT id); familyID groups tokens rotated from one
// login. issuedAt and expiresAt are unix seconds. It returns ErrConflict if the
// jti already exists.
//
// ctx bounds acquiring the connection and running the insert.
func (s *Store) CreateRefreshToken(ctx context.Context, jti, familyID string, accountID, issuedAt, expiresAt int64) error {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	defer s.pool.Put(conn)

	err = sqlitex.Execute(conn,
		"INSERT INTO refresh_tokens(jti, family_id, account_id, issued_at, expires_at, revoked) VALUES(?, ?, ?, ?, ?, 0);",
		&sqlitex.ExecOptions{Args: []any{jti, familyID, accountID, issuedAt, expiresAt}})
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.ResultConstraintUnique {
			return fmt.Errorf("create refresh token %q: %w", jti, ErrConflict)
		}
		return fmt.Errorf("create refresh token %q: %w", jti, err)
	}
	return nil
}

// RefreshTokenByJTI returns the refresh token with the given jti. It returns
// ErrNotFound if no such token exists. A returned token may be revoked or
// expired; callers decide whether that is acceptable.
//
// ctx bounds acquiring the connection and running the query.
func (s *Store) RefreshTokenByJTI(ctx context.Context, jti string) (RefreshToken, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return RefreshToken{}, fmt.Errorf("refresh token by jti: %w", err)
	}
	defer s.pool.Put(conn)

	token := RefreshToken{JTI: jti}
	found := false
	err = sqlitex.Execute(conn,
		"SELECT family_id, account_id, issued_at, expires_at, revoked FROM refresh_tokens WHERE jti = ?;",
		&sqlitex.ExecOptions{
			Args: []any{jti},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				token.FamilyID = stmt.ColumnText(0)
				token.AccountID = stmt.ColumnInt64(1)
				token.IssuedAt = stmt.ColumnInt64(2)
				token.ExpiresAt = stmt.ColumnInt64(3)
				token.Revoked = stmt.ColumnInt(4) != 0
				return nil
			},
		})
	if err != nil {
		return RefreshToken{}, fmt.Errorf("refresh token by jti %q: %w", jti, err)
	}
	if !found {
		return RefreshToken{}, ErrNotFound
	}
	return token, nil
}

// RevokeRefreshToken marks the token with the given jti revoked. It is
// idempotent: revoking an already-revoked or unknown jti is not an error, since
// revocation only ever needs to leave the token unusable.
//
// ctx bounds acquiring the connection and running the update.
func (s *Store) RevokeRefreshToken(ctx context.Context, jti string) error {
	return s.revoke(ctx, "jti = ?", jti, "revoke refresh token")
}

// RevokeFamily marks every token in familyID revoked, used for theft/reuse
// detection: presenting an already-rotated token kills the whole session
// family. It is idempotent.
//
// ctx bounds acquiring the connection and running the update.
func (s *Store) RevokeFamily(ctx context.Context, familyID string) error {
	return s.revoke(ctx, "family_id = ?", familyID, "revoke refresh family")
}

// RevokeAllForAccount marks every refresh token for accountID revoked, used for
// logout-everywhere. It is idempotent.
//
// ctx bounds acquiring the connection and running the update.
func (s *Store) RevokeAllForAccount(ctx context.Context, accountID int64) error {
	return s.revoke(ctx, "account_id = ?", accountID, "revoke account refresh tokens")
}

// revoke sets revoked = 1 on every refresh_tokens row matching whereCol against
// arg. It is the shared body of the three Revoke* methods; op labels errors.
func (s *Store) revoke(ctx context.Context, whereCol string, arg any, op string) error {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer s.pool.Put(conn)

	if err := sqlitex.Execute(conn,
		"UPDATE refresh_tokens SET revoked = 1 WHERE "+whereCol+";",
		&sqlitex.ExecOptions{Args: []any{arg}}); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// PurgeExpiredRefreshTokens deletes refresh-token rows whose expires_at is at or
// before cutoff (unix seconds), returning the number of rows removed. Callers
// pass the current time as cutoff.
//
// It prunes strictly by expiry, not by the revoked flag: an expired token can no
// longer authenticate (VerifyRefresh rejects it on expiry before this table is
// ever consulted), so dropping it changes nothing observable. A revoked but
// still-unexpired token is deliberately kept — presenting it is the reuse/theft
// signal that revokes the whole family — so it must survive until it expires.
//
// ctx bounds acquiring the connection and running the delete.
func (s *Store) PurgeExpiredRefreshTokens(ctx context.Context, cutoff int64) (int64, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("purge expired refresh tokens: %w", err)
	}
	defer s.pool.Put(conn)

	if err := sqlitex.Execute(conn,
		"DELETE FROM refresh_tokens WHERE expires_at <= ?;",
		&sqlitex.ExecOptions{Args: []any{cutoff}}); err != nil {
		return 0, fmt.Errorf("purge expired refresh tokens: %w", err)
	}
	return int64(conn.Changes()), nil
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

// ListAccounts returns all accounts ordered by id, without hashed secrets. An
// empty table yields an empty slice, not an error.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer s.pool.Put(conn)

	accounts := []Account{}
	err = sqlitex.Execute(conn, "SELECT id, email, is_admin, is_active FROM accounts ORDER BY id;", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			accounts = append(accounts, Account{
				ID:       stmt.ColumnInt64(0),
				Email:    stmt.ColumnText(1),
				IsAdmin:  stmt.ColumnInt(2) != 0,
				IsActive: stmt.ColumnInt(3) != 0,
			})
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return accounts, nil
}

// GameMembership projects one row of the game_account_role bridge joined to its
// game: a game the account participates in, together with the account's handle
// and game-master status in that game. Code is the game's code; IsActive is the
// game's own active flag (not the membership's).
type GameMembership struct {
	GameID   int64
	Code     string
	IsActive bool
	Handle   string
	IsGM     bool
}

// GamesForAccount returns the games the account is currently a member of, one
// GameMembership per active membership, ordered by game id. A membership that
// has been dropped (game_account_role.is_active = 0) is excluded; the game's own
// is_active flag is reported in IsActive rather than filtered on, so a member of
// an archived game still sees it. An account in no games yields an empty slice,
// not an error.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) GamesForAccount(ctx context.Context, accountID int64) ([]GameMembership, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("games for account: %w", err)
	}
	defer s.pool.Put(conn)

	memberships := []GameMembership{}
	err = sqlitex.Execute(conn,
		`SELECT g.id, g.code, g.is_active, r.handle, r.is_gm
			FROM game_account_role r
			JOIN games g ON g.id = r.game_id
			WHERE r.account_id = ? AND r.is_active = 1
			ORDER BY g.id;`,
		&sqlitex.ExecOptions{
			Args: []any{accountID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				memberships = append(memberships, GameMembership{
					GameID:   stmt.ColumnInt64(0),
					Code:     stmt.ColumnText(1),
					IsActive: stmt.ColumnInt(2) != 0,
					Handle:   stmt.ColumnText(3),
					IsGM:     stmt.ColumnInt(4) != 0,
				})
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("games for account %d: %w", accountID, err)
	}
	return memberships, nil
}

// Game is a row from the games table. Description is nil when the column is
// NULL (no description set).
type Game struct {
	ID          int64
	Code        string
	Name        string
	Status      string
	Description *string
	IsActive    bool
}

// CreateGame inserts a new game and returns it. code and name must already be
// validated by the caller (the games.code CHECK is a backstop, not the primary
// guard); a new game starts in the 'draft' status and active. description is
// stored as NULL when nil. It returns ErrConflict if the code is already taken.
//
// ctx bounds acquiring the connection and running the insert.
func (s *Store) CreateGame(ctx context.Context, code, name string, description *string) (Game, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Game{}, fmt.Errorf("create game: %w", err)
	}
	defer s.pool.Put(conn)

	err = sqlitex.Execute(conn,
		"INSERT INTO games(code, name, status, description, is_active) VALUES(?, ?, 'draft', ?, 1);",
		&sqlitex.ExecOptions{Args: []any{code, name, nullableText(description)}})
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.ResultConstraintUnique {
			return Game{}, fmt.Errorf("create game %q: %w", code, ErrConflict)
		}
		return Game{}, fmt.Errorf("create game %q: %w", code, err)
	}
	return Game{
		ID:          conn.LastInsertRowID(),
		Code:        code,
		Name:        name,
		Status:      "draft",
		Description: description,
		IsActive:    true,
	}, nil
}

// nullableText maps an optional string to a SQL argument: nil binds as NULL, a
// non-nil pointer binds its value.
func nullableText(s *string) any {
	if s == nil {
		return nil
	}
	return *s
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

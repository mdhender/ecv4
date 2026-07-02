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

// ErrMemberExists is returned by AddMember when the target account already has a
// membership row in the game (active or dropped). The caller should reactivate
// the existing membership rather than add a new one.
var ErrMemberExists = errors.New("account is already a member")

// ErrHandleTaken is returned by AddMember when the handle — supplied or the
// computed player_N default — is already in use by another member of the game.
var ErrHandleTaken = errors.New("handle already in use")

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
	op := fmt.Sprintf("update account %q", email)
	if upd.empty() {
		return fmt.Errorf("%s: no changes requested", op)
	}
	return s.updateAccountWhere(ctx, "email", email, op, upd)
}

// UpdateAccountByID applies upd to the account with the given id. It returns
// ErrNotFound if no account has that id, and an error if upd requests no
// changes. Only the fields set in upd are written. It mirrors
// UpdateAccountByEmail, selecting by id instead of email.
//
// ctx bounds acquiring the connection and running the update.
func (s *Store) UpdateAccountByID(ctx context.Context, id int64, upd AccountUpdate) error {
	op := fmt.Sprintf("update account %d", id)
	if upd.empty() {
		return fmt.Errorf("%s: no changes requested", op)
	}
	return s.updateAccountWhere(ctx, "id", id, op, upd)
}

// buildAccountUpdate turns upd into the parallel SET-clause fragments and their
// arguments for an accounts UPDATE. Only the fields set in upd are included, so
// the returned slices are non-empty exactly when upd is non-empty.
func buildAccountUpdate(upd AccountUpdate) (sets []string, args []any) {
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
	return sets, args
}

// updateAccountWhere applies upd to the single account matching whereCol against
// arg, returning ErrNotFound if no row matched; op labels errors. It is the
// shared body of UpdateAccountByEmail and UpdateAccountByID, which guarantee upd
// is non-empty before calling.
func (s *Store) updateAccountWhere(ctx context.Context, whereCol string, arg any, op string, upd AccountUpdate) error {
	sets, args := buildAccountUpdate(upd)
	args = append(args, arg)
	query := "UPDATE accounts SET " + strings.Join(sets, ", ") + " WHERE " + whereCol + " = ?;"

	conn, err := s.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer s.pool.Put(conn)

	if err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args}); err != nil {
		return fmt.Errorf("%s: %w", op, err)
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

// RevokeFamilyForAccount marks every token in familyID revoked, but only if the
// family belongs to accountID. It returns ErrNotFound when no row for that family
// is owned by the account (the family is unknown, or belongs to someone else), so
// callers can 404 without revealing another account's session. Revoking a family
// the account owns is idempotent, returning nil even if it was already revoked.
//
// ctx bounds acquiring the connection and running the update.
func (s *Store) RevokeFamilyForAccount(ctx context.Context, familyID string, accountID int64) error {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return fmt.Errorf("revoke family for account: %w", err)
	}
	defer s.pool.Put(conn)

	if err := sqlitex.Execute(conn,
		"UPDATE refresh_tokens SET revoked = 1 WHERE family_id = ? AND account_id = ?;",
		&sqlitex.ExecOptions{Args: []any{familyID, accountID}}); err != nil {
		return fmt.Errorf("revoke family %q for account %d: %w", familyID, accountID, err)
	}
	// SQLite counts every row matched by the WHERE clause as changed, even when
	// revoked was already 1, so zero changes means the account owns no such family.
	if conn.Changes() == 0 {
		return ErrNotFound
	}
	return nil
}

// Session is one active refresh-token family for an account: the family id plus
// the issue and expiry times of the family's current token. It carries no token
// material, only enough to recognize and revoke a session.
type Session struct {
	FamilyID  string
	IssuedAt  int64
	ExpiresAt int64
}

// SessionsForAccount returns the account's active sessions: one Session per
// refresh-token family that still has a token that is neither revoked nor expired
// as of now (unix seconds). Within a family the current token has the greatest
// issued_at/expires_at (rotation revokes the old and mints a newer one), so those
// maxima describe the live token. Sessions are ordered newest-first by issue time.
// An account with no live sessions yields an empty slice, not an error.
//
// ctx bounds acquiring the connection and running the query.
func (s *Store) SessionsForAccount(ctx context.Context, accountID, now int64) ([]Session, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("sessions for account: %w", err)
	}
	defer s.pool.Put(conn)

	sessions := []Session{}
	err = sqlitex.Execute(conn,
		`SELECT family_id, MAX(issued_at), MAX(expires_at)
			FROM refresh_tokens
			WHERE account_id = ? AND revoked = 0 AND expires_at > ?
			GROUP BY family_id
			ORDER BY MAX(issued_at) DESC, family_id;`,
		&sqlitex.ExecOptions{
			Args: []any{accountID, now},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				sessions = append(sessions, Session{
					FamilyID:  stmt.ColumnText(0),
					IssuedAt:  stmt.ColumnInt64(1),
					ExpiresAt: stmt.ColumnInt64(2),
				})
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("sessions for account %d: %w", accountID, err)
	}
	return sessions, nil
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

// GameUpdate describes a partial update to a game. A nil field is left unchanged;
// a non-nil field is written. The store applies exactly what it is given — the
// lifecycle/status and role rules are enforced by the handler before calling.
type GameUpdate struct {
	Status      *string
	Name        *string
	Description *string
	IsActive    *bool
}

// empty reports whether the update requests no changes.
func (u GameUpdate) empty() bool {
	return u.Status == nil && u.Name == nil && u.Description == nil && u.IsActive == nil
}

// buildGameUpdate turns upd into the SET-clause fragments and their arguments for
// a games UPDATE. Only the fields set in upd are included.
func buildGameUpdate(upd GameUpdate) (sets []string, args []any) {
	if upd.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *upd.Status)
	}
	if upd.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *upd.Name)
	}
	if upd.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *upd.Description)
	}
	if upd.IsActive != nil {
		sets = append(sets, "is_active = ?")
		args = append(args, boolToInt(*upd.IsActive))
	}
	return sets, args
}

// UpdateGame applies upd to the game and returns the updated Game. The current-row
// read and update run in one transaction. It returns ErrNotFound if no game has
// the id and an error if upd requests no changes. It enforces no lifecycle or role
// rules; the handler authorizes the change first.
//
// ctx bounds the whole operation.
func (s *Store) UpdateGame(ctx context.Context, id int64, upd GameUpdate) (game Game, err error) {
	if upd.empty() {
		return Game{}, fmt.Errorf("update game %d: no changes requested", id)
	}

	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Game{}, fmt.Errorf("update game: %w", err)
	}
	defer s.pool.Put(conn)

	endTx, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return Game{}, fmt.Errorf("update game: %w", err)
	}
	defer endTx(&err)

	current := Game{ID: id}
	found := false
	if e := sqlitex.Execute(conn,
		"SELECT code, name, status, description, is_active FROM games WHERE id = ?;",
		&sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				current.Code = stmt.ColumnText(0)
				current.Name = stmt.ColumnText(1)
				current.Status = stmt.ColumnText(2)
				if stmt.ColumnType(3) != sqlite.TypeNull {
					desc := stmt.ColumnText(3)
					current.Description = &desc
				}
				current.IsActive = stmt.ColumnInt(4) != 0
				return nil
			},
		}); e != nil {
		return Game{}, fmt.Errorf("update game %d: %w", id, e)
	}
	if !found {
		return Game{}, ErrNotFound
	}

	sets, args := buildGameUpdate(upd)
	args = append(args, id)
	if e := sqlitex.Execute(conn,
		"UPDATE games SET "+strings.Join(sets, ", ")+" WHERE id = ?;",
		&sqlitex.ExecOptions{Args: args}); e != nil {
		return Game{}, fmt.Errorf("update game %d: %w", id, e)
	}

	// Merge the applied fields onto the current row for the returned Game.
	if upd.Status != nil {
		current.Status = *upd.Status
	}
	if upd.Name != nil {
		current.Name = *upd.Name
	}
	if upd.Description != nil {
		desc := *upd.Description
		current.Description = &desc
	}
	if upd.IsActive != nil {
		current.IsActive = *upd.IsActive
	}
	return current, nil
}

// nullableText maps an optional string to a SQL argument: nil binds as NULL, a
// non-nil pointer binds its value.
func nullableText(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// scanGame reads a Game from the current row of stmt, whose selected columns must
// be, in order: id, code, name, status, description, is_active. A NULL description
// maps to a nil pointer.
func scanGame(stmt *sqlite.Stmt) Game {
	game := Game{
		ID:       stmt.ColumnInt64(0),
		Code:     stmt.ColumnText(1),
		Name:     stmt.ColumnText(2),
		Status:   stmt.ColumnText(3),
		IsActive: stmt.ColumnInt(5) != 0,
	}
	if stmt.ColumnType(4) != sqlite.TypeNull {
		desc := stmt.ColumnText(4)
		game.Description = &desc
	}
	return game
}

// ListGames returns the games visible to a caller under the game-management
// visibility rules, ordered by game id. A non-admin caller sees every game they
// were ever assigned to — an active OR dropped membership, unlike GamesForAccount,
// which requires an active membership — excluding games whose own is_active flag
// is 0 (an admin hard-hide). An admin caller sees every game, including hidden
// ones. When status is non-nil the result is further restricted to games in that
// lifecycle status. No visible games yields an empty slice, not an error.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) ListGames(ctx context.Context, accountID int64, isAdmin bool, status *string) ([]Game, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("list games: %w", err)
	}
	defer s.pool.Put(conn)

	var query strings.Builder
	var args []any
	if isAdmin {
		// Admin sees every game, including hard-hidden (is_active = 0) ones.
		query.WriteString(
			`SELECT g.id, g.code, g.name, g.status, g.description, g.is_active
				FROM games g
				WHERE 1 = 1`)
	} else {
		// The join to any membership row (active or dropped) restricts to games the
		// account was ever assigned to; UNIQUE(game_id, account_id) means at most one
		// such row per game, so no de-duplication is needed. g.is_active = 1 hides
		// admin-hidden games from non-admins.
		query.WriteString(
			`SELECT g.id, g.code, g.name, g.status, g.description, g.is_active
				FROM games g
				JOIN game_account_role r ON r.game_id = g.id
				WHERE r.account_id = ? AND g.is_active = 1`)
		args = append(args, accountID)
	}
	if status != nil {
		query.WriteString(" AND g.status = ?")
		args = append(args, *status)
	}
	query.WriteString(" ORDER BY g.id;")

	games := []Game{}
	err = sqlitex.Execute(conn, query.String(), &sqlitex.ExecOptions{
		Args: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			games = append(games, scanGame(stmt))
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list games: %w", err)
	}
	return games, nil
}

// GameByID returns the game with the given id if it is visible to the caller,
// applying the same visibility rules as ListGames to a single game. An admin sees
// any game. A non-admin sees a game only if they were ever assigned to it (active
// or dropped membership) and the game's own is_active flag is 1. A game that does
// not exist, or exists but is not visible to the caller, is reported as
// ErrNotFound, so the two are indistinguishable to a non-admin.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) GameByID(ctx context.Context, id, accountID int64, isAdmin bool) (Game, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Game{}, fmt.Errorf("game by id: %w", err)
	}
	defer s.pool.Put(conn)

	var query string
	var args []any
	if isAdmin {
		query = `SELECT g.id, g.code, g.name, g.status, g.description, g.is_active
			FROM games g
			WHERE g.id = ?;`
		args = []any{id}
	} else {
		query = `SELECT g.id, g.code, g.name, g.status, g.description, g.is_active
			FROM games g
			JOIN game_account_role r ON r.game_id = g.id
			WHERE g.id = ? AND r.account_id = ? AND g.is_active = 1;`
		args = []any{id, accountID}
	}

	var game Game
	found := false
	err = sqlitex.Execute(conn, query, &sqlitex.ExecOptions{
		Args: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			found = true
			game = scanGame(stmt)
			return nil
		},
	})
	if err != nil {
		return Game{}, fmt.Errorf("game by id %d: %w", id, err)
	}
	if !found {
		return Game{}, ErrNotFound
	}
	return game, nil
}

// Member is one row of a game's roster: an account's assignment to a game (a
// game_account_role row) with its handle and role. IsActive is the membership's
// own flag — false for a dropped member, who remains listed rather than deleted.
type Member struct {
	AccountID int64
	Handle    string
	IsGM      bool
	IsActive  bool
}

// MembersForGame returns every membership row for the game — active and dropped
// alike — ordered by the membership row id (assignment order), so a GM sees who
// has left as well as who remains. It neither filters on the game's own is_active
// flag nor checks visibility; callers gate access to the game first. A game with
// no members, or one that does not exist, yields an empty slice, not an error.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) MembersForGame(ctx context.Context, gameID int64) ([]Member, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("members for game: %w", err)
	}
	defer s.pool.Put(conn)

	members := []Member{}
	err = sqlitex.Execute(conn,
		`SELECT r.account_id, r.handle, r.is_gm, r.is_active
			FROM game_account_role r
			WHERE r.game_id = ?
			ORDER BY r.id;`,
		&sqlitex.ExecOptions{
			Args: []any{gameID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				members = append(members, Member{
					AccountID: stmt.ColumnInt64(0),
					Handle:    stmt.ColumnText(1),
					IsGM:      stmt.ColumnInt(2) != 0,
					IsActive:  stmt.ColumnInt(3) != 0,
				})
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("members for game %d: %w", gameID, err)
	}
	return members, nil
}

// MemberForGame returns the account's membership row in the game, active or
// dropped. It returns ErrNotFound when the account was never assigned to the
// game. Callers use it to decide authorization (an active GM) and to detect an
// existing membership before adding one.
//
// ctx bounds acquiring the connection and running the query; a cancelled ctx
// interrupts the read.
func (s *Store) MemberForGame(ctx context.Context, gameID, accountID int64) (Member, error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Member{}, fmt.Errorf("member for game: %w", err)
	}
	defer s.pool.Put(conn)

	member := Member{AccountID: accountID}
	found := false
	err = sqlitex.Execute(conn,
		`SELECT handle, is_gm, is_active
			FROM game_account_role
			WHERE game_id = ? AND account_id = ?;`,
		&sqlitex.ExecOptions{
			Args: []any{gameID, accountID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				member.Handle = stmt.ColumnText(0)
				member.IsGM = stmt.ColumnInt(1) != 0
				member.IsActive = stmt.ColumnInt(2) != 0
				return nil
			},
		})
	if err != nil {
		return Member{}, fmt.Errorf("member for game %d account %d: %w", gameID, accountID, err)
	}
	if !found {
		return Member{}, ErrNotFound
	}
	return member, nil
}

// AddMember assigns accountID to the game as a new active membership and returns
// the created Member. When handle is empty it defaults to player_N, where N is
// the game's current membership count (active or dropped) plus one. The count,
// default, uniqueness checks, and insert run in one transaction so the computed
// default is consistent.
//
// It returns:
//   - ErrNotFound if accountID does not name an existing account (so the caller
//     can reject a bad target with a 400 rather than surfacing a raw FK error);
//   - ErrMemberExists if the account is already assigned to the game (the caller
//     should reactivate the dropped membership instead of adding a new one);
//   - ErrHandleTaken if the handle (supplied or the computed default) is already
//     in use in the game — the default is never auto-bumped.
//
// It does not enforce role or status gates; the handler applies those before
// calling. ctx bounds the whole operation.
func (s *Store) AddMember(ctx context.Context, gameID, accountID int64, handle string, isGM bool) (member Member, err error) {
	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Member{}, fmt.Errorf("add member: %w", err)
	}
	defer s.pool.Put(conn)

	// Immediate transaction: the player_N default reads a count that the insert
	// then depends on, so take the write lock up front to keep the two consistent.
	endTx, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return Member{}, fmt.Errorf("add member: %w", err)
	}
	defer endTx(&err)

	// The target account must exist; an FK failure on insert would otherwise be an
	// opaque 500, so surface a missing account as ErrNotFound for a clean 400.
	if ok, e := existsInt(conn, "SELECT 1 FROM accounts WHERE id = ?;", accountID); e != nil {
		return Member{}, fmt.Errorf("add member: %w", e)
	} else if !ok {
		return Member{}, ErrNotFound
	}

	// An account is assigned to a game at most once (UNIQUE(game_id, account_id));
	// a second add is a conflict pointing at the reactivate path.
	if ok, e := existsInt(conn, "SELECT 1 FROM game_account_role WHERE game_id = ? AND account_id = ?;", gameID, accountID); e != nil {
		return Member{}, fmt.Errorf("add member: %w", e)
	} else if ok {
		return Member{}, ErrMemberExists
	}

	if handle == "" {
		var count int64
		if e := sqlitex.Execute(conn, "SELECT COUNT(*) FROM game_account_role WHERE game_id = ?;", &sqlitex.ExecOptions{
			Args: []any{gameID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt64(0)
				return nil
			},
		}); e != nil {
			return Member{}, fmt.Errorf("add member: %w", e)
		}
		handle = fmt.Sprintf("player_%d", count+1)
	}

	// Check the handle explicitly so a collision (supplied or the computed default)
	// is a clean ErrHandleTaken rather than a raw UNIQUE(game_id, handle) failure,
	// and so we never auto-bump the default.
	if ok, e := existsInt(conn, "SELECT 1 FROM game_account_role WHERE game_id = ? AND handle = ?;", gameID, handle); e != nil {
		return Member{}, fmt.Errorf("add member: %w", e)
	} else if ok {
		return Member{}, ErrHandleTaken
	}

	if e := sqlitex.Execute(conn,
		"INSERT INTO game_account_role(game_id, account_id, handle, is_gm, is_active) VALUES(?, ?, ?, ?, 1);",
		&sqlitex.ExecOptions{Args: []any{gameID, accountID, handle, boolToInt(isGM)}}); e != nil {
		// The pre-checks above cover the expected conflicts; a UNIQUE violation here
		// is an unexpected race, still reported as a generic conflict.
		if sqlite.ErrCode(e) == sqlite.ResultConstraintUnique {
			return Member{}, fmt.Errorf("add member: %w", ErrConflict)
		}
		return Member{}, fmt.Errorf("add member: %w", e)
	}

	return Member{AccountID: accountID, Handle: handle, IsGM: isGM, IsActive: true}, nil
}

// MemberUpdate describes a partial update to a game membership. A nil field is
// left unchanged; a non-nil field is written. The store applies exactly what it
// is given — the role/status rules (reactivate-only, promote-only, self-rename)
// are enforced by the handler before calling.
type MemberUpdate struct {
	IsActive *bool
	IsGM     *bool
	Handle   *string
}

// empty reports whether the update requests no changes.
func (u MemberUpdate) empty() bool {
	return u.IsActive == nil && u.IsGM == nil && u.Handle == nil
}

// buildMemberUpdate turns upd into the SET-clause fragments and their arguments
// for a game_account_role UPDATE. Only the fields set in upd are included.
func buildMemberUpdate(upd MemberUpdate) (sets []string, args []any) {
	if upd.IsActive != nil {
		sets = append(sets, "is_active = ?")
		args = append(args, boolToInt(*upd.IsActive))
	}
	if upd.IsGM != nil {
		sets = append(sets, "is_gm = ?")
		args = append(args, boolToInt(*upd.IsGM))
	}
	if upd.Handle != nil {
		sets = append(sets, "handle = ?")
		args = append(args, *upd.Handle)
	}
	return sets, args
}

// UpdateMember applies upd to the account's membership in the game and returns the
// updated Member. The current-row read, handle-uniqueness check, and update run in
// one transaction. It returns ErrNotFound if the account has no membership row in
// the game, an error if upd requests no changes, and ErrHandleTaken if a new
// handle is already used by another member of the game.
//
// It enforces no role or status rules; the handler authorizes the change first.
// ctx bounds the whole operation.
func (s *Store) UpdateMember(ctx context.Context, gameID, accountID int64, upd MemberUpdate) (member Member, err error) {
	if upd.empty() {
		return Member{}, fmt.Errorf("update member %d in game %d: no changes requested", accountID, gameID)
	}

	conn, err := s.pool.Get(ctx)
	if err != nil {
		return Member{}, fmt.Errorf("update member: %w", err)
	}
	defer s.pool.Put(conn)

	endTx, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return Member{}, fmt.Errorf("update member: %w", err)
	}
	defer endTx(&err)

	// Load the current row so we can return the merged result and reject an unknown
	// member with a clean ErrNotFound.
	current := Member{AccountID: accountID}
	found := false
	if e := sqlitex.Execute(conn,
		"SELECT handle, is_gm, is_active FROM game_account_role WHERE game_id = ? AND account_id = ?;",
		&sqlitex.ExecOptions{
			Args: []any{gameID, accountID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				current.Handle = stmt.ColumnText(0)
				current.IsGM = stmt.ColumnInt(1) != 0
				current.IsActive = stmt.ColumnInt(2) != 0
				return nil
			},
		}); e != nil {
		return Member{}, fmt.Errorf("update member %d in game %d: %w", accountID, gameID, e)
	}
	if !found {
		return Member{}, ErrNotFound
	}

	// A handle change must stay unique within the game; check it explicitly so a
	// collision is a clean ErrHandleTaken rather than a raw UNIQUE failure. Another
	// row (not this account) already holding the handle is the conflict.
	if upd.Handle != nil && *upd.Handle != current.Handle {
		if ok, e := existsInt(conn,
			"SELECT 1 FROM game_account_role WHERE game_id = ? AND handle = ? AND account_id <> ?;",
			gameID, *upd.Handle, accountID); e != nil {
			return Member{}, fmt.Errorf("update member %d in game %d: %w", accountID, gameID, e)
		} else if ok {
			return Member{}, ErrHandleTaken
		}
	}

	sets, args := buildMemberUpdate(upd)
	args = append(args, gameID, accountID)
	if e := sqlitex.Execute(conn,
		"UPDATE game_account_role SET "+strings.Join(sets, ", ")+" WHERE game_id = ? AND account_id = ?;",
		&sqlitex.ExecOptions{Args: args}); e != nil {
		if sqlite.ErrCode(e) == sqlite.ResultConstraintUnique {
			return Member{}, ErrHandleTaken
		}
		return Member{}, fmt.Errorf("update member %d in game %d: %w", accountID, gameID, e)
	}

	// Merge the applied fields onto the current row for the returned Member.
	if upd.IsActive != nil {
		current.IsActive = *upd.IsActive
	}
	if upd.IsGM != nil {
		current.IsGM = *upd.IsGM
	}
	if upd.Handle != nil {
		current.Handle = *upd.Handle
	}
	return current, nil
}

// existsInt reports whether query (which selects a constant when a row matches)
// returns at least one row for the given args.
func existsInt(conn *sqlite.Conn, query string, args ...any) (bool, error) {
	found := false
	err := sqlitex.Execute(conn, query, &sqlitex.ExecOptions{
		Args: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			found = true
			return nil
		},
	})
	return found, err
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

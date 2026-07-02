package database

import "zombiezen.com/go/sqlite/sqlitemigration"

// appID identifies this application in the database header. It is the
// ASCII bytes of "ecv4" (0x65 0x63 0x76 0x34) and must never change for
// the lifetime of the application.
const appID int32 = 0x65637634

// migrations is the ordered list of SQL scripts that define the schema.
// Append-only: once a migration ships it must never be edited or
// reordered, since sqlitemigration tracks how many have already run.
//
// Pattern note: a game code must match [A-Z][A-Z]+ (two or more uppercase ASCII
// letters — no lowercase, digits, or punctuation), and a handle must match
// [a-z][a-z0-9._-]+ (a lowercase letter followed by one or more of
// letter/digit/dot/underscore/hyphen, so a minimum length of two). SQLite's GLOB
// cannot express "every remaining character is in this set", so each rule is
// built from three parts:
//
//   - length(x) >= 2          enforces the trailing "+" (at least two chars).
//   - x GLOB '[A-Z]*'         enforces a (case-appropriate) letter first char.
//   - x NOT GLOB '*[^...]*'   rejects any character outside the allowed set.
//
// The original games CHECK (migration 0003) used the lowercase-with-punctuation
// handle shape; migration 0006 rebuilds the table to the strict uppercase-only
// code rule. handles still use lower(handle) for their GLOB checks so the
// by-convention GM handle "GM" is accepted at the storage layer; the service
// layer applies the stricter, case-sensitive rule to player handles.
var migrations = []string{
	// 0001 - establish the schema metadata table.
	`CREATE TABLE meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	) STRICT;`,

	// 0002 - accounts. Email is stored lower-cased by the service layer and
	// must be unique. The secret is bcrypt-hashed before it reaches the
	// database. Accounts are never deleted; is_active is toggled instead.
	`CREATE TABLE accounts (
		id            INTEGER PRIMARY KEY,
		email         TEXT    NOT NULL UNIQUE,
		is_admin      INTEGER NOT NULL CHECK (is_admin  IN (0, 1)),
		is_active     INTEGER NOT NULL CHECK (is_active IN (0, 1)),
		hashed_secret TEXT    NOT NULL
	) STRICT;`,

	// 0003 - games. Code must match the [a-z][a-z0-9._-]+ pattern and be
	// unique. Games are never deleted; is_active is toggled instead.
	`CREATE TABLE games (
		id        INTEGER PRIMARY KEY,
		code      TEXT    NOT NULL UNIQUE
			CHECK (length(code) >= 2
				AND code     GLOB '[a-z]*'
				AND code NOT GLOB '*[^a-z0-9._-]*'),
		is_active INTEGER NOT NULL CHECK (is_active IN (0, 1))
	) STRICT;`,

	// 0004 - game_account_role: the "players" bridge between accounts and
	// games. Its surrogate id is the player_id referenced by child tables.
	//
	//   - UNIQUE (game_id, account_id) means an account is assigned to a game
	//     at most once; combined with soft-delete, a dropped player is
	//     reactivated rather than re-inserted.
	//   - UNIQUE (game_id, handle) makes handles unique within a game.
	//   - is_gm marks GMs; the service layer enforces that an admin is never a
	//     member and that a GM is never reverted to player.
	//
	// Rows are never deleted; is_active is toggled to drop a member.
	`CREATE TABLE game_account_role (
		id         INTEGER PRIMARY KEY,
		game_id    INTEGER NOT NULL REFERENCES games(id),
		account_id INTEGER NOT NULL REFERENCES accounts(id),
		handle     TEXT    NOT NULL
			CHECK (length(handle) >= 2
				AND lower(handle)     GLOB '[a-z]*'
				AND lower(handle) NOT GLOB '*[^a-z0-9._-]*'),
		is_gm      INTEGER NOT NULL CHECK (is_gm     IN (0, 1)),
		is_active  INTEGER NOT NULL CHECK (is_active IN (0, 1)),
		UNIQUE (game_id, account_id),
		UNIQUE (game_id, handle)
	) STRICT;`,

	// 0005 - refresh_tokens: persisted refresh-token state so that logout and
	// theft/reuse detection mean something. Each row is one issued refresh
	// token, identified by its JWT id (jti). Tokens issued from a single login
	// share a family_id; rotating a token keeps the family and mints a new jti,
	// so presenting an already-revoked token lets us revoke the whole family.
	// revoked is a soft flag (rows are never deleted here); the JWT signature
	// remains the real secret, so rotating ECV4_JWT_SECRET still invalidates
	// every outstanding token at once, independent of this table.
	`CREATE TABLE refresh_tokens (
		id         INTEGER PRIMARY KEY,
		jti        TEXT    NOT NULL UNIQUE,
		family_id  TEXT    NOT NULL,
		account_id INTEGER NOT NULL REFERENCES accounts(id),
		issued_at  INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		revoked    INTEGER NOT NULL CHECK (revoked IN (0, 1))
	) STRICT;
	CREATE INDEX refresh_tokens_family_id  ON refresh_tokens(family_id);
	CREATE INDEX refresh_tokens_account_id ON refresh_tokens(account_id);`,

	// 0006 - tighten the games.code rule to the strict uppercase-letters-only
	// form [A-Z][A-Z]+. The original CHECK (migration 0003) allowed a lowercase
	// code with digits and punctuation; the intended rule is two or more
	// uppercase ASCII letters and nothing else. SQLite cannot ALTER an existing
	// CHECK, so the games table is rebuilt: a new table with the tightened CHECK
	// is created, surviving rows are copied into it with their codes upper-cased,
	// and the old table is dropped and the new one renamed into its place.
	//
	// Uppercasing a code that was already all-lowercase letters keeps it valid
	// (alpha -> ALPHA); the only rows that cannot be salvaged are dev codes that
	// carried digits or punctuation (alpha-1, a.b_c-2), which even after
	// upper-casing still fail the new CHECK. Those games are thrown away rather
	// than migrated — they only ever existed in the disposable dev databases —
	// along with the game_account_role rows that referenced them, so no dangling
	// foreign keys remain.
	//
	// game_account_role references games(id); dropping and rebuilding the parent
	// trips foreign-key enforcement mid-migration (DROP TABLE performs an implicit
	// delete of the parent rows). This migration is therefore registered with the
	// DisableForeignKeys option (see migrationOptions below), which turns foreign
	// keys off for the duration of its transaction and restores them afterward.
	// The DELETE below still hand-prunes the orphaned membership rows so that no
	// dangling reference survives once enforcement is restored.
	`CREATE TABLE games_new (
		id        INTEGER PRIMARY KEY,
		code      TEXT    NOT NULL UNIQUE
			CHECK (length(code) >= 2
				AND code     GLOB '[A-Z]*'
				AND code NOT GLOB '*[^A-Z]*'),
		is_active INTEGER NOT NULL CHECK (is_active IN (0, 1))
	) STRICT;

	INSERT INTO games_new (id, code, is_active)
		SELECT id, upper(code), is_active
		FROM games
		WHERE length(code) >= 2
			AND upper(code) NOT GLOB '*[^A-Z]*';

	DELETE FROM game_account_role
		WHERE game_id NOT IN (SELECT id FROM games_new);

	DROP TABLE games;

	ALTER TABLE games_new RENAME TO games;`,
}

// migrationOptions carries per-migration options, index-aligned with
// migrations. Migration 0006 (index 5) rebuilds the games table, which requires
// foreign keys to be disabled for its transaction; every other migration takes
// the zero value (nil), meaning no special handling.
var migrationOptions = []*sqlitemigration.MigrationOptions{
	5: {DisableForeignKeys: true},
}

// schema returns the migration schema applied to every database.
func schema() sqlitemigration.Schema {
	return sqlitemigration.Schema{
		AppID:            appID,
		Migrations:       migrations,
		MigrationOptions: migrationOptions,
	}
}

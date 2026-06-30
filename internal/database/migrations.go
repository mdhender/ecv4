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
// Pattern note: codes and handles must match [a-z][a-z0-9._-]+ (a lowercase
// letter followed by one or more of letter/digit/dot/underscore/hyphen, so a
// minimum length of two). SQLite's GLOB cannot express "every remaining
// character is in this set", so each rule is built from three parts:
//
//   - length(x) >= 2          enforces the trailing "+" (at least two chars).
//   - x GLOB '[a-z]*'         enforces a lowercase-letter first character.
//   - x NOT GLOB '*[^...]*'   rejects any character outside the allowed set.
//
// codes use the strict lowercase form. handles use lower(handle) for the
// GLOB checks so the by-convention GM handle "GM" is accepted at the storage
// layer; the service layer applies the stricter, case-sensitive rule to
// player handles.
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
}

// schema returns the migration schema applied to every database.
func schema() sqlitemigration.Schema {
	return sqlitemigration.Schema{
		AppID:      appID,
		Migrations: migrations,
	}
}

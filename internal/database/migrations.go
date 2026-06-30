package database

import "zombiezen.com/go/sqlite/sqlitemigration"

// appID identifies this application in the database header. It is the
// ASCII bytes of "ecv4" (0x65 0x63 0x76 0x34) and must never change for
// the lifetime of the application.
const appID int32 = 0x65637634

// migrations is the ordered list of SQL scripts that define the schema.
// Append-only: once a migration ships it must never be edited or
// reordered, since sqlitemigration tracks how many have already run.
var migrations = []string{
	// 0001 - establish the schema metadata table.
	`CREATE TABLE meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	) STRICT;`,
}

// schema returns the migration schema applied to every database.
func schema() sqlitemigration.Schema {
	return sqlitemigration.Schema{
		AppID:      appID,
		Migrations: migrations,
	}
}

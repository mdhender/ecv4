// Package cli builds and runs the game-server command tree. It holds the CLI
// business logic (create database, manage accounts, run the server) behind an
// App value so cmd/game-server stays a thin process shell and the command
// wiring is testable without shelling out to a built binary.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	ecv4 "github.com/mdhender/ecv4"
	"github.com/mdhender/ecv4/internal/config"
	"github.com/mdhender/ecv4/internal/database"
)

// App holds the process-level context and output sinks the CLI writes to. main
// constructs one with the resolved environment and os.Stdout/os.Stderr; tests
// construct one with buffers and a temp database to assert on behavior.
type App struct {
	// Env is the resolved ECV4_ENV (defaulting to "development"). It gates the
	// development admin seed and the production JWT-secret requirement.
	Env    string
	Stdout io.Writer
	Stderr io.Writer
}

// Run builds the command tree and parses/executes args, reading configuration
// from flags or ECV4_-prefixed environment variables. It prints help on
// -h/--help and prints the error plus usage on failure, returning the error so
// the caller can set the process exit code. It never calls os.Exit, so it is
// safe to call from tests.
func (a *App) Run(ctx context.Context, args []string) error {
	root := a.rootCommand()
	switch err := root.ParseAndRun(ctx, args, ff.WithEnvVarPrefix("ECV4")); {
	case err == nil:
		return nil
	case errors.Is(err, ff.ErrHelp):
		fmt.Fprintf(a.Stderr, "%s\n", ffhelp.Command(root))
		return nil
	default:
		fmt.Fprintf(a.Stderr, "%s\n", ffhelp.Command(root))
		fmt.Fprintf(a.Stderr, "error: %v\n", err)
		return err
	}
}

// rootCommand assembles the ff command tree. Flag pointers are captured by the
// Exec closures; the flagsets are captured too, so commands can use IsSet to
// distinguish "flag provided" from "default value" for tri-state flags.
func (a *App) rootCommand() *ff.Command {
	rootFlags := ff.NewFlagSet("game-server")
	addr := rootFlags.StringLong("addr", config.DefaultAddr, "HTTP listen address")
	dbDir := rootFlags.StringLong("db-dir", ".", "directory holding the "+database.FileName+" database (env ECV4_DB_DIR)")
	jwtSecret := rootFlags.StringLong("jwt-secret", "", "HMAC secret (>=32 bytes) for signing JWTs (env ECV4_JWT_SECRET); required when ECV4_ENV=production, otherwise a random ephemeral secret is generated")
	// One development switch, shared by every command (ff inherits root flags
	// into subcommands): when serving it enables development-only endpoints
	// (notably POST /admin/shutdown); with `database create` it seeds a known
	// admin. Declared once here so the two uses cannot collide.
	development := rootFlags.BoolLong("development", "enable development mode: the POST /admin/shutdown endpoint when serving, and the known-admin seed with 'database create' (env ECV4_DEVELOPMENT)")
	// Opt-in, off by default: serve the interactive OpenAPI docs at /docs. It is
	// deliberately independent of --development so docs can be exposed (or not)
	// on any deployment without also enabling the development-only shutdown route.
	allowDocs := rootFlags.BoolLong("allow-openapi-docs", "serve the interactive OpenAPI docs (Swagger UI) at /docs (env ECV4_ALLOW_OPENAPI_DOCS)")
	// How often the background reaper prunes expired refresh tokens while
	// serving. 0 disables the reaper (the on-demand POST /admin/refresh-tokens/purge
	// still works).
	reapInterval := rootFlags.DurationLong("session-reap-interval", 15*time.Minute, "interval between background purges of expired refresh tokens; 0 disables the reaper (env ECV4_SESSION_REAP_INTERVAL)")
	rootCmd := &ff.Command{
		Name:      "game-server",
		Usage:     "game-server [FLAGS] <SUBCOMMAND>",
		ShortHelp: "serve the experimental game API",
		Flags:     rootFlags,
		// With no subcommand, run the server. This keeps `make run`
		// (go run ./cmd/game-server) serving the skeleton as before.
		Exec: func(ctx context.Context, _ []string) error {
			return a.runServer(ctx, *addr, *dbDir, *jwtSecret, *development, *allowDocs, *reapInterval)
		},
	}

	versionCmd := &ff.Command{
		Name:      "version",
		Usage:     "game-server version",
		ShortHelp: "print the version and exit",
		Flags:     ff.NewFlagSet("version").SetParent(rootFlags),
		Exec: func(context.Context, []string) error {
			fmt.Fprintln(a.Stdout, ecv4.Version().Short())
			return nil
		},
	}
	rootCmd.Subcommands = append(rootCmd.Subcommands, versionCmd)

	databaseFlags := ff.NewFlagSet("database").SetParent(rootFlags)
	databaseCmd := &ff.Command{
		Name:      "database",
		Usage:     "game-server database <SUBCOMMAND>",
		ShortHelp: "manage the game database",
		Flags:     databaseFlags,
	}

	databaseCreateFlags := ff.NewFlagSet("create").SetParent(databaseFlags)
	databaseCreateCmd := &ff.Command{
		Name:      "create",
		Usage:     "game-server database create [--development] <PATH>",
		ShortHelp: "create a new database in an existing directory",
		LongHelp: "Create a new " + database.FileName + " database file inside PATH.\n" +
			"PATH must be an existing directory; it is never created.\n" +
			"The command fails if the database file already exists.\n" +
			"\n" +
			"A PATH of " + database.MemoryPath + " builds an ephemeral in-memory\n" +
			"database to verify the migrations apply; nothing is written to disk.\n" +
			"\n" +
			"With --development, and only when ECV4_ENV is development and both\n" +
			"ECV4_DEVELOPMENT_ADMIN_EMAIL and ECV4_DEVELOPMENT_ADMIN_SECRET are set,\n" +
			"seed a known active admin account so local smoke tests have a reliable\n" +
			"login. It is skipped (with a note) when those conditions are not met.",
		Flags: databaseCreateFlags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("create requires exactly one PATH argument")
			}
			path := args[0]
			if err := database.Create(ctx, path); err != nil {
				return err
			}
			if path == database.MemoryPath {
				fmt.Fprintln(a.Stdout, "verified migrations against an in-memory database (nothing persisted)")
			} else {
				fmt.Fprintf(a.Stdout, "created %s\n", filepath.Join(path, database.FileName))
			}
			if *development {
				if err := a.seedDevelopmentAdmin(ctx, path); err != nil {
					return err
				}
			}
			return nil
		},
	}
	databaseCmd.Subcommands = append(databaseCmd.Subcommands, databaseCreateCmd)

	// Account management is grouped under `database account <verb>` so future
	// verbs (update, list, ...) sit together. The database directory comes from
	// the shared --db-dir flag / ECV4_DB_DIR, defaulting to the current dir.
	databaseAccountFlags := ff.NewFlagSet("account").SetParent(databaseFlags)
	databaseAccountCmd := &ff.Command{
		Name:      "account",
		Usage:     "game-server database account <SUBCOMMAND>",
		ShortHelp: "manage accounts in the database",
		Flags:     databaseAccountFlags,
	}

	accountCreateFlags := ff.NewFlagSet("create").SetParent(databaseAccountFlags)
	accEmail := accountCreateFlags.StringLong("email", "", "account email, also the login username (required)")
	accSecret := accountCreateFlags.StringLong("secret", "", "account password, stored bcrypt-hashed (optional; a random passphrase is generated and printed if omitted)")
	accSeed := accountCreateFlags.Uint64Long("seed", 0, "for testing: seed the generated-passphrase RNG for a reproducible secret (only when --secret is omitted)")
	accIsInactive := accountCreateFlags.BoolLong("is-inactive", "create the account disabled, unable to log in")
	accIsAdmin := accountCreateFlags.BoolLong("is-admin", "grant the account admin privileges")
	databaseAccountCreateCmd := &ff.Command{
		Name:      "create",
		Usage:     "game-server database account create --email <email> [--secret <secret>] [--is-inactive] [--is-admin]",
		ShortHelp: "create a new account",
		LongHelp: "Create an account in the " + database.FileName + " database inside --db-dir.\n" +
			"The email is lower-cased and must be unique. The secret is bcrypt-hashed\n" +
			"before storage; if --secret is omitted a random passphrase is generated and\n" +
			"printed once (it is not recoverable). Pass --seed for a reproducible\n" +
			"passphrase in tests. Accounts are active by default; pass --is-inactive to\n" +
			"create one that cannot log in.",
		Flags: accountCreateFlags,
		Exec: func(ctx context.Context, _ []string) error {
			// A seed of 0 is valid, so distinguish "provided" from "default"
			// via IsSet rather than the zero value.
			var seed *uint64
			if isFlagSet(accountCreateFlags, "seed") {
				seed = accSeed
			}
			return a.createAccount(ctx, *dbDir, *accEmail, *accSecret, seed, !*accIsInactive, *accIsAdmin)
		},
	}
	databaseAccountCmd.Subcommands = append(databaseAccountCmd.Subcommands, databaseAccountCreateCmd)

	accountUpdateFlags := ff.NewFlagSet("update").SetParent(databaseAccountFlags)
	updEmail := accountUpdateFlags.StringLong("email", "", "email of the account to update (required)")
	updSecret := accountUpdateFlags.StringLong("secret", "", "set a new password (>= 8 characters)")
	updGenerate := accountUpdateFlags.BoolLong("generate-secret", "set a new randomly generated password and print it")
	updSeed := accountUpdateFlags.Uint64Long("seed", 0, "for testing: seed the generated-password RNG (only with --generate-secret)")
	updIsActive := accountUpdateFlags.BoolLong("is-active", "set active (--is-active) or disabled (--is-active=false); omit to leave unchanged")
	updIsAdmin := accountUpdateFlags.BoolLong("is-admin", "set admin (--is-admin) or not (--is-admin=false); omit to leave unchanged")
	databaseAccountUpdateCmd := &ff.Command{
		Name:      "update",
		Usage:     "game-server database account update --email <email> [--is-active[=false]] [--is-admin[=false]] [--secret <secret> | --generate-secret]",
		ShortHelp: "update an existing account",
		LongHelp: "Update the account with --email in the " + database.FileName + " database inside\n" +
			"--db-dir. --is-active and --is-admin are tri-state: omit to leave unchanged,\n" +
			"pass --is-active to enable, or --is-active=false to disable. Change the\n" +
			"password with --secret (>= 8 chars) or --generate-secret (prints a new random\n" +
			"passphrase once). At least one change is required.",
		Flags: accountUpdateFlags,
		Exec: func(ctx context.Context, _ []string) error {
			// Tri-state booleans: a nil pointer means "leave unchanged".
			var isActive, isAdmin *bool
			if isFlagSet(accountUpdateFlags, "is-active") {
				isActive = updIsActive
			}
			if isFlagSet(accountUpdateFlags, "is-admin") {
				isAdmin = updIsAdmin
			}
			var seed *uint64
			if isFlagSet(accountUpdateFlags, "seed") {
				seed = updSeed
			}
			return a.updateAccount(ctx, *dbDir, *updEmail, isActive, isAdmin, isFlagSet(accountUpdateFlags, "secret"), *updSecret, *updGenerate, seed)
		},
	}
	databaseAccountCmd.Subcommands = append(databaseAccountCmd.Subcommands, databaseAccountUpdateCmd)

	// reset-password is a discoverable alias for the password-only case of
	// `update`: it forwards to the same updateAccount path, leaving roles and
	// active state untouched. With neither flag it generates a fresh passphrase
	// and prints it once, which is the common "reset it to something new" intent.
	accountResetFlags := ff.NewFlagSet("reset-password").SetParent(databaseAccountFlags)
	rpEmail := accountResetFlags.StringLong("email", "", "email of the account whose password to reset (required)")
	rpSecret := accountResetFlags.StringLong("secret", "", "set this specific new password (>= 8 characters); omit to generate one")
	rpGenerate := accountResetFlags.BoolLong("generate-secret", "generate and print a new random password (the default when --secret is omitted)")
	rpSeed := accountResetFlags.Uint64Long("seed", 0, "for testing: seed the generated-password RNG (only when generating)")
	databaseAccountResetCmd := &ff.Command{
		Name:      "reset-password",
		Usage:     "game-server database account reset-password --email <email> [--secret <secret> | --generate-secret]",
		ShortHelp: "reset an account's password",
		LongHelp: "Reset the password of the account with --email in the " + database.FileName + "\n" +
			"database inside --db-dir. Roles and active state are left unchanged. Pass\n" +
			"--secret to set a specific password, or omit it (or pass --generate-secret) to\n" +
			"generate a new random passphrase that is printed once. This is a convenience\n" +
			"alias for `database account update --email ... --secret/--generate-secret`.",
		Flags: accountResetFlags,
		Exec: func(ctx context.Context, _ []string) error {
			secretProvided := isFlagSet(accountResetFlags, "secret")
			// Default to generating when no specific secret was given, so
			// `reset-password --email ...` always produces a usable new password.
			generate := *rpGenerate || !secretProvided
			var seed *uint64
			if isFlagSet(accountResetFlags, "seed") {
				seed = rpSeed
			}
			return a.updateAccount(ctx, *dbDir, *rpEmail, nil, nil, secretProvided, *rpSecret, generate, seed)
		},
	}
	databaseAccountCmd.Subcommands = append(databaseAccountCmd.Subcommands, databaseAccountResetCmd)

	databaseCmd.Subcommands = append(databaseCmd.Subcommands, databaseAccountCmd)
	rootCmd.Subcommands = append(rootCmd.Subcommands, databaseCmd)

	return rootCmd
}

// isFlagSet reports whether the named flag was explicitly provided (on the
// command line or via its environment variable), as opposed to holding its
// default value. It underpins the tri-state boolean and optional-seed flags.
func isFlagSet(flags *ff.FlagSet, name string) bool {
	f, ok := flags.GetFlag(name)
	return ok && f.IsSet()
}

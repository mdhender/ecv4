package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	ecv4 "github.com/mdhender/ecv4"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/config"
	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/dotenv"
	"github.com/mdhender/ecv4/internal/handlers"
	"github.com/mdhender/ecv4/internal/httputil"
	"github.com/mdhender/ecv4/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Load .env files before parsing flags so ff reads ECV4_* variables sourced
	// from them. ECV4_ENV selects which files load (see internal/dotenv) and is
	// read straight from the environment — not a flag — because it must be known
	// before any flag is parsed. It defaults to development.
	env := os.Getenv("ECV4_ENV")
	if env == "" {
		env = "development"
	}
	if err := dotenv.Load(env); err != nil {
		fmt.Fprintf(os.Stderr, "error: load %q environment: %v\n", env, err)
		os.Exit(1)
	}

	rootFlags := ff.NewFlagSet("game-server")
	addr := rootFlags.StringLong("addr", config.DefaultAddr, "HTTP listen address")
	dbDir := rootFlags.StringLong("db-dir", ".", "directory holding the "+database.FileName+" database (env ECV4_DB_DIR)")
	jwtSecret := rootFlags.StringLong("jwt-secret", "", "HMAC secret (>=32 bytes) for signing JWTs (env ECV4_JWT_SECRET); if empty, a random ephemeral secret is generated")
	// One development switch, shared by every command (ff inherits root flags
	// into subcommands): when serving it enables development-only endpoints
	// (notably POST /admin/shutdown); with `database create` it seeds a known
	// admin. Declared once here so the two uses cannot collide.
	development := rootFlags.BoolLong("development", "enable development mode: the POST /admin/shutdown endpoint when serving, and the known-admin seed with 'database create' (env ECV4_DEVELOPMENT)")
	rootCmd := &ff.Command{
		Name:      "game-server",
		Usage:     "game-server [FLAGS] <SUBCOMMAND>",
		ShortHelp: "serve the experimental game API",
		Flags:     rootFlags,
		// With no subcommand, run the server. This keeps `make run`
		// (go run ./cmd/game-server) serving the skeleton as before.
		Exec: func(ctx context.Context, _ []string) error {
			return runServer(ctx, *addr, *dbDir, *jwtSecret, *development)
		},
	}

	versionCmd := &ff.Command{
		Name:      "version",
		Usage:     "game-server version",
		ShortHelp: "print the version and exit",
		Flags:     ff.NewFlagSet("version").SetParent(rootFlags),
		Exec: func(context.Context, []string) error {
			fmt.Println(ecv4.Version().Short())
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
				fmt.Println("verified migrations against an in-memory database (nothing persisted)")
			} else {
				fmt.Printf("created %s\n", filepath.Join(path, database.FileName))
			}
			if *development {
				if err := seedDevelopmentAdmin(ctx, env, path); err != nil {
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
			if f, ok := accountCreateFlags.GetFlag("seed"); ok && f.IsSet() {
				seed = accSeed
			}
			return createAccount(ctx, *dbDir, *accEmail, *accSecret, seed, !*accIsInactive, *accIsAdmin)
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
			isSet := func(name string) bool {
				f, ok := accountUpdateFlags.GetFlag(name)
				return ok && f.IsSet()
			}
			// Tri-state booleans: a nil pointer means "leave unchanged".
			var isActive, isAdmin *bool
			if isSet("is-active") {
				isActive = updIsActive
			}
			if isSet("is-admin") {
				isAdmin = updIsAdmin
			}
			var seed *uint64
			if isSet("seed") {
				seed = updSeed
			}
			return updateAccount(ctx, *dbDir, *updEmail, isActive, isAdmin, isSet("secret"), *updSecret, *updGenerate, seed)
		},
	}
	databaseAccountCmd.Subcommands = append(databaseAccountCmd.Subcommands, databaseAccountUpdateCmd)

	databaseCmd.Subcommands = append(databaseCmd.Subcommands, databaseAccountCmd)

	rootCmd.Subcommands = append(rootCmd.Subcommands, databaseCmd)

	switch err := rootCmd.ParseAndRun(ctx, os.Args[1:], ff.WithEnvVarPrefix("ECV4")); {
	case err == nil:
		// success
	case errors.Is(err, ff.ErrHelp):
		fmt.Fprintf(os.Stderr, "%s\n", ffhelp.Command(rootCmd))
	default:
		fmt.Fprintf(os.Stderr, "%s\n", ffhelp.Command(rootCmd))
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// runServer opens the database, starts the skeleton HTTP server, and blocks
// until ctx is cancelled (SIGINT/SIGTERM) or the listener fails. The database
// pool is opened before the listener and closed only after the server has
// drained, so in-flight requests keep a usable pool through shutdown.
func runServer(ctx context.Context, addr, dbDir, jwtSecret string, development bool) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	secret, err := resolveJWTSecret(jwtSecret, logger)
	if err != nil {
		return err
	}
	// 15-minute access tokens, 24-hour refresh tokens, rotated and revoked
	// through /auth/refresh and /auth/logout.
	tokens := auth.NewTokenService(secret, 15*time.Minute, 24*time.Hour)

	// Open (and migrate) the database before binding the listener; a bad
	// database should fail startup, not surface on the first request. The
	// deferred close runs after the shutdown logic below returns, so it
	// happens once the server has stopped accepting and draining requests.
	dbPath := filepath.Join(dbDir, database.FileName)
	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := closeDB(); err != nil {
			logger.Error("closing database", "err", err)
		} else {
			logger.Info("database closed")
		}
	}()
	logger.Info("database ready", "path", dbPath)

	// srvCtx cancels either on the signal that cancels ctx (SIGINT/SIGTERM) or
	// when the development shutdown route triggers it, so both drive the same
	// graceful-drain path below. The deferred cancel prevents a context leak on
	// the listener-error return.
	srvCtx, triggerShutdown := context.WithCancel(ctx)
	defer triggerShutdown()

	// The store wraps the pool with typed query methods; the generated API
	// handlers reach the database only through it. The token service both
	// issues tokens (for /auth/login) and verifies them (for secured routes).
	// In development, wire the admin shutdown route to triggerShutdown; without
	// --development the route is not enabled and responds 404.
	var serverOpts []handlers.Option
	if development {
		serverOpts = append(serverOpts, handlers.WithShutdown(triggerShutdown))
		logger.Info("development mode: POST /admin/shutdown enabled")
	}
	apiServer := handlers.NewServer(store.New(pool), tokens, serverOpts...)

	// Serve the raw spec alongside the generated API routes, then let
	// oapi-codegen register the API operations (including /healthz and
	// /version) on the same mux.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.yaml", httputil.OpenAPIHandler("api/openapi.yaml"))
	apiHandler := handlers.NewHTTPHandler(apiServer, mux, tokens)

	srv := &http.Server{
		Addr:              addr,
		Handler:           httputil.RequestLogger(logger, apiHandler),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting game server", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-srvCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown failed: %w", err)
		}
		logger.Info("server stopped")
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}
}

// seedDevelopmentAdmin optionally seeds a known, active admin account into a
// freshly created database so local smoke tests have a reliable login. It is a
// deliberate no-op — with an explanatory note — unless env is development and
// both ECV4_DEVELOPMENT_ADMIN_EMAIL and ECV4_DEVELOPMENT_ADMIN_SECRET are set.
// The special in-memory database is never seeded because it is not persisted.
func seedDevelopmentAdmin(ctx context.Context, env, path string) error {
	if path == database.MemoryPath {
		fmt.Println("skipping --development admin seed: the in-memory database is not persisted")
		return nil
	}
	if env != "development" {
		fmt.Printf("skipping --development admin seed: ECV4_ENV is %q, not development\n", env)
		return nil
	}
	email := os.Getenv("ECV4_DEVELOPMENT_ADMIN_EMAIL")
	secret := os.Getenv("ECV4_DEVELOPMENT_ADMIN_SECRET")
	if email == "" || secret == "" {
		fmt.Println("skipping --development admin seed: set ECV4_DEVELOPMENT_ADMIN_EMAIL and ECV4_DEVELOPMENT_ADMIN_SECRET to seed one")
		return nil
	}

	fmt.Println("seeding development admin account...")
	return createAccount(ctx, path, email, secret, nil, true, true)
}

// createAccount opens the database in dbDir and inserts a new account. The
// email is normalized and the secret is bcrypt-hashed before it is stored.
func createAccount(ctx context.Context, dbDir, email, secret string, seed *uint64, isActive, isAdmin bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("account create requires --email")
	}

	generated := false
	if secret == "" {
		var err error
		secret, err = auth.GenerateSecret(seed)
		if err != nil {
			return err
		}
		generated = true
	}

	hashedSecret, err := auth.HashSecret(secret)
	if err != nil {
		return err
	}

	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return err
	}
	defer closeDB()

	id, err := store.New(pool).CreateAccount(ctx, email, isAdmin, isActive, hashedSecret)
	if err != nil {
		return err
	}

	fmt.Printf("created account %d %s (is_active=%t, is_admin=%t)\n", id, email, isActive, isAdmin)
	if generated {
		fmt.Printf("generated secret: %s\n", secret)
		fmt.Println("WARNING: only the hash is stored; this secret is shown once and cannot be recovered. Save it now.")
	}
	if !isActive {
		fmt.Println("note: account is inactive and cannot log in (created with --is-inactive)")
	}
	return nil
}

// updateAccount opens the database in dbDir and applies a partial update to the
// account selected by email. isActive and isAdmin are nil to leave unchanged.
// The password changes when secretProvided is set (to secret) or generate is
// set (to a fresh passphrase, printed once); at most one may be requested. At
// least one change overall is required.
func updateAccount(ctx context.Context, dbDir, email string, isActive, isAdmin *bool, secretProvided bool, secret string, generate bool, seed *uint64) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("account update requires --email")
	}
	if secretProvided && generate {
		return fmt.Errorf("use either --secret or --generate-secret, not both")
	}

	var hashedSecret *string
	var generatedSecret string
	switch {
	case secretProvided:
		hash, err := auth.HashSecret(secret)
		if err != nil {
			return err
		}
		hashedSecret = &hash
	case generate:
		gen, err := auth.GenerateSecret(seed)
		if err != nil {
			return err
		}
		hash, err := auth.HashSecret(gen)
		if err != nil {
			return err
		}
		hashedSecret = &hash
		generatedSecret = gen
	}

	upd := store.AccountUpdate{IsAdmin: isAdmin, IsActive: isActive, HashedSecret: hashedSecret}
	if upd.IsAdmin == nil && upd.IsActive == nil && upd.HashedSecret == nil {
		return fmt.Errorf("nothing to update: set --is-active, --is-admin, --secret, or --generate-secret")
	}

	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return err
	}
	defer closeDB()

	if err := store.New(pool).UpdateAccountByEmail(ctx, email, upd); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no account with email %q", email)
		}
		return err
	}

	fmt.Printf("updated account %s\n", email)
	if isActive != nil {
		fmt.Printf("  is_active=%t\n", *isActive)
	}
	if isAdmin != nil {
		fmt.Printf("  is_admin=%t\n", *isAdmin)
	}
	if generatedSecret != "" {
		fmt.Printf("  generated secret: %s\n", generatedSecret)
		fmt.Println("  WARNING: only the hash is stored; this secret is shown once and cannot be recovered. Save it now.")
	} else if hashedSecret != nil {
		fmt.Println("  secret updated")
	}
	return nil
}

// resolveJWTSecret returns the HMAC signing key. A configured secret must be at
// least 32 bytes (256 bits) to match HS256. When none is configured it
// generates a random ephemeral secret and warns: this keeps `make run` working
// in development, at the cost of invalidating all tokens on restart. Production
// deployments must set ECV4_JWT_SECRET.
func resolveJWTSecret(configured string, logger *slog.Logger) ([]byte, error) {
	if configured != "" {
		if len(configured) < 32 {
			return nil, fmt.Errorf("jwt secret must be at least 32 bytes, got %d", len(configured))
		}
		return []byte(configured), nil
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate ephemeral jwt secret: %w", err)
	}
	logger.Warn("no jwt secret configured; generated a random ephemeral secret",
		"consequence", "all tokens become invalid on restart",
		"fix", "set ECV4_JWT_SECRET (>=32 bytes) for a stable signing key")
	return secret, nil
}

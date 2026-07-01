package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	"golang.org/x/crypto/bcrypt"

	ecv4 "github.com/mdhender/ecv4"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/config"
	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/dotenv"
	"github.com/mdhender/ecv4/internal/handlers"
	"github.com/mdhender/ecv4/internal/httputil"
	"github.com/mdhender/ecv4/internal/phrases"
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
	rootCmd := &ff.Command{
		Name:      "game-server",
		Usage:     "game-server [FLAGS] <SUBCOMMAND>",
		ShortHelp: "serve the experimental game API",
		Flags:     rootFlags,
		// With no subcommand, run the server. This keeps `make run`
		// (go run ./cmd/game-server) serving the skeleton as before.
		Exec: func(ctx context.Context, _ []string) error {
			return runServer(ctx, *addr, *dbDir, *jwtSecret)
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

	databaseCreateCmd := &ff.Command{
		Name:      "create",
		Usage:     "game-server database create <PATH>",
		ShortHelp: "create a new database in an existing directory",
		LongHelp: "Create a new " + database.FileName + " database file inside PATH.\n" +
			"PATH must be an existing directory; it is never created.\n" +
			"The command fails if the database file already exists.\n" +
			"\n" +
			"A PATH of " + database.MemoryPath + " builds an ephemeral in-memory\n" +
			"database to verify the migrations apply; nothing is written to disk.",
		Flags: ff.NewFlagSet("create").SetParent(databaseFlags),
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
func runServer(ctx context.Context, addr, dbDir, jwtSecret string) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	secret, err := resolveJWTSecret(jwtSecret, logger)
	if err != nil {
		return err
	}
	// 15-minute access tokens, 24-hour refresh tokens. The refresh flow
	// (/auth/refresh, /auth/logout) is not implemented yet.
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

	// The store wraps the pool with typed query methods; the generated API
	// handlers reach the database only through it. The token service both
	// issues tokens (for /auth/login) and verifies them (for secured routes).
	apiServer := handlers.NewServer(store.New(pool), tokens)

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
	case <-ctx.Done():
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
		secret, err = generateSecret(seed)
		if err != nil {
			return err
		}
		generated = true
	}

	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash secret: %w", err)
	}

	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return err
	}
	defer closeDB()

	id, err := store.New(pool).CreateAccount(ctx, email, isAdmin, isActive, string(hashedSecret))
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

// generateSecret returns a random six-word passphrase for a new account (the
// EFF short list carries roughly 10.3 bits per word, so ~62 bits total).
//
// When seed is nil the two PCG seeds come from crypto/rand, so the result is
// unpredictable. When seed is non-nil the PCG is seeded with
// (*seed, splitMix64(*seed)), giving a reproducible "known random" passphrase
// for tests; splitMix64 derives an independent second seed so the PCG's two
// 64-bit lanes are not identical.
func generateSecret(seed *uint64) (string, error) {
	var s1, s2 uint64
	if seed != nil {
		s1, s2 = *seed, splitMix64(*seed)
	} else {
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("generate secret: %w", err)
		}
		s1 = binary.LittleEndian.Uint64(buf[0:8])
		s2 = binary.LittleEndian.Uint64(buf[8:16])
	}
	r := mrand.New(mrand.NewPCG(s1, s2))
	return phrases.Generate(r, 6), nil
}

// splitMix64 is the canonical SplitMix64 step: it advances state x by the
// golden-ratio increment and applies the finalizing avalanche mix. It is used
// to derive a second PCG seed from a single --seed value.
func splitMix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
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

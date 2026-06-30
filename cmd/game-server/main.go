package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"

	ecv4 "github.com/mdhender/ecv4"
	"github.com/mdhender/ecv4/internal/config"
	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/handlers"
	"github.com/mdhender/ecv4/internal/httputil"
	"github.com/mdhender/ecv4/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rootFlags := ff.NewFlagSet("game-server")
	addr := rootFlags.StringLong("addr", config.DefaultAddr, "HTTP listen address")
	dbDir := rootFlags.StringLong("db-dir", ".", "directory holding the "+database.FileName+" database (env ECV4_DB_DIR)")
	rootCmd := &ff.Command{
		Name:      "game-server",
		Usage:     "game-server [FLAGS] <SUBCOMMAND>",
		ShortHelp: "serve the experimental game API",
		Flags:     rootFlags,
		// With no subcommand, run the server. This keeps `make run`
		// (go run ./cmd/game-server) serving the skeleton as before.
		Exec: func(ctx context.Context, _ []string) error {
			return runServer(ctx, *addr, *dbDir)
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
func runServer(ctx context.Context, addr, dbDir string) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	// handlers reach the database only through it.
	apiServer := handlers.NewServer(store.New(pool))

	// Serve the raw spec alongside the generated API routes, then let
	// oapi-codegen register the API operations (including /healthz and
	// /version) on the same mux.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.yaml", httputil.OpenAPIHandler("api/openapi.yaml"))
	apiHandler := handlers.NewHTTPHandler(apiServer, mux)

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

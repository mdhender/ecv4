package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/handlers"
	"github.com/mdhender/ecv4/internal/httputil"
	"github.com/mdhender/ecv4/internal/store"
)

// runServer opens the database, starts the skeleton HTTP server, and blocks
// until ctx is cancelled (SIGINT/SIGTERM) or the listener fails. The database
// pool is opened before the listener and closed only after the server has
// drained, so in-flight requests keep a usable pool through shutdown.
//
// reapInterval sets how often the background refresh-token reaper runs; 0
// disables it (the on-demand purge endpoint still works).
func (a *App) runServer(ctx context.Context, addr, dbDir, jwtSecret string, development, allowDocs bool, reapInterval time.Duration) error {
	logger := slog.New(slog.NewTextHandler(a.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	secret, err := resolveJWTSecret(a.Env, jwtSecret, logger)
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
	st := store.New(pool)
	apiServer := handlers.NewServer(st, tokens, serverOpts...)

	// Reap expired refresh tokens in the background so the table does not grow
	// without bound (issue #5). It shares srvCtx, so graceful shutdown cancels
	// it; the WaitGroup below makes the drain wait for an in-flight sweep to
	// finish before the deferred pool close runs, so the reaper never touches a
	// closed pool. It calls the store directly, bypassing the API. A zero
	// interval disables it (runRefreshTokenReaper returns immediately).
	if reapInterval > 0 {
		logger.Info("refresh-token reaper enabled", "interval", reapInterval)
	} else {
		logger.Info("refresh-token reaper disabled", "reason", "session-reap-interval is 0")
	}
	var reaper sync.WaitGroup
	reaper.Add(1)
	go func() {
		defer reaper.Done()
		runRefreshTokenReaper(srvCtx, st, reapInterval, tokens.Now, logger)
	}()

	// Serve the raw spec alongside the generated API routes, then let
	// oapi-codegen register the API operations (including /healthz and
	// /version) on the same mux.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.yaml", httputil.OpenAPIHandler("api/openapi.yaml"))
	// Interactive OpenAPI docs, off unless the operator opts in. Like
	// /openapi.yaml these are meta routes registered on the mux directly, not
	// part of the API contract. The subtree pattern serves the page and its
	// embedded assets; /docs (no slash) redirects to it.
	if allowDocs {
		mux.Handle("GET /docs/", httputil.DocsHandler("/docs/"))
		mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/docs/", http.StatusMovedPermanently)
		})
		logger.Info("OpenAPI docs enabled", "url", "/docs")
	}
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
		// srvCtx is already cancelled here, so the reaper is stopping; wait for
		// it before returning so the deferred pool close does not race a sweep.
		err := srv.Shutdown(shutdownCtx)
		reaper.Wait()
		if err != nil {
			return fmt.Errorf("server shutdown failed: %w", err)
		}
		logger.Info("server stopped")
		return nil
	case err := <-errCh:
		// The listener failed without a shutdown signal; cancel srvCtx to stop
		// the reaper (the deferred triggerShutdown would too, but that runs
		// after the pool close) and wait for it before returning.
		triggerShutdown()
		reaper.Wait()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}
}

// resolveJWTSecret returns the HMAC signing key. A configured secret must be at
// least 32 bytes (256 bits) to match HS256. When none is configured, behavior
// depends on env: in production it is a fatal error (an ephemeral secret would
// silently invalidate every issued token on each restart); in any other
// environment it generates a random ephemeral secret and warns, which keeps
// `make run` working in development.
func resolveJWTSecret(env, configured string, logger *slog.Logger) ([]byte, error) {
	if configured != "" {
		if len(configured) < 32 {
			return nil, fmt.Errorf("jwt secret must be at least 32 bytes, got %d", len(configured))
		}
		return []byte(configured), nil
	}

	if env == "production" {
		return nil, fmt.Errorf("no jwt secret configured: ECV4_JWT_SECRET (>=32 bytes) is required when ECV4_ENV=production; an ephemeral secret would invalidate all tokens on restart")
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

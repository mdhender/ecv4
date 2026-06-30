package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdhender/ecv4/internal/config"
	"github.com/mdhender/ecv4/internal/httputil"
)

func main() {
	var addr string
	flag.StringVar(&addr, "addr", config.DefaultAddr, "HTTP listen address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httputil.HealthHandler("0.1.0"))
	mux.HandleFunc("GET /openapi.yaml", httputil.OpenAPIHandler("api/openapi.yaml"))

	// After `make generate`, replace this small skeleton mux with the generated
	// oapi-codegen handler wiring. See internal/handlers/server.go.stub.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           httputil.RequestLogger(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting game server", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown failed", "error", err)
			os.Exit(1)
		}
		logger.Info("server stopped")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	}
}

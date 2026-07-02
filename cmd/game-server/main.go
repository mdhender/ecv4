package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdhender/ecv4/internal/cli"
	"github.com/mdhender/ecv4/internal/dotenv"
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

	// All command construction, parsing, and business logic live in internal/cli
	// so this stays a thin process shell. Run prints its own help/errors; main
	// only maps a returned error to a non-zero exit code.
	app := &cli.App{Env: env, Stdout: os.Stdout, Stderr: os.Stderr}
	if err := app.Run(ctx, os.Args[1:]); err != nil {
		os.Exit(1)
	}
}

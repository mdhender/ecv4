package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

// runCLI drives the full parse→exec path exactly as main does, capturing the
// output streams so tests can assert on the wiring (flag → argument mapping,
// env-var binding, exit-worthy errors) rather than only the business logic.
func runCLI(t *testing.T, env string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	app := &App{Env: env, Stdout: &out, Stderr: &errBuf}
	err = app.Run(context.Background(), args)
	return out.String(), errBuf.String(), err
}

func TestRunVersion(t *testing.T) {
	stdout, _, err := runCLI(t, "development", "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected version output on stdout")
	}
}

func TestRunDatabaseCreate(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := runCLI(t, "development", "database", "create", dir)
	if err != nil {
		t.Fatalf("database create: %v", err)
	}
	if !strings.Contains(stdout, "created") {
		t.Fatalf("stdout = %q, want it to mention the created database", stdout)
	}
	// The database is usable afterwards.
	openStore(t, dir)
}

func TestRunDatabaseCreateInMemory(t *testing.T) {
	stdout, _, err := runCLI(t, "development", "database", "create", database.MemoryPath)
	if err != nil {
		t.Fatalf("database create :memory:: %v", err)
	}
	if !strings.Contains(stdout, "verified migrations") {
		t.Fatalf("stdout = %q, want the in-memory verification note", stdout)
	}
}

// TestRunAccountCreateWiring checks that create flags map to the right
// arguments: --is-admin sets admin, and omitting --is-inactive leaves the
// account active (the flag is inverted on the way through).
func TestRunAccountCreateWiring(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	_, _, err := runCLI(t, "development",
		"--db-dir", dir,
		"database", "account", "create",
		"--email", "wired@example.com", "--secret", "supersecret1", "--is-admin")
	if err != nil {
		t.Fatalf("account create: %v", err)
	}

	acct, hash, err := openStore(t, dir).Credentials(ctx, "wired@example.com")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if !acct.IsAdmin || !acct.IsActive {
		t.Fatalf("got is_admin=%t is_active=%t, want both true", acct.IsAdmin, acct.IsActive)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("supersecret1")) != nil {
		t.Fatal("stored hash does not verify against the flag-supplied secret")
	}
}

func TestRunAccountCreateInactive(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	if _, _, err := runCLI(t, "development",
		"--db-dir", dir,
		"database", "account", "create",
		"--email", "off@example.com", "--secret", "supersecret1", "--is-inactive"); err != nil {
		t.Fatalf("account create --is-inactive: %v", err)
	}

	acct, _, err := openStore(t, dir).Credentials(ctx, "off@example.com")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if acct.IsActive {
		t.Fatal("expected --is-inactive to create a disabled account")
	}
}

// TestRunAccountUpdateTriState is the wiring test the earlier unit tests could
// not reach: it drives the actual flag parser so the IsSet-based tri-state
// mapping runs. --is-admin=false must disable admin, and an update with no
// change flags must be rejected before touching the database.
func TestRunAccountUpdateTriState(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	if _, _, err := runCLI(t, "development",
		"--db-dir", dir,
		"database", "account", "create",
		"--email", "tri@example.com", "--secret", "supersecret1", "--is-admin"); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// Explicit --is-admin=false must set the tri-state pointer to false.
	if _, _, err := runCLI(t, "development",
		"--db-dir", dir,
		"database", "account", "update",
		"--email", "tri@example.com", "--is-admin=false"); err != nil {
		t.Fatalf("account update --is-admin=false: %v", err)
	}
	acct, _, err := openStore(t, dir).Credentials(ctx, "tri@example.com")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if acct.IsAdmin {
		t.Fatal("expected --is-admin=false to clear admin")
	}

	// No change flags at all: the nil tri-state pointers mean "nothing to update".
	_, stderr, err := runCLI(t, "development",
		"--db-dir", dir,
		"database", "account", "update", "--email", "tri@example.com")
	if err == nil {
		t.Fatal("expected an error when no update flags are set")
	}
	if !strings.Contains(stderr, "nothing to update") {
		t.Fatalf("stderr = %q, want a nothing-to-update message", stderr)
	}
}

// TestRunDBDirFromEnv verifies the ECV4_ env-var prefix is honored: --db-dir
// left off the command line is read from ECV4_DB_DIR.
func TestRunDBDirFromEnv(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	t.Setenv("ECV4_DB_DIR", dir)

	if _, _, err := runCLI(t, "development",
		"database", "account", "create",
		"--email", "env@example.com", "--secret", "supersecret1"); err != nil {
		t.Fatalf("account create with ECV4_DB_DIR: %v", err)
	}
	if _, _, err := openStore(t, dir).Credentials(ctx, "env@example.com"); err != nil {
		t.Fatalf("account should have been created in the env-configured dir: %v", err)
	}
}

// TestRunDevelopmentCreateSeedsAdmin checks that --development on `database
// create` triggers the admin seed when the environment is configured for it.
func TestRunDevelopmentCreateSeedsAdmin(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("ECV4_DEVELOPMENT_ADMIN_EMAIL", "seed@example.com")
	t.Setenv("ECV4_DEVELOPMENT_ADMIN_SECRET", "seedsecret123")

	if _, _, err := runCLI(t, "development", "database", "create", "--development", dir); err != nil {
		t.Fatalf("database create --development: %v", err)
	}

	acct, _, err := openStore(t, dir).Credentials(ctx, "seed@example.com")
	if err != nil {
		t.Fatalf("expected a seeded admin: %v", err)
	}
	if !acct.IsAdmin || !acct.IsActive {
		t.Fatalf("seeded account is_admin=%t is_active=%t, want both true", acct.IsAdmin, acct.IsActive)
	}
}

func TestRunAccountCreateDuplicateReturnsError(t *testing.T) {
	dir := newTestDB(t)
	args := []string{"--db-dir", dir, "database", "account", "create", "--email", "dup@example.com", "--secret", "supersecret1"}

	if _, _, err := runCLI(t, "development", args...); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, stderr, err := runCLI(t, "development", args...)
	if err == nil {
		t.Fatal("expected an error creating a duplicate account")
	}
	// The conflict surfaces to the user, and the underlying error is the store's.
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	if !strings.Contains(stderr, "error:") {
		t.Fatalf("stderr = %q, want it to report the error", stderr)
	}
}

func TestRunHelpReturnsNil(t *testing.T) {
	_, stderr, err := runCLI(t, "development", "--help")
	if err != nil {
		t.Fatalf("--help should not be a failure, got %v", err)
	}
	if !strings.Contains(stderr, "game-server") {
		t.Fatalf("stderr = %q, want usage output", stderr)
	}
}

func TestRunUnknownFlagReturnsError(t *testing.T) {
	_, stderr, err := runCLI(t, "development", "--definitely-not-a-flag")
	if err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
	if !strings.Contains(stderr, "error:") {
		t.Fatalf("stderr = %q, want an error line", stderr)
	}
}

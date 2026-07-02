package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

// newTestApp returns an App that discards output and runs in development, for
// tests that call the business-logic methods directly.
func newTestApp() *App {
	return &App{Env: "development", Stdout: io.Discard, Stderr: io.Discard}
}

// newTestDB creates a fresh, migrated database in a temp directory and returns
// its directory (the value the CLI's --db-dir flag carries). The CLI helpers
// under test open the database themselves via database.Open(dir).
func newTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := database.Create(context.Background(), dir); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	return dir
}

// openStore opens the database in dir for assertions and closes it via t.Cleanup.
func openStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	pool, closeDB, err := database.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = closeDB() })
	return store.New(pool)
}

func TestCreateAccountHappyPath(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	if err := newTestApp().createAccount(ctx, dir, "  Boss@Example.com ", "supersecret1", nil, true, true); err != nil {
		t.Fatalf("createAccount: %v", err)
	}

	// Email is normalized (trimmed + lower-cased) before storage.
	acct, hash, err := openStore(t, dir).Credentials(ctx, "boss@example.com")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if !acct.IsAdmin || !acct.IsActive {
		t.Fatalf("got is_admin=%t is_active=%t, want both true", acct.IsAdmin, acct.IsActive)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("supersecret1")) != nil {
		t.Fatal("stored hash does not verify against the supplied secret")
	}
}

func TestCreateAccountGeneratesSecretWhenOmitted(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	seed := uint64(42)
	if err := newTestApp().createAccount(ctx, dir, "player@example.com", "", &seed, false, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}

	acct, hash, err := openStore(t, dir).Credentials(ctx, "player@example.com")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if acct.IsAdmin || acct.IsActive {
		t.Fatalf("got is_admin=%t is_active=%t, want both false", acct.IsAdmin, acct.IsActive)
	}
	if hash == "" {
		t.Fatal("expected a generated secret to be hashed and stored")
	}
}

func TestCreateAccountRequiresEmail(t *testing.T) {
	if err := newTestApp().createAccount(context.Background(), newTestDB(t), "   ", "secret12", nil, true, false); err == nil {
		t.Fatal("expected an error for an empty email")
	}
}

func TestCreateAccountDuplicateEmailIsConflict(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()

	if err := app.createAccount(ctx, dir, "dup@example.com", "secret12", nil, true, false); err != nil {
		t.Fatalf("first createAccount: %v", err)
	}
	err := app.createAccount(ctx, dir, "dup@example.com", "secret12", nil, true, false)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second createAccount: got %v, want ErrConflict", err)
	}
}

func TestUpdateAccountChangesRoleAndSecret(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()

	if err := app.createAccount(ctx, dir, "u@example.com", "originalpw1", nil, true, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}

	isAdmin := true
	if err := app.updateAccount(ctx, dir, "u@example.com", nil, &isAdmin, true, "newpassword2", false, nil); err != nil {
		t.Fatalf("updateAccount: %v", err)
	}

	acct, hash, err := openStore(t, dir).Credentials(ctx, "u@example.com")
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if !acct.IsAdmin {
		t.Fatal("expected is_admin to be set true")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("newpassword2")) != nil {
		t.Fatal("new secret does not verify")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("originalpw1")) == nil {
		t.Fatal("old secret still verifies after change")
	}
}

func TestUpdateAccountRejectsSecretAndGenerateTogether(t *testing.T) {
	err := newTestApp().updateAccount(context.Background(), newTestDB(t), "u@example.com", nil, nil, true, "somesecret1", true, nil)
	if err == nil || !strings.Contains(err.Error(), "either --secret or --generate-secret") {
		t.Fatalf("got %v, want a secret/generate-secret conflict error", err)
	}
}

func TestUpdateAccountRequiresAChange(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()
	if err := app.createAccount(ctx, dir, "u@example.com", "originalpw1", nil, true, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}
	err := app.updateAccount(ctx, dir, "u@example.com", nil, nil, false, "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("got %v, want a nothing-to-update error", err)
	}
}

func TestUpdateAccountUnknownEmail(t *testing.T) {
	isActive := false
	err := newTestApp().updateAccount(context.Background(), newTestDB(t), "ghost@example.com", &isActive, nil, false, "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "no account with email") {
		t.Fatalf("got %v, want a no-such-account error", err)
	}
}

func TestSeedDevelopmentAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("skips in-memory database", func(t *testing.T) {
		if err := newTestApp().seedDevelopmentAdmin(ctx, database.MemoryPath); err != nil {
			t.Fatalf("expected a no-op, got %v", err)
		}
	})

	t.Run("skips outside development", func(t *testing.T) {
		dir := newTestDB(t)
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_EMAIL", "admin@example.com")
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_SECRET", "adminsecret1")
		app := &App{Env: "production", Stdout: io.Discard, Stderr: io.Discard}
		if err := app.seedDevelopmentAdmin(ctx, dir); err != nil {
			t.Fatalf("expected a no-op, got %v", err)
		}
		if _, _, err := openStore(t, dir).Credentials(ctx, "admin@example.com"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("account should not have been seeded outside development: %v", err)
		}
	})

	t.Run("skips when env vars unset", func(t *testing.T) {
		dir := newTestDB(t)
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_EMAIL", "")
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_SECRET", "")
		if err := newTestApp().seedDevelopmentAdmin(ctx, dir); err != nil {
			t.Fatalf("expected a no-op, got %v", err)
		}
	})

	t.Run("seeds an active admin when configured", func(t *testing.T) {
		dir := newTestDB(t)
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_EMAIL", "admin@example.com")
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_SECRET", "adminsecret1")
		if err := newTestApp().seedDevelopmentAdmin(ctx, dir); err != nil {
			t.Fatalf("seedDevelopmentAdmin: %v", err)
		}
		acct, hash, err := openStore(t, dir).Credentials(ctx, "admin@example.com")
		if err != nil {
			t.Fatalf("Credentials: %v", err)
		}
		if !acct.IsAdmin || !acct.IsActive {
			t.Fatalf("seeded account is_admin=%t is_active=%t, want both true", acct.IsAdmin, acct.IsActive)
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte("adminsecret1")) != nil {
			t.Fatal("seeded admin secret does not verify")
		}
	})
}

func TestListAccountsEmpty(t *testing.T) {
	dir := newTestDB(t)
	var out bytes.Buffer
	app := &App{Env: "development", Stdout: &out, Stderr: io.Discard}
	if err := app.listAccounts(context.Background(), dir); err != nil {
		t.Fatalf("listAccounts: %v", err)
	}
	if !strings.Contains(out.String(), "no accounts") {
		t.Fatalf("stdout = %q, want the empty-table note", out.String())
	}
}

// TestListAccountsColumns seeds a mix of accounts and checks the table lists them
// all with their id, active, admin, and email — and never the hashed secret.
func TestListAccountsColumns(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	if err := newTestApp().createAccount(ctx, dir, "admin@example.com", "supersecret1", nil, true, true); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := newTestApp().createAccount(ctx, dir, "off@example.com", "supersecret1", nil, false, false); err != nil {
		t.Fatalf("seed inactive: %v", err)
	}

	var out bytes.Buffer
	app := &App{Env: "development", Stdout: &out, Stderr: io.Discard}
	if err := app.listAccounts(ctx, dir); err != nil {
		t.Fatalf("listAccounts: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "ACTIVE", "ADMIN", "EMAIL", "admin@example.com", "off@example.com", "true", "false"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "$2") {
		t.Fatalf("stdout = %q, must not leak a bcrypt hash", got)
	}
}

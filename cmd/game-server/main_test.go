package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

// quietLogger discards log output so tests don't spam stdout.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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

func TestResolveJWTSecretConfigured(t *testing.T) {
	configured := strings.Repeat("k", 32)
	for _, env := range []string{"development", "production", ""} {
		secret, err := resolveJWTSecret(env, configured, quietLogger())
		if err != nil {
			t.Fatalf("env=%q: unexpected error: %v", env, err)
		}
		if string(secret) != configured {
			t.Fatalf("env=%q: got %q, want configured secret", env, secret)
		}
	}
}

func TestResolveJWTSecretTooShort(t *testing.T) {
	if _, err := resolveJWTSecret("production", strings.Repeat("k", 31), quietLogger()); err == nil {
		t.Fatal("expected error for secret shorter than 32 bytes")
	}
}

func TestResolveJWTSecretProductionRequiresSecret(t *testing.T) {
	if _, err := resolveJWTSecret("production", "", quietLogger()); err == nil {
		t.Fatal("expected error when ECV4_ENV=production and no secret is configured")
	}
}

func TestResolveJWTSecretNonProductionGeneratesEphemeral(t *testing.T) {
	for _, env := range []string{"development", "staging", ""} {
		secret, err := resolveJWTSecret(env, "", quietLogger())
		if err != nil {
			t.Fatalf("env=%q: unexpected error: %v", env, err)
		}
		if len(secret) != 32 {
			t.Fatalf("env=%q: got %d-byte ephemeral secret, want 32", env, len(secret))
		}
	}
}

func TestResolveJWTSecretEphemeralIsRandom(t *testing.T) {
	a, err := resolveJWTSecret("development", "", quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	b, err := resolveJWTSecret("development", "", quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatal("expected two ephemeral secrets to differ")
	}
}

func TestCreateAccountHappyPath(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	if err := createAccount(ctx, dir, "  Boss@Example.com ", "supersecret1", nil, true, true); err != nil {
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
	if err := createAccount(ctx, dir, "player@example.com", "", &seed, false, false); err != nil {
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
	if err := createAccount(context.Background(), newTestDB(t), "   ", "secret12", nil, true, false); err == nil {
		t.Fatal("expected an error for an empty email")
	}
}

func TestCreateAccountDuplicateEmailIsConflict(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	if err := createAccount(ctx, dir, "dup@example.com", "secret12", nil, true, false); err != nil {
		t.Fatalf("first createAccount: %v", err)
	}
	err := createAccount(ctx, dir, "dup@example.com", "secret12", nil, true, false)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second createAccount: got %v, want ErrConflict", err)
	}
}

func TestUpdateAccountChangesRoleAndSecret(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	if err := createAccount(ctx, dir, "u@example.com", "originalpw1", nil, true, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}

	isAdmin := true
	if err := updateAccount(ctx, dir, "u@example.com", nil, &isAdmin, true, "newpassword2", false, nil); err != nil {
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
	err := updateAccount(context.Background(), newTestDB(t), "u@example.com", nil, nil, true, "somesecret1", true, nil)
	if err == nil || !strings.Contains(err.Error(), "either --secret or --generate-secret") {
		t.Fatalf("got %v, want a secret/generate-secret conflict error", err)
	}
}

func TestUpdateAccountRequiresAChange(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	if err := createAccount(ctx, dir, "u@example.com", "originalpw1", nil, true, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}
	err := updateAccount(ctx, dir, "u@example.com", nil, nil, false, "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("got %v, want a nothing-to-update error", err)
	}
}

func TestUpdateAccountUnknownEmail(t *testing.T) {
	isActive := false
	err := updateAccount(context.Background(), newTestDB(t), "ghost@example.com", &isActive, nil, false, "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "no account with email") {
		t.Fatalf("got %v, want a no-such-account error", err)
	}
}

func TestSeedDevelopmentAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("skips in-memory database", func(t *testing.T) {
		if err := seedDevelopmentAdmin(ctx, "development", database.MemoryPath); err != nil {
			t.Fatalf("expected a no-op, got %v", err)
		}
	})

	t.Run("skips outside development", func(t *testing.T) {
		dir := newTestDB(t)
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_EMAIL", "admin@example.com")
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_SECRET", "adminsecret1")
		if err := seedDevelopmentAdmin(ctx, "production", dir); err != nil {
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
		if err := seedDevelopmentAdmin(ctx, "development", dir); err != nil {
			t.Fatalf("expected a no-op, got %v", err)
		}
	})

	t.Run("seeds an active admin when configured", func(t *testing.T) {
		dir := newTestDB(t)
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_EMAIL", "admin@example.com")
		t.Setenv("ECV4_DEVELOPMENT_ADMIN_SECRET", "adminsecret1")
		if err := seedDevelopmentAdmin(ctx, "development", dir); err != nil {
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

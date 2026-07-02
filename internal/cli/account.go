package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

// seedDevelopmentAdmin optionally seeds a known, active admin account into a
// freshly created database so local smoke tests have a reliable login. It is a
// deliberate no-op — with an explanatory note — unless a.Env is development and
// both ECV4_DEVELOPMENT_ADMIN_EMAIL and ECV4_DEVELOPMENT_ADMIN_SECRET are set.
// The special in-memory database is never seeded because it is not persisted.
func (a *App) seedDevelopmentAdmin(ctx context.Context, path string) error {
	if path == database.MemoryPath {
		fmt.Fprintln(a.Stdout, "skipping --development admin seed: the in-memory database is not persisted")
		return nil
	}
	if a.Env != "development" {
		fmt.Fprintf(a.Stdout, "skipping --development admin seed: ECV4_ENV is %q, not development\n", a.Env)
		return nil
	}
	email := os.Getenv("ECV4_DEVELOPMENT_ADMIN_EMAIL")
	secret := os.Getenv("ECV4_DEVELOPMENT_ADMIN_SECRET")
	if email == "" || secret == "" {
		fmt.Fprintln(a.Stdout, "skipping --development admin seed: set ECV4_DEVELOPMENT_ADMIN_EMAIL and ECV4_DEVELOPMENT_ADMIN_SECRET to seed one")
		return nil
	}

	fmt.Fprintln(a.Stdout, "seeding development admin account...")
	return a.createAccount(ctx, path, email, secret, nil, true, true)
}

// createAccount opens the database in dbDir and inserts a new account. The
// email is normalized and the secret is bcrypt-hashed before it is stored.
func (a *App) createAccount(ctx context.Context, dbDir, email, secret string, seed *uint64, isActive, isAdmin bool) error {
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

	fmt.Fprintf(a.Stdout, "created account %d %s (is_active=%t, is_admin=%t)\n", id, email, isActive, isAdmin)
	if generated {
		fmt.Fprintf(a.Stdout, "generated secret: %s\n", secret)
		fmt.Fprintln(a.Stdout, "WARNING: only the hash is stored; this secret is shown once and cannot be recovered. Save it now.")
	}
	if !isActive {
		fmt.Fprintln(a.Stdout, "note: account is inactive and cannot log in (created with --is-inactive)")
	}
	return nil
}

// listAccounts opens the database in dbDir read-only-in-spirit (no mutation) and
// prints every account in a columnar table: id, is_active, is_admin, email. It
// is an operator/recovery tool — inspect who is admin, or recover from an admin
// lockout, without a running server or a token. Hashed secrets are never loaded.
func (a *App) listAccounts(ctx context.Context, dbDir string) error {
	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return err
	}
	defer closeDB()

	accounts, err := store.New(pool).ListAccounts(ctx)
	if err != nil {
		return err
	}

	if len(accounts) == 0 {
		fmt.Fprintln(a.Stdout, "no accounts")
		return nil
	}

	// Right-align the id column to the widest id so the table stays aligned.
	idWidth := len("ID")
	for _, acc := range accounts {
		if w := len(strconv.FormatInt(acc.ID, 10)); w > idWidth {
			idWidth = w
		}
	}

	fmt.Fprintf(a.Stdout, "%*s  %-8s  %-8s  %s\n", idWidth, "ID", "ACTIVE", "ADMIN", "EMAIL")
	for _, acc := range accounts {
		fmt.Fprintf(a.Stdout, "%*d  %-8t  %-8t  %s\n", idWidth, acc.ID, acc.IsActive, acc.IsAdmin, acc.Email)
	}
	return nil
}

// updateAccount opens the database in dbDir and applies a partial update to the
// account selected by email. isActive and isAdmin are nil to leave unchanged.
// The password changes when secretProvided is set (to secret) or generate is
// set (to a fresh passphrase, printed once); at most one may be requested. At
// least one change overall is required.
func (a *App) updateAccount(ctx context.Context, dbDir, email string, isActive, isAdmin *bool, secretProvided bool, secret string, generate bool, seed *uint64) error {
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

	fmt.Fprintf(a.Stdout, "updated account %s\n", email)
	if isActive != nil {
		fmt.Fprintf(a.Stdout, "  is_active=%t\n", *isActive)
	}
	if isAdmin != nil {
		fmt.Fprintf(a.Stdout, "  is_admin=%t\n", *isAdmin)
	}
	if generatedSecret != "" {
		fmt.Fprintf(a.Stdout, "  generated secret: %s\n", generatedSecret)
		fmt.Fprintln(a.Stdout, "  WARNING: only the hash is stored; this secret is shown once and cannot be recovered. Save it now.")
	} else if hashedSecret != nil {
		fmt.Fprintln(a.Stdout, "  secret updated")
	}
	return nil
}

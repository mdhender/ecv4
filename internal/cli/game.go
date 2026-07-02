package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mdhender/ecv4/internal/database"
	"github.com/mdhender/ecv4/internal/store"
)

// gameCodePattern and memberHandlePattern mirror the DB CHECKs the migrations
// apply (games.code and game_account_role.handle) and the equivalent
// service-layer patterns in internal/handlers/games.go. They are duplicated here
// rather than exported so the offline verbs give a clear error instead of letting
// the DB CHECK surface as an opaque failure; the store's CHECK remains the
// backstop.
var (
	gameCodePattern     = regexp.MustCompile(`^[A-Z][A-Z]+$`)
	memberHandlePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]+$`)
)

// createGame opens the database in dbDir and inserts a new game directly, with no
// running server and no authorization gate — it is the offline admin bootstrap
// analog of `database account create`. It enforces only store-level integrity
// (valid code, non-empty name, unique code), NOT the API's lifecycle/role action
// matrix. The new game starts in 'draft' and active; it is printed on success.
func (a *App) createGame(ctx context.Context, dbDir, code, name string, description *string) error {
	code = strings.TrimSpace(code)
	if !gameCodePattern.MatchString(code) {
		return fmt.Errorf("code must be two or more uppercase ASCII letters (A-Z)")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("game create requires --name")
	}
	if description != nil {
		trimmed := strings.TrimSpace(*description)
		description = &trimmed
	}

	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return err
	}
	defer closeDB()

	game, err := store.New(pool).CreateGame(ctx, code, name, description)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("a game with code %q already exists", code)
		}
		return err
	}

	a.printGame(game)
	return nil
}

// listGames opens the database in dbDir and prints every game — including hidden
// ones (is_active=0) — in a columnar table: id, active, status, code, name. It is
// a read-only operator/bootstrap tool: no mutation, no running server, no token.
// It lists all games by calling ListGames as an admin.
func (a *App) listGames(ctx context.Context, dbDir string) error {
	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return err
	}
	defer closeDB()

	// isAdmin=true sees every game including hard-hidden ones; accountID is ignored
	// for admins, and a nil status leaves the listing unfiltered.
	games, err := store.New(pool).ListGames(ctx, 0, true, nil)
	if err != nil {
		return err
	}

	if len(games) == 0 {
		fmt.Fprintln(a.Stdout, "no games")
		return nil
	}

	// Right-align the id column to the widest id so the table stays aligned.
	idWidth := len("ID")
	for _, g := range games {
		if w := len(strconv.FormatInt(g.ID, 10)); w > idWidth {
			idWidth = w
		}
	}

	fmt.Fprintf(a.Stdout, "%*s  %-8s  %-10s  %-10s  %s\n", idWidth, "ID", "ACTIVE", "STATUS", "CODE", "NAME")
	for _, g := range games {
		fmt.Fprintf(a.Stdout, "%*d  %-8t  %-10s  %-10s  %s\n", idWidth, g.ID, g.IsActive, g.Status, g.Code, g.Name)
	}
	return nil
}

// addMember opens the database in dbDir and assigns the account with email to the
// game with code as a new active member. handle defaults to player_N when empty
// (N = the game's current membership count + 1); a supplied handle is validated
// and a collision — computed or supplied — fails clearly (never auto-bumped).
// isGM makes the member a game master. Like the other offline verbs it enforces
// only store-level integrity, NOT the API's recruiting-only / active-GM gates.
func (a *App) addMember(ctx context.Context, dbDir, code, email, handle string, isGM bool) error {
	code = strings.TrimSpace(code)
	if !gameCodePattern.MatchString(code) {
		return fmt.Errorf("code must be two or more uppercase ASCII letters (A-Z)")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("game add-member requires --email")
	}
	handle = strings.TrimSpace(handle)
	if handle != "" && !memberHandlePattern.MatchString(handle) {
		return fmt.Errorf("handle must be two or more characters, start with a letter, and use only letters, digits, '.', '_' or '-'")
	}

	pool, closeDB, err := database.Open(ctx, dbDir)
	if err != nil {
		return err
	}
	defer closeDB()
	st := store.New(pool)

	game, err := st.GameByCode(ctx, code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no game with code %q", code)
		}
		return err
	}
	account, err := st.AccountByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no account with email %q", email)
		}
		return err
	}

	member, err := st.AddMember(ctx, game.ID, account.ID, handle, isGM)
	switch {
	case errors.Is(err, store.ErrMemberExists):
		return fmt.Errorf("account %q is already a member of game %q", email, code)
	case errors.Is(err, store.ErrHandleTaken):
		return fmt.Errorf("handle %q is already in use in game %q", handle, code)
	case errors.Is(err, store.ErrNotFound):
		// AddMember re-checks the account FK inside its transaction.
		return fmt.Errorf("no account with email %q", email)
	case err != nil:
		return err
	}

	fmt.Fprintf(a.Stdout, "added member to game %d %s: account %d %s, handle %s (is_gm=%t, is_active=%t)\n",
		game.ID, game.Code, account.ID, email, member.Handle, member.IsGM, member.IsActive)
	return nil
}

// printGame writes a one-line summary of a created/looked-up game, plus its
// description on a second line when present.
func (a *App) printGame(g store.Game) {
	fmt.Fprintf(a.Stdout, "created game %d %s %q (status=%s, is_active=%t)\n", g.ID, g.Code, g.Name, g.Status, g.IsActive)
	if g.Description != nil {
		fmt.Fprintf(a.Stdout, "  description: %s\n", *g.Description)
	}
}

package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mdhender/ecv4/internal/store"
)

func TestCreateGameHappyPath(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)

	desc := "The first playtest."
	var out bytes.Buffer
	app := &App{Env: "development", Stdout: &out, Stderr: io.Discard}
	if err := app.createGame(ctx, dir, "  ALPHA ", " Alpha Campaign ", &desc); err != nil {
		t.Fatalf("createGame: %v", err)
	}

	// The created game is printed, code trimmed, with its description.
	got := out.String()
	for _, want := range []string{"ALPHA", "Alpha Campaign", "draft", "The first playtest."} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want it to contain %q", got, want)
		}
	}

	// It lands in the database in draft, active, code trimmed.
	game, err := openStore(t, dir).GameByCode(ctx, "ALPHA")
	if err != nil {
		t.Fatalf("GameByCode: %v", err)
	}
	if game.Name != "Alpha Campaign" || game.Status != "draft" || !game.IsActive ||
		game.Description == nil || *game.Description != desc {
		t.Fatalf("stored game = %+v, want name=Alpha Campaign status=draft active with description", game)
	}
}

func TestCreateGameRejectsBadCode(t *testing.T) {
	err := newTestApp().createGame(context.Background(), newTestDB(t), "alpha1", "Bad Code", nil)
	if err == nil || !strings.Contains(err.Error(), "uppercase") {
		t.Fatalf("got %v, want a code-format error", err)
	}
}

func TestCreateGameRequiresName(t *testing.T) {
	err := newTestApp().createGame(context.Background(), newTestDB(t), "ALPHA", "   ", nil)
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("got %v, want a missing-name error", err)
	}
}

func TestCreateGameDuplicateCodeIsConflict(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()
	if err := app.createGame(ctx, dir, "ALPHA", "First", nil); err != nil {
		t.Fatalf("first createGame: %v", err)
	}
	err := app.createGame(ctx, dir, "ALPHA", "Second", nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v, want a duplicate-code error", err)
	}
}

func TestListGamesEmpty(t *testing.T) {
	dir := newTestDB(t)
	var out bytes.Buffer
	app := &App{Env: "development", Stdout: &out, Stderr: io.Discard}
	if err := app.listGames(context.Background(), dir); err != nil {
		t.Fatalf("listGames: %v", err)
	}
	if !strings.Contains(out.String(), "no games") {
		t.Fatalf("stdout = %q, want the empty-table note", out.String())
	}
}

// TestListGamesIncludesHidden seeds an active and a hard-hidden (is_active=0) game
// and checks that list prints both — the offline verb must not hide anything.
func TestListGamesIncludesHidden(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	if err := newTestApp().createGame(ctx, dir, "VISIBLE", "Visible Game", nil); err != nil {
		t.Fatalf("createGame visible: %v", err)
	}
	if err := newTestApp().createGame(ctx, dir, "HIDDEN", "Hidden Game", nil); err != nil {
		t.Fatalf("createGame hidden: %v", err)
	}
	// Hard-hide the second game directly through the store.
	st := openStore(t, dir)
	hidden, err := st.GameByCode(ctx, "HIDDEN")
	if err != nil {
		t.Fatalf("GameByCode: %v", err)
	}
	inactive := false
	if _, err := st.UpdateGame(ctx, hidden.ID, store.GameUpdate{IsActive: &inactive}); err != nil {
		t.Fatalf("hide game: %v", err)
	}

	var out bytes.Buffer
	app := &App{Env: "development", Stdout: &out, Stderr: io.Discard}
	if err := app.listGames(ctx, dir); err != nil {
		t.Fatalf("listGames: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "ACTIVE", "STATUS", "CODE", "NAME", "VISIBLE", "HIDDEN", "draft"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want it to contain %q", got, want)
		}
	}
}

func TestAssignGMDefaultHandle(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()
	if err := app.createGame(ctx, dir, "ALPHA", "Alpha", nil); err != nil {
		t.Fatalf("createGame: %v", err)
	}
	if err := app.createAccount(ctx, dir, "gm@example.com", "supersecret1", nil, true, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}

	// assign-gm forces is_gm=true; with no handle it defaults to player_1.
	if err := app.addMember(ctx, dir, "ALPHA", "gm@example.com", "", true); err != nil {
		t.Fatalf("assign-gm: %v", err)
	}

	st := openStore(t, dir)
	game, _ := st.GameByCode(ctx, "ALPHA")
	account, _ := st.AccountByEmail(ctx, "gm@example.com")
	member, err := st.MemberForGame(ctx, game.ID, account.ID)
	if err != nil {
		t.Fatalf("MemberForGame: %v", err)
	}
	if member.Handle != "player_1" || !member.IsGM || !member.IsActive {
		t.Fatalf("member = %+v, want handle=player_1 is_gm=true is_active=true", member)
	}
}

func TestAddMemberSuppliedHandle(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()
	if err := app.createGame(ctx, dir, "ALPHA", "Alpha", nil); err != nil {
		t.Fatalf("createGame: %v", err)
	}
	if err := app.createAccount(ctx, dir, "player@example.com", "supersecret1", nil, true, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}

	if err := app.addMember(ctx, dir, "ALPHA", "player@example.com", " Warlord ", false); err != nil {
		t.Fatalf("add-member: %v", err)
	}

	st := openStore(t, dir)
	game, _ := st.GameByCode(ctx, "ALPHA")
	account, _ := st.AccountByEmail(ctx, "player@example.com")
	member, err := st.MemberForGame(ctx, game.ID, account.ID)
	if err != nil {
		t.Fatalf("MemberForGame: %v", err)
	}
	if member.Handle != "Warlord" || member.IsGM || !member.IsActive {
		t.Fatalf("member = %+v, want handle=Warlord is_gm=false is_active=true", member)
	}
}

func TestAddMemberDuplicateHandleFails(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()
	if err := app.createGame(ctx, dir, "ALPHA", "Alpha", nil); err != nil {
		t.Fatalf("createGame: %v", err)
	}
	if err := app.createAccount(ctx, dir, "one@example.com", "supersecret1", nil, true, false); err != nil {
		t.Fatalf("createAccount one: %v", err)
	}
	if err := app.createAccount(ctx, dir, "two@example.com", "supersecret1", nil, true, false); err != nil {
		t.Fatalf("createAccount two: %v", err)
	}

	if err := app.addMember(ctx, dir, "ALPHA", "one@example.com", "Warlord", true); err != nil {
		t.Fatalf("first add-member: %v", err)
	}
	// A second member claiming the same handle fails clearly, never auto-bumped.
	err := app.addMember(ctx, dir, "ALPHA", "two@example.com", "Warlord", false)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("got %v, want a duplicate-handle error", err)
	}
}

func TestAddMemberUnknownGameAndAccount(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()

	// Unknown game.
	if err := app.addMember(ctx, dir, "GHOST", "someone@example.com", "", false); err == nil || !strings.Contains(err.Error(), "no game with code") {
		t.Fatalf("got %v, want a no-such-game error", err)
	}

	// Known game, unknown account.
	if err := app.createGame(ctx, dir, "ALPHA", "Alpha", nil); err != nil {
		t.Fatalf("createGame: %v", err)
	}
	if err := app.addMember(ctx, dir, "ALPHA", "ghost@example.com", "", false); err == nil || !strings.Contains(err.Error(), "no account with email") {
		t.Fatalf("got %v, want a no-such-account error", err)
	}
}

func TestAddMemberRejectsBadHandle(t *testing.T) {
	ctx := context.Background()
	dir := newTestDB(t)
	app := newTestApp()
	if err := app.createGame(ctx, dir, "ALPHA", "Alpha", nil); err != nil {
		t.Fatalf("createGame: %v", err)
	}
	if err := app.createAccount(ctx, dir, "p@example.com", "supersecret1", nil, true, false); err != nil {
		t.Fatalf("createAccount: %v", err)
	}
	err := app.addMember(ctx, dir, "ALPHA", "p@example.com", "!bad", false)
	if err == nil || !strings.Contains(err.Error(), "handle must be") {
		t.Fatalf("got %v, want a handle-format error", err)
	}
}

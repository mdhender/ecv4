package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mdhender/ecv4/internal/api"
)

func TestCreateGameSuccess(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	rr := doJSON(t, handler, http.MethodPost, "/games", token, map[string]any{
		"code": "ALPHA", "name": "Alpha Campaign", "description": "First playtest.",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201 (body %q)", rr.Code, rr.Body.String())
	}

	var game api.Game
	if err := json.Unmarshal(rr.Body.Bytes(), &game); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if game.Id == 0 || game.Code != "ALPHA" || game.Name != "Alpha Campaign" {
		t.Fatalf("game = %+v, want id!=0 code=ALPHA name=\"Alpha Campaign\"", game)
	}
	// A freshly created game is in draft status.
	if game.Status != api.Draft {
		t.Fatalf("status = %q, want %q", game.Status, api.Draft)
	}
	if game.Description == nil || *game.Description != "First playtest." {
		t.Fatalf("description = %v, want \"First playtest.\"", game.Description)
	}
}

func TestCreateGameRejectsBadCode(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	// Each of these violates ^[A-Z][A-Z]+$ and must be a 400 (not a DB-CHECK 500).
	for _, code := range []string{"alpha", "A", "ALPHA1", "AL-PHA", "AL PHA", ""} {
		rr := doJSON(t, handler, http.MethodPost, "/games", token, map[string]any{
			"code": code, "name": "A Game",
		})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("code %q: got %d, want 400 (body %q)", code, rr.Code, rr.Body.String())
		}
		var body api.ErrorResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if body.Code != "bad_request" {
			t.Fatalf("code %q: error code = %q, want bad_request", code, body.Code)
		}
	}
}

func TestCreateGameRequiresName(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	rr := doJSON(t, handler, http.MethodPost, "/games", token, map[string]any{
		"code": "ALPHA", "name": "   ",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("blank name: got %d, want 400 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestCreateGameConflict(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	body := map[string]any{"code": "ALPHA", "name": "Alpha"}
	if rr := doJSON(t, handler, http.MethodPost, "/games", token, body); rr.Code != http.StatusCreated {
		t.Fatalf("first create: got %d, want 201 (body %q)", rr.Code, rr.Body.String())
	}
	rr := doJSON(t, handler, http.MethodPost, "/games", token, body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate: got %d, want 409 (body %q)", rr.Code, rr.Body.String())
	}
	var errBody api.ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Code != "conflict" {
		t.Fatalf("error code = %q, want conflict", errBody.Code)
	}
}

func TestCreateGameRequiresAdmin(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)

	// A non-admin caller is forbidden (403).
	insertAccount(t, pool, 2, "player@example.com", false, true)
	playerAccess, _, err := tokens.IssueAccess(2, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if rr := doJSON(t, handler, http.MethodPost, "/games", playerAccess, map[string]any{
		"code": "ALPHA", "name": "Alpha",
	}); rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin: got %d, want 403 (body %q)", rr.Code, rr.Body.String())
	}

	// No credentials at all is a 401.
	if rr := doJSON(t, handler, http.MethodPost, "/games", "", map[string]any{
		"code": "ALPHA", "name": "Alpha",
	}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: got %d, want 401 (body %q)", rr.Code, rr.Body.String())
	}
}

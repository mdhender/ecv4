package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
)

// adminToken seeds an active admin account and returns a bearer access token for
// it, for driving the admin-only account endpoints through the full handler.
func adminToken(t *testing.T, pool *sqlitemigration.Pool, tokens *auth.TokenService, id int64, email string) string {
	t.Helper()
	insertAccount(t, pool, id, email, true, true)
	access, _, err := tokens.IssueAccess(id, email, []string{"admin"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	return access
}

// doJSON sends a request with an optional bearer token and JSON body through the
// full HTTP handler, returning the recorder.
func doJSON(t *testing.T, handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func newAccountsHandler(t *testing.T) (http.Handler, *sqlitemigration.Pool, *auth.TokenService) {
	t.Helper()
	st, pool := seedStore(t)
	tokens := testTokens()
	handler := NewHTTPHandler(NewServer(st, tokens), http.NewServeMux(), tokens)
	return handler, pool, tokens
}

func TestCreateAccountGeneratesSecret(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	rr := doJSON(t, handler, http.MethodPost, "/accounts", token, map[string]any{
		"email": "New@Example.com", "isActive": true, "isAdmin": false,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201 (body %q)", rr.Code, rr.Body.String())
	}

	var resp api.CreateAccountResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(resp.Account.Email); got != "new@example.com" {
		t.Fatalf("email = %q, want normalized new@example.com", got)
	}
	if !resp.Account.IsActive || resp.Account.IsAdmin {
		t.Fatalf("flags: %+v, want isActive=true isAdmin=false", resp.Account)
	}
	if resp.GeneratedSecret == nil || *resp.GeneratedSecret == "" {
		t.Fatal("expected a generated secret to be echoed when secret is omitted")
	}
}

func TestCreateAccountWithSecretOmitsGenerated(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	rr := doJSON(t, handler, http.MethodPost, "/accounts", token, map[string]any{
		"email": "chosen@example.com", "secret": "supplied-secret",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201 (body %q)", rr.Code, rr.Body.String())
	}
	var resp api.CreateAccountResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.GeneratedSecret != nil {
		t.Fatalf("generatedSecret should be absent when a secret is supplied, got %q", *resp.GeneratedSecret)
	}
	// Defaults: omitted isActive/isAdmin are false.
	if resp.Account.IsActive || resp.Account.IsAdmin {
		t.Fatalf("flags: %+v, want both false by default", resp.Account)
	}
}

func TestCreateAccountDuplicateIs409(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	body := map[string]any{"email": "dupe@example.com", "secret": "supplied-secret"}
	if rr := doJSON(t, handler, http.MethodPost, "/accounts", token, body); rr.Code != http.StatusCreated {
		t.Fatalf("first create: got %d, want 201", rr.Code)
	}
	if rr := doJSON(t, handler, http.MethodPost, "/accounts", token, body); rr.Code != http.StatusConflict {
		t.Fatalf("second create: got %d, want 409 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestCreateAccountShortSecretIs400(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	rr := doJSON(t, handler, http.MethodPost, "/accounts", token, map[string]any{
		"email": "short@example.com", "secret": "short",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("short secret: got %d, want 400 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestCreateAccountNonAdminIs403(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	insertAccount(t, pool, 2, "player@example.com", false, true)
	access, _, err := tokens.IssueAccess(2, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	rr := doJSON(t, handler, http.MethodPost, "/accounts", access, map[string]any{
		"email": "nope@example.com", "secret": "supplied-secret",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: got %d, want 403 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestGetAccount(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")
	insertAccount(t, pool, 50, "target@example.com", false, true)

	rr := doJSON(t, handler, http.MethodGet, "/accounts/50", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: got %d, want 200 (body %q)", rr.Code, rr.Body.String())
	}
	var account api.Account
	if err := json.Unmarshal(rr.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if account.Id != 50 || string(account.Email) != "target@example.com" || !account.IsActive || account.IsAdmin {
		t.Fatalf("unexpected account: %+v", account)
	}
}

func TestGetAccountUnknownIdIs404(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	rr := doJSON(t, handler, http.MethodGet, "/accounts/999", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestGetAccountNonAdminIs403(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	insertAccount(t, pool, 2, "player@example.com", false, true)
	insertAccount(t, pool, 50, "target@example.com", false, true)
	access, _, err := tokens.IssueAccess(2, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	rr := doJSON(t, handler, http.MethodGet, "/accounts/50", access, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin get: got %d, want 403 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestListAccounts(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")
	insertAccount(t, pool, 50, "target@example.com", false, true)

	rr := doJSON(t, handler, http.MethodGet, "/accounts", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: got %d, want 200 (body %q)", rr.Code, rr.Body.String())
	}
	var resp api.ListAccountsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Both the seeded admin (id 1) and the target (id 50), ordered by id.
	if len(resp.Accounts) != 2 {
		t.Fatalf("got %d accounts, want 2 (body %q)", len(resp.Accounts), rr.Body.String())
	}
	if resp.Accounts[0].Id != 1 || resp.Accounts[1].Id != 50 {
		t.Fatalf("ids = %d, %d; want 1, 50", resp.Accounts[0].Id, resp.Accounts[1].Id)
	}
}

func TestListAccountsNonAdminIs403(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	insertAccount(t, pool, 2, "player@example.com", false, true)
	access, _, err := tokens.IssueAccess(2, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	rr := doJSON(t, handler, http.MethodGet, "/accounts", access, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: got %d, want 403 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestUpdateAccountPartial(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")
	insertAccount(t, pool, 50, "target@example.com", false, false)

	rr := doJSON(t, handler, http.MethodPatch, "/accounts/50", token, map[string]any{
		"isActive": true,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: got %d, want 200 (body %q)", rr.Code, rr.Body.String())
	}
	var account api.Account
	if err := json.Unmarshal(rr.Body.Bytes(), &account); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !account.IsActive {
		t.Fatal("isActive should be true after update")
	}
	if account.IsAdmin {
		t.Fatal("isAdmin should be unchanged (false)")
	}
}

func TestUpdateAccountUnknownIdIs404(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")

	rr := doJSON(t, handler, http.MethodPatch, "/accounts/999", token, map[string]any{
		"isActive": true,
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestUpdateAccountEmptyIs400(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")
	insertAccount(t, pool, 50, "target@example.com", false, true)

	rr := doJSON(t, handler, http.MethodPatch, "/accounts/50", token, map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty update: got %d, want 400 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestUpdateAccountShortSecretIs400(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	token := adminToken(t, pool, tokens, 1, "admin@example.com")
	insertAccount(t, pool, 50, "target@example.com", false, true)

	rr := doJSON(t, handler, http.MethodPatch, "/accounts/50", token, map[string]any{
		"secret": "short",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("short secret: got %d, want 400 (body %q)", rr.Code, rr.Body.String())
	}
}

func TestUpdateAccountNonAdminIs403(t *testing.T) {
	handler, pool, tokens := newAccountsHandler(t)
	insertAccount(t, pool, 2, "player@example.com", false, true)
	insertAccount(t, pool, 50, "target@example.com", false, true)
	access, _, err := tokens.IssueAccess(2, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	rr := doJSON(t, handler, http.MethodPatch, "/accounts/50", access, map[string]any{
		"isActive": false,
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin update: got %d, want 403 (body %q)", rr.Code, rr.Body.String())
	}
}

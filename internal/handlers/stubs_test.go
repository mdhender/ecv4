package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mdhender/ecv4/internal/api"
)

// TestUnimplementedStubReturns501 pins the behavior of the out-of-scope game
// handlers: rather than the misleading empty 200 they used to return, they now
// surface a 501 with the standard error envelope.
func TestUnimplementedStubReturns501(t *testing.T) {
	st, pool := seedStore(t)
	tokens := testTokens()
	srv := NewServer(st, tokens)
	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)

	insertAccount(t, pool, 1, "admin@example.com", true, true)
	access, _, err := tokens.IssueAccess(1, "admin@example.com", []string{"admin"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	rr := doJSON(t, handler, http.MethodGet, "/games", access, nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("GET /games: got %d, want 501 (body %q)", rr.Code, rr.Body.String())
	}

	var body api.ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (raw %q)", err, rr.Body.String())
	}
	if body.Code != "not_implemented" {
		t.Fatalf("error code: got %q, want %q", body.Code, "not_implemented")
	}
}

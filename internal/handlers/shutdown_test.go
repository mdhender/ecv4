package handlers

import (
	"net/http"
	"testing"
)

// newShutdownHandler wires a handler whose shutdown trigger records that it
// fired, and returns the handler, the seeded-admin path helpers, and a pointer
// to the "fired" flag. When development is false the route is left disabled
// (no WithShutdown), mirroring a server started without --development.
func newShutdownHandler(t *testing.T, development bool) (http.Handler, *bool) {
	t.Helper()
	st, pool := seedStore(t)
	tokens := testTokens()
	fired := false

	var opts []Option
	if development {
		opts = append(opts, WithShutdown(func() { fired = true }))
	}
	srv := NewServer(st, tokens, opts...)
	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)

	// Seed an active admin (id 1) and a plain player (id 2) for the auth cases.
	insertAccount(t, pool, 1, "admin@example.com", true, true)
	insertAccount(t, pool, 2, "player@example.com", false, true)
	return handler, &fired
}

func adminAccess(t *testing.T) string {
	t.Helper()
	access, _, err := testTokens().IssueAccess(1, "admin@example.com", []string{"admin"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	return access
}

func playerAccess(t *testing.T) string {
	t.Helper()
	access, _, err := testTokens().IssueAccess(2, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	return access
}

func TestShutdownAdminTriggers202(t *testing.T) {
	handler, fired := newShutdownHandler(t, true)

	rr := doJSON(t, handler, http.MethodPost, "/admin/shutdown", adminAccess(t), nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("shutdown: got %d, want 202 (body %q)", rr.Code, rr.Body.String())
	}
	if !*fired {
		t.Fatal("shutdown trigger was not fired")
	}
}

func TestShutdownNonAdminIs403(t *testing.T) {
	handler, fired := newShutdownHandler(t, true)

	rr := doJSON(t, handler, http.MethodPost, "/admin/shutdown", playerAccess(t), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin shutdown: got %d, want 403 (body %q)", rr.Code, rr.Body.String())
	}
	if *fired {
		t.Fatal("shutdown must not fire for a non-admin caller")
	}
}

func TestShutdownNoTokenIs401(t *testing.T) {
	handler, fired := newShutdownHandler(t, true)

	rr := doJSON(t, handler, http.MethodPost, "/admin/shutdown", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated shutdown: got %d, want 401 (body %q)", rr.Code, rr.Body.String())
	}
	if *fired {
		t.Fatal("shutdown must not fire without authentication")
	}
}

func TestShutdownDisabledIs404(t *testing.T) {
	// Without --development the route is not wired: even an admin gets 404, so
	// the capability is invisible in a non-development deployment.
	handler, fired := newShutdownHandler(t, false)

	rr := doJSON(t, handler, http.MethodPost, "/admin/shutdown", adminAccess(t), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("disabled shutdown: got %d, want 404 (body %q)", rr.Code, rr.Body.String())
	}
	if *fired {
		t.Fatal("disabled shutdown must never fire")
	}
}

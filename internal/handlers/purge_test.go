package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mdhender/ecv4/internal/store"
)

// newPurgeHandler wires a handler over a store seeded with an admin (id 1) and a
// plain player (id 2), plus a set of refresh tokens: two already expired and one
// far in the future. It returns the handler and the backing store.
func newPurgeHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, pool := seedStore(t)
	tokens := testTokens()
	srv := NewServer(st, tokens)
	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)

	insertAccount(t, pool, 1, "admin@example.com", true, true)
	insertAccount(t, pool, 2, "player@example.com", false, true)

	// testTokens uses the real clock, so anything with expires_at at 1/2 is long
	// expired while the far-future one survives any plausible "now".
	ctx := context.Background()
	if err := st.CreateRefreshToken(ctx, "expired-1", "fam", 1, 1, 2); err != nil {
		t.Fatalf("seed expired-1: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "expired-2", "fam", 2, 1, 2); err != nil {
		t.Fatalf("seed expired-2: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "live", "fam", 1, 1, 1<<40); err != nil {
		t.Fatalf("seed live: %v", err)
	}
	return handler, st
}

func TestPurgeRefreshTokensAdminPurges(t *testing.T) {
	handler, st := newPurgeHandler(t)

	rr := doJSON(t, handler, http.MethodPost, "/admin/refresh-tokens/purge", adminAccess(t), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("purge: got %d, want 200 (body %q)", rr.Code, rr.Body.String())
	}

	var body struct {
		Purged int64 `json:"purged"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	if body.Purged != 2 {
		t.Fatalf("purged = %d, want 2", body.Purged)
	}

	// The two expired tokens are gone; the live one survives.
	ctx := context.Background()
	for _, jti := range []string{"expired-1", "expired-2"} {
		if _, err := st.RefreshTokenByJTI(ctx, jti); err == nil {
			t.Fatalf("%q should have been purged", jti)
		}
	}
	if _, err := st.RefreshTokenByJTI(ctx, "live"); err != nil {
		t.Fatalf("live token should survive: %v", err)
	}
}

func TestPurgeRefreshTokensNonAdminIs403(t *testing.T) {
	handler, st := newPurgeHandler(t)

	rr := doJSON(t, handler, http.MethodPost, "/admin/refresh-tokens/purge", playerAccess(t), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-admin purge: got %d, want 403 (body %q)", rr.Code, rr.Body.String())
	}
	// Nothing was purged.
	if _, err := st.RefreshTokenByJTI(context.Background(), "expired-1"); err != nil {
		t.Fatalf("non-admin call must not purge: %v", err)
	}
}

func TestPurgeRefreshTokensNoTokenIs401(t *testing.T) {
	handler, _ := newPurgeHandler(t)

	rr := doJSON(t, handler, http.MethodPost, "/admin/refresh-tokens/purge", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated purge: got %d, want 401 (body %q)", rr.Code, rr.Body.String())
	}
}

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// fixedClockTokens is a token service whose clock is pinned to now (unix
// seconds), so the sessions handlers filter expiry deterministically.
func fixedClockTokens(now int64) *auth.TokenService {
	at := time.Unix(now, 0).UTC()
	return auth.NewTokenService([]byte("0123456789abcdef0123456789abcdef"),
		15*time.Minute, time.Hour, auth.WithClock(func() time.Time { return at }))
}

// seedRefresh inserts a refresh-token row via the store, failing on error.
func seedRefresh(t *testing.T, st *store.Store, jti, family string, accountID, issuedAt, expiresAt int64) {
	t.Helper()
	if err := st.CreateRefreshToken(context.Background(), jti, family, accountID, issuedAt, expiresAt); err != nil {
		t.Fatalf("CreateRefreshToken %q: %v", jti, err)
	}
}

// callListMySessions invokes the handler directly with claims in the context.
func callListMySessions(t *testing.T, srv *Server, claims auth.Claims, withClaims bool) api.ListMySessionsResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := srv.ListMySessions(ctx, api.ListMySessionsRequestObject{})
	if err != nil {
		t.Fatalf("ListMySessions returned error: %v", err)
	}
	return resp
}

// callRevokeMySession invokes the handler directly with claims in the context.
func callRevokeMySession(t *testing.T, srv *Server, claims auth.Claims, withClaims bool, familyID string) api.RevokeMySessionResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := srv.RevokeMySession(ctx, api.RevokeMySessionRequestObject{FamilyId: familyID})
	if err != nil {
		t.Fatalf("RevokeMySession returned error: %v", err)
	}
	return resp
}

func TestListMySessionsReturnsActive(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "me@example.com", false, true)
	insertAccount(t, pool, 2, "other@example.com", false, true)

	// fam-live: rotated — old token revoked, newer token live (its times win).
	seedRefresh(t, st, "live-old", "fam-live", 1, 10, 500)
	if err := st.RevokeRefreshToken(context.Background(), "live-old"); err != nil {
		t.Fatalf("revoke live-old: %v", err)
	}
	seedRefresh(t, st, "live-new", "fam-live", 1, 20, 600)
	// fam-newer sorts ahead of fam-live (later issued_at).
	seedRefresh(t, st, "newer", "fam-newer", 1, 30, 700)
	// A fully revoked family and an expired one are not sessions.
	seedRefresh(t, st, "revoked", "fam-revoked", 1, 5, 900)
	if err := st.RevokeFamily(context.Background(), "fam-revoked"); err != nil {
		t.Fatalf("revoke fam-revoked: %v", err)
	}
	seedRefresh(t, st, "expired", "fam-expired", 1, 1, 100)
	// A bystander's live session must not leak.
	seedRefresh(t, st, "bystander", "fam-bystander", 2, 15, 800)

	srv := NewServer(st, fixedClockTokens(200))
	resp := callListMySessions(t, srv, auth.Claims{UserID: 1}, true)
	ok, is := resp.(api.ListMySessions200JSONResponse)
	if !is {
		t.Fatalf("got %T, want ListMySessions200JSONResponse", resp)
	}
	want := []api.Session{
		{FamilyId: "fam-newer", IssuedAt: time.Unix(30, 0).UTC(), ExpiresAt: time.Unix(700, 0).UTC()},
		{FamilyId: "fam-live", IssuedAt: time.Unix(20, 0).UTC(), ExpiresAt: time.Unix(600, 0).UTC()},
	}
	if len(ok.Sessions) != len(want) {
		t.Fatalf("got %d sessions, want %d: %+v", len(ok.Sessions), len(want), ok.Sessions)
	}
	for i := range want {
		if !ok.Sessions[i].IssuedAt.Equal(want[i].IssuedAt) ||
			!ok.Sessions[i].ExpiresAt.Equal(want[i].ExpiresAt) ||
			ok.Sessions[i].FamilyId != want[i].FamilyId {
			t.Fatalf("session %d = %+v, want %+v", i, ok.Sessions[i], want[i])
		}
	}
}

func TestListMySessionsEmpty(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 5, "loner@example.com", false, true)
	srv := NewServer(st, fixedClockTokens(0))
	resp := callListMySessions(t, srv, auth.Claims{UserID: 5}, true)
	ok, is := resp.(api.ListMySessions200JSONResponse)
	if !is {
		t.Fatalf("got %T, want 200", resp)
	}
	if len(ok.Sessions) != 0 {
		t.Fatalf("got %d sessions, want 0", len(ok.Sessions))
	}
}

func TestListMySessionsNoClaimsIs401(t *testing.T) {
	st, _ := seedStore(t)
	srv := NewServer(st, fixedClockTokens(0))
	if _, is := callListMySessions(t, srv, auth.Claims{}, false).(api.ListMySessions401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

func TestListMySessionsInactiveAccountIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 7, "gone@example.com", false, false)
	srv := NewServer(st, fixedClockTokens(0))
	if _, is := callListMySessions(t, srv, auth.Claims{UserID: 7}, true).(api.ListMySessions401JSONResponse); !is {
		t.Fatal("expected 401 when the account is inactive")
	}
}

func TestListMySessionsUnknownAccountIs401(t *testing.T) {
	st, _ := seedStore(t)
	srv := NewServer(st, fixedClockTokens(0))
	if _, is := callListMySessions(t, srv, auth.Claims{UserID: 999}, true).(api.ListMySessions401JSONResponse); !is {
		t.Fatal("expected 401 when the account does not exist")
	}
}

func TestRevokeMySessionOwnFamily(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "me@example.com", false, true)
	seedRefresh(t, st, "a", "fam-mine", 1, 20, 600)
	seedRefresh(t, st, "b", "fam-mine", 1, 25, 600)

	srv := NewServer(st, fixedClockTokens(200))
	if _, is := callRevokeMySession(t, srv, auth.Claims{UserID: 1}, true, "fam-mine").(api.RevokeMySession204Response); !is {
		t.Fatal("expected 204 revoking own family")
	}

	// Both tokens are now revoked, so the session no longer lists.
	sessions, err := st.SessionsForAccount(context.Background(), 1, 200)
	if err != nil {
		t.Fatalf("SessionsForAccount: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("session should be gone after revoke: %+v", sessions)
	}

	// Idempotent while the rows persist.
	if _, is := callRevokeMySession(t, srv, auth.Claims{UserID: 1}, true, "fam-mine").(api.RevokeMySession204Response); !is {
		t.Fatal("expected 204 on repeat revoke")
	}
}

func TestRevokeMySessionOtherAccountIs404(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "me@example.com", false, true)
	insertAccount(t, pool, 2, "other@example.com", false, true)
	seedRefresh(t, st, "theirs", "fam-theirs", 2, 20, 600)

	srv := NewServer(st, fixedClockTokens(200))
	if _, is := callRevokeMySession(t, srv, auth.Claims{UserID: 1}, true, "fam-theirs").(api.RevokeMySession404JSONResponse); !is {
		t.Fatal("expected 404 revoking another account's family")
	}
	// The bystander's token is untouched.
	if got, _ := st.RefreshTokenByJTI(context.Background(), "theirs"); got.Revoked {
		t.Fatal("another account's token must not be revoked")
	}
}

func TestRevokeMySessionUnknownIs404(t *testing.T) {
	st, pool := seedStore(t)
	insertAccount(t, pool, 1, "me@example.com", false, true)
	srv := NewServer(st, fixedClockTokens(0))
	if _, is := callRevokeMySession(t, srv, auth.Claims{UserID: 1}, true, "fam-nope").(api.RevokeMySession404JSONResponse); !is {
		t.Fatal("expected 404 revoking an unknown family")
	}
}

func TestRevokeMySessionNoClaimsIs401(t *testing.T) {
	st, _ := seedStore(t)
	srv := NewServer(st, fixedClockTokens(0))
	if _, is := callRevokeMySession(t, srv, auth.Claims{}, false, "fam-x").(api.RevokeMySession401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

// TestSessionsHTTPRoundTrip drives the real router: a login creates one session,
// which lists over GET /me/sessions and then revokes over DELETE, all with the
// bearer-auth middleware in place.
func TestSessionsHTTPRoundTrip(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 5, "user@example.com", "s3cret-pass", true)
	tokens := testTokens()
	srv := NewServer(st, tokens)
	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)

	// Secured route: no bearer token is a 401 before the handler runs.
	if rr := doJSON(t, handler, http.MethodGet, "/me/sessions", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /me/sessions without token: got %d, want 401", rr.Code)
	}

	tok := loginTokens(t, srv)

	rr := doJSON(t, handler, http.MethodGet, "/me/sessions", tok.AccessToken, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /me/sessions: got %d, want 200 (body %q)", rr.Code, rr.Body.String())
	}
	var listed api.ListMySessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Sessions) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(listed.Sessions), listed.Sessions)
	}
	family := listed.Sessions[0].FamilyId

	if rr := doJSON(t, handler, http.MethodDelete, "/me/sessions/"+family, tok.AccessToken, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE /me/sessions/%s: got %d, want 204 (body %q)", family, rr.Code, rr.Body.String())
	}

	// The revoked session no longer refreshes, and the list is now empty.
	if rr := refreshHTTP(t, handler, tok.RefreshToken); rr.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after session revoke: got %d, want 401", rr.Code)
	}
	rr = doJSON(t, handler, http.MethodGet, "/me/sessions", tok.AccessToken, nil)
	var afterList api.ListMySessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &afterList); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(afterList.Sessions) != 0 {
		t.Fatalf("got %d sessions after revoke, want 0", len(afterList.Sessions))
	}

	// Revoking an unknown family is a 404.
	if rr := doJSON(t, handler, http.MethodDelete, "/me/sessions/deadbeef", tok.AccessToken, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown family: got %d, want 404", rr.Code)
	}
}

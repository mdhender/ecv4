package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/httputil"
)

// impersonationFixture builds the world for the impersonation tests. Accounts:
// 1 is an admin, 2 an active non-admin (the impersonation target), 3 another
// admin, 4 an inactive non-admin. ALPHA (10) is recruiting and visible, with the
// target as its GM; BETA (20) is visible but the target is not a member (used to
// show god-mode is off under impersonation).
func impersonationFixture(t *testing.T) (*Server, *sqlitemigration.Pool, *auth.TokenService) {
	t.Helper()
	st, pool := seedStore(t)
	tokens := testTokens()

	insertAccount(t, pool, 1, "admin@example.com", true, true)
	insertAccount(t, pool, 2, "player@example.com", false, true)
	insertAccount(t, pool, 3, "other-admin@example.com", true, true)
	insertAccount(t, pool, 4, "inactive@example.com", false, false)

	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(10, 'ALPHA', 'Alpha', 'recruiting', 1);")
	seedExec(t, pool, "INSERT INTO games(id, code, name, status, is_active) VALUES(20, 'BETA', 'Beta', 'active', 1);")
	seedExec(t, pool, "INSERT INTO game_account_role(id, game_id, account_id, handle, is_gm, is_active) VALUES(100, 10, 2, 'Overlord', 1, 1);")

	return NewServer(st, tokens), pool, tokens
}

// callCreateImpersonation invokes the mint handler directly with claims in the
// context, bypassing the HTTP/auth layer.
func callCreateImpersonation(t *testing.T, srv *Server, claims auth.Claims, withClaims bool, body *api.ImpersonationRequest) api.CreateImpersonationResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := srv.CreateImpersonation(ctx, api.CreateImpersonationRequestObject{Body: body})
	if err != nil {
		t.Fatalf("CreateImpersonation returned error: %v", err)
	}
	return resp
}

func TestCreateImpersonationMintsTokenForTarget(t *testing.T) {
	srv, _, tokens := impersonationFixture(t)

	resp := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 2})
	ok, is := resp.(api.CreateImpersonation200JSONResponse)
	if !is {
		t.Fatalf("got %T, want CreateImpersonation200JSONResponse", resp)
	}
	if ok.Subject.AccountId != 2 || string(ok.Subject.Email) != "player@example.com" {
		t.Fatalf("subject = %+v, want account 2 player@example.com", ok.Subject)
	}
	if ok.Actor.AccountId != 1 {
		t.Fatalf("actor = %+v, want admin account 1", ok.Actor)
	}
	if ok.ExpiresInSeconds != 900 {
		t.Fatalf("expiresInSeconds = %d, want 900 (15m)", ok.ExpiresInSeconds)
	}
	if ok.TokenType != api.ImpersonationResponseTokenTypeBearer {
		t.Fatalf("tokenType = %q, want Bearer", ok.TokenType)
	}

	// The minted token bears the target as subject and the admin as actor.
	claims, err := tokens.Verify(ok.Token)
	if err != nil {
		t.Fatalf("Verify minted token: %v", err)
	}
	if claims.UserID != 2 || claims.Actor != 1 || !claims.Impersonated() {
		t.Fatalf("claims = %+v, want UserID=2 Actor=1 impersonated", claims)
	}
}

// TestImpersonationTokenActsAsTargetNotAdmin proves the effective identity is the
// target: the minted token reaches a game the target is a member of, but not one
// the target was never assigned to — even though the minting admin is god-mode.
func TestImpersonationTokenActsAsTargetNotAdmin(t *testing.T) {
	srv, _, tokens := impersonationFixture(t)

	resp := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 2})
	ok := resp.(api.CreateImpersonation200JSONResponse)
	claims, err := tokens.Verify(ok.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	impCtx := auth.WithClaims(context.Background(), claims)

	// ALPHA: the target is a GM, so the impersonated request sees it.
	if got, err := srv.GetGame(impCtx, api.GetGameRequestObject{GameId: 10}); err != nil {
		t.Fatalf("GetGame(ALPHA): %v", err)
	} else if _, is := got.(api.GetGame200JSONResponse); !is {
		t.Fatalf("GetGame(ALPHA) as target = %T, want 200 (target is a member)", got)
	}

	// BETA: the target is not a member, so the impersonated request is a 404 —
	// god-mode is off, unlike the admin's own token below.
	if got, err := srv.GetGame(impCtx, api.GetGameRequestObject{GameId: 20}); err != nil {
		t.Fatalf("GetGame(BETA): %v", err)
	} else if _, is := got.(api.GetGame404JSONResponse); !is {
		t.Fatalf("GetGame(BETA) as target = %T, want 404 (target never assigned)", got)
	}

	// The admin's own identity, by contrast, sees BETA (god-mode).
	adminCtx := auth.WithClaims(context.Background(), auth.Claims{UserID: 1})
	if got, err := srv.GetGame(adminCtx, api.GetGameRequestObject{GameId: 20}); err != nil {
		t.Fatalf("GetGame(BETA) as admin: %v", err)
	} else if _, is := got.(api.GetGame200JSONResponse); !is {
		t.Fatalf("GetGame(BETA) as admin = %T, want 200 (god-mode)", got)
	}
}

func TestCreateImpersonationNonAdminIs403(t *testing.T) {
	srv, _, _ := impersonationFixture(t)
	// Account 2 is a non-admin player: it may not mint impersonation tokens.
	if _, is := callCreateImpersonation(t, srv, auth.Claims{UserID: 2}, true, &api.ImpersonationRequest{AccountId: 4}).(api.CreateImpersonation403JSONResponse); !is {
		t.Fatal("expected 403 for a non-admin caller")
	}
}

func TestCreateImpersonationNoClaimsIs401(t *testing.T) {
	srv, _, _ := impersonationFixture(t)
	if _, is := callCreateImpersonation(t, srv, auth.Claims{}, false, &api.ImpersonationRequest{AccountId: 2}).(api.CreateImpersonation401JSONResponse); !is {
		t.Fatal("expected 401 when claims are absent")
	}
}

func TestCreateImpersonationMissingBodyIs400(t *testing.T) {
	srv, _, _ := impersonationFixture(t)
	if _, is := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, nil).(api.CreateImpersonation400JSONResponse); !is {
		t.Fatal("expected 400 for a missing body")
	}
	if _, is := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 0}).(api.CreateImpersonation400JSONResponse); !is {
		t.Fatal("expected 400 for a missing/zero accountId")
	}
}

func TestCreateImpersonationSelfIs409(t *testing.T) {
	srv, _, _ := impersonationFixture(t)
	if _, is := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 1}).(api.CreateImpersonation409JSONResponse); !is {
		t.Fatal("expected 409 when impersonating your own account")
	}
}

func TestCreateImpersonationAdminTargetIs409(t *testing.T) {
	srv, _, _ := impersonationFixture(t)
	// Account 3 is another admin: off-limits (would hand out god-mode).
	if _, is := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 3}).(api.CreateImpersonation409JSONResponse); !is {
		t.Fatal("expected 409 when impersonating another admin")
	}
}

func TestCreateImpersonationInactiveTargetIs409(t *testing.T) {
	srv, _, _ := impersonationFixture(t)
	// Account 4 is inactive: impersonating it would not reproduce a usable account.
	if _, is := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 4}).(api.CreateImpersonation409JSONResponse); !is {
		t.Fatal("expected 409 when impersonating an inactive account")
	}
}

func TestCreateImpersonationUnknownTargetIs404(t *testing.T) {
	srv, _, _ := impersonationFixture(t)
	if _, is := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 999}).(api.CreateImpersonation404JSONResponse); !is {
		t.Fatal("expected 404 for an unknown target account")
	}
}

// TestImpersonationResponseCarriesSubjectHeader drives the full HTTP stack: an
// admin mints a token, then a request bearing it carries the Impersonated-Subject
// header naming the target, and reads as the target rather than the admin.
func TestImpersonationResponseCarriesSubjectHeader(t *testing.T) {
	srv, pool, tokens := impersonationFixture(t)
	_ = pool
	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)

	adminAccess, _, err := tokens.IssueAccess(1, "admin@example.com", []string{"admin"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}

	mint := doJSON(t, handler, http.MethodPost, "/admin/impersonation", adminAccess, map[string]any{"accountId": 2})
	if mint.Code != http.StatusOK {
		t.Fatalf("mint: got %d, want 200 (body %q)", mint.Code, mint.Body.String())
	}
	var minted api.ImpersonationResponse
	if err := json.Unmarshal(mint.Body.Bytes(), &minted); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}

	// A request bearing the impersonation token: the header names the target and
	// the body is the target's identity (account 2), not the admin's.
	me := doJSON(t, handler, http.MethodGet, "/me", minted.Token, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("GET /me: got %d, want 200 (body %q)", me.Code, me.Body.String())
	}
	if got := me.Header().Get(httputil.ImpersonatedSubjectHeader); got != "2" {
		t.Fatalf("%s = %q, want \"2\"", httputil.ImpersonatedSubjectHeader, got)
	}
	var meResp api.MeResponse
	if err := json.Unmarshal(me.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	if meResp.User.Id != 2 || meResp.User.Username != "player@example.com" {
		t.Fatalf("/me user = %+v, want the impersonated target (account 2)", meResp.User)
	}

	// An ordinary (non-impersonated) request must not carry the header.
	adminMe := doJSON(t, handler, http.MethodGet, "/me", adminAccess, nil)
	if got := adminMe.Header().Get(httputil.ImpersonatedSubjectHeader); got != "" {
		t.Fatalf("%s = %q on an ordinary request, want empty", httputil.ImpersonatedSubjectHeader, got)
	}
}

// TestImpersonationTokenRejectedByRefresh confirms the token is non-refreshable
// through the real /auth/refresh route.
func TestImpersonationTokenRejectedByRefresh(t *testing.T) {
	srv, _, tokens := impersonationFixture(t)
	handler := NewHTTPHandler(srv, http.NewServeMux(), tokens)

	resp := callCreateImpersonation(t, srv, auth.Claims{UserID: 1}, true, &api.ImpersonationRequest{AccountId: 2})
	ok := resp.(api.CreateImpersonation200JSONResponse)

	refresh := doJSON(t, handler, http.MethodPost, "/auth/refresh", "", map[string]any{"refreshToken": ok.Token})
	if refresh.Code != http.StatusUnauthorized {
		t.Fatalf("/auth/refresh with an impersonation token: got %d, want 401", refresh.Code)
	}
}

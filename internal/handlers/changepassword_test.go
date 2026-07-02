package handlers

import (
	"context"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/mdhender/ecv4/internal/api"
	"github.com/mdhender/ecv4/internal/auth"
	"github.com/mdhender/ecv4/internal/store"
)

// callChangeMyPassword invokes the handler directly with claims placed in the
// context, bypassing the HTTP/auth layer to exercise ChangeMyPassword against
// the store.
func callChangeMyPassword(t *testing.T, st *store.Store, claims auth.Claims, withClaims bool, body *api.ChangePasswordRequest) api.ChangeMyPasswordResponseObject {
	t.Helper()
	ctx := context.Background()
	if withClaims {
		ctx = auth.WithClaims(ctx, claims)
	}
	resp, err := NewServer(st, nil).ChangeMyPassword(ctx, api.ChangeMyPasswordRequestObject{Body: body})
	if err != nil {
		t.Fatalf("ChangeMyPassword returned error: %v", err)
	}
	return resp
}

// storedHash returns the account's current bcrypt hash straight from the store.
func storedHash(t *testing.T, st *store.Store, email string) string {
	t.Helper()
	_, hash, err := st.Credentials(context.Background(), email)
	if err != nil {
		t.Fatalf("Credentials(%q): %v", email, err)
	}
	return hash
}

func TestChangeMyPasswordSuccess(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 42, "player@example.com", "old-password", true)

	resp := callChangeMyPassword(t, st, auth.Claims{UserID: 42}, true, &api.ChangePasswordRequest{
		CurrentPassword: "old-password",
		NewPassword:     "brand-new-password",
	})
	if _, is := resp.(api.ChangeMyPassword204Response); !is {
		t.Fatalf("got %T, want ChangeMyPassword204Response", resp)
	}

	// The new password must verify, and the old one must not.
	hash := storedHash(t, st, "player@example.com")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("brand-new-password")); err != nil {
		t.Fatalf("new password does not verify against stored hash: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("old-password")) == nil {
		t.Fatal("old password still verifies after change")
	}
}

func TestChangeMyPasswordRevokesSessions(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 42, "player@example.com", "old-password", true)

	// Two live sessions for the account and one for a bystander that must survive.
	ctx := context.Background()
	if err := st.CreateRefreshToken(ctx, "jti-a", "fam-a", 42, 0, 1<<40); err != nil {
		t.Fatalf("create refresh a: %v", err)
	}
	if err := st.CreateRefreshToken(ctx, "jti-b", "fam-b", 42, 0, 1<<40); err != nil {
		t.Fatalf("create refresh b: %v", err)
	}
	insertAccountWithPassword(t, pool, 7, "other@example.com", "other-password", true)
	if err := st.CreateRefreshToken(ctx, "jti-other", "fam-other", 7, 0, 1<<40); err != nil {
		t.Fatalf("create refresh other: %v", err)
	}

	resp := callChangeMyPassword(t, st, auth.Claims{UserID: 42}, true, &api.ChangePasswordRequest{
		CurrentPassword: "old-password",
		NewPassword:     "brand-new-password",
	})
	if _, is := resp.(api.ChangeMyPassword204Response); !is {
		t.Fatalf("got %T, want 204", resp)
	}

	for _, jti := range []string{"jti-a", "jti-b"} {
		rec, err := st.RefreshTokenByJTI(ctx, jti)
		if err != nil {
			t.Fatalf("RefreshTokenByJTI(%q): %v", jti, err)
		}
		if !rec.Revoked {
			t.Fatalf("session %q was not revoked", jti)
		}
	}
	other, err := st.RefreshTokenByJTI(ctx, "jti-other")
	if err != nil {
		t.Fatalf("RefreshTokenByJTI(jti-other): %v", err)
	}
	if other.Revoked {
		t.Fatal("another account's session was revoked")
	}
}

func TestChangeMyPasswordWrongCurrentIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 42, "player@example.com", "old-password", true)

	resp := callChangeMyPassword(t, st, auth.Claims{UserID: 42}, true, &api.ChangePasswordRequest{
		CurrentPassword: "not-the-password",
		NewPassword:     "brand-new-password",
	})
	if _, is := resp.(api.ChangeMyPassword401JSONResponse); !is {
		t.Fatalf("got %T, want 401 for wrong current password", resp)
	}

	// The stored password must be unchanged.
	hash := storedHash(t, st, "player@example.com")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("old-password")); err != nil {
		t.Fatal("password changed despite wrong current password")
	}
}

func TestChangeMyPasswordShortNewIs400(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 42, "player@example.com", "old-password", true)

	resp := callChangeMyPassword(t, st, auth.Claims{UserID: 42}, true, &api.ChangePasswordRequest{
		CurrentPassword: "old-password",
		NewPassword:     "short", // 5 chars, below MinSecretLength
	})
	if _, is := resp.(api.ChangeMyPassword400JSONResponse); !is {
		t.Fatalf("got %T, want 400 for too-short new password", resp)
	}

	// The change must not have been applied.
	hash := storedHash(t, st, "player@example.com")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("old-password")); err != nil {
		t.Fatal("password changed despite too-short new password")
	}
}

func TestChangeMyPasswordNoClaimsIs401(t *testing.T) {
	st, _ := seedStore(t)
	resp := callChangeMyPassword(t, st, auth.Claims{}, false, &api.ChangePasswordRequest{
		CurrentPassword: "old-password",
		NewPassword:     "brand-new-password",
	})
	if _, is := resp.(api.ChangeMyPassword401JSONResponse); !is {
		t.Fatalf("got %T, want 401 when claims are absent", resp)
	}
}

func TestChangeMyPasswordMissingBodyIs400(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 42, "player@example.com", "old-password", true)

	resp := callChangeMyPassword(t, st, auth.Claims{UserID: 42}, true, nil)
	if _, is := resp.(api.ChangeMyPassword400JSONResponse); !is {
		t.Fatalf("got %T, want 400 for missing body", resp)
	}
}

func TestChangeMyPasswordInactiveAccountIs401(t *testing.T) {
	st, pool := seedStore(t)
	insertAccountWithPassword(t, pool, 7, "gone@example.com", "old-password", false)

	resp := callChangeMyPassword(t, st, auth.Claims{UserID: 7}, true, &api.ChangePasswordRequest{
		CurrentPassword: "old-password",
		NewPassword:     "brand-new-password",
	})
	if _, is := resp.(api.ChangeMyPassword401JSONResponse); !is {
		t.Fatalf("got %T, want 401 for inactive account", resp)
	}
}

package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef") // 32 bytes

func TestAccessTokenRoundTrip(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, time.Hour)

	token, exp, err := ts.IssueAccess(42, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	if !exp.After(time.Now()) {
		t.Fatalf("expiry %v is not in the future", exp)
	}

	claims, err := ts.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.UserID != 42 || claims.Username != "player@example.com" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "player" {
		t.Fatalf("roles = %v, want [player]", claims.Roles)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, time.Hour)
	token, _, _ := ts.IssueAccess(1, "a@b.com", nil)

	// Tamper by flipping a bit in the decoded signature bytes, then re-encoding.
	// Flipping the last base64url *character* is unsound: a 32-byte HS256 signature
	// encodes to 43 no-pad characters whose final character carries redundant
	// low-order bits, so a different final character can decode to the same bytes
	// and still verify (the source of a historical flake — see issue #65).
	dot := strings.LastIndexByte(token, '.')
	sig, err := base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	sig[0] ^= 0x01
	bad := token[:dot+1] + base64.RawURLEncoding.EncodeToString(sig)
	if _, err := ts.Verify(bad); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	issuer := NewTokenService(testSecret, time.Minute, time.Hour)
	token, _, _ := issuer.IssueAccess(1, "a@b.com", nil)

	other := NewTokenService([]byte("XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"), time.Minute, time.Hour)
	if _, err := other.Verify(token); err == nil {
		t.Fatal("expected token signed with a different secret to be rejected")
	}
}

func TestVerifyRejectsRefreshTokenAsAccess(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, time.Hour)
	refresh, _, err := ts.IssueRefresh(1, "jti-1", "fam-1")
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}
	// A refresh token carries a different audience and must not verify as an
	// access token.
	if _, err := ts.Verify(refresh); err == nil {
		t.Fatal("expected refresh token to be rejected by access-token Verify")
	}
}

func TestVerifyRefreshRoundTrip(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, time.Hour)
	refresh, exp, err := ts.IssueRefresh(42, "jti-abc", "fam-xyz")
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}
	if !exp.After(time.Now()) {
		t.Fatalf("expiry %v is not in the future", exp)
	}

	claims, err := ts.VerifyRefresh(refresh)
	if err != nil {
		t.Fatalf("VerifyRefresh: %v", err)
	}
	if claims.UserID != 42 {
		t.Fatalf("UserID = %d, want 42", claims.UserID)
	}
	if claims.JTI != "jti-abc" || claims.Family != "fam-xyz" {
		t.Fatalf("jti/family = %q/%q, want jti-abc/fam-xyz", claims.JTI, claims.Family)
	}
}

func TestVerifyRefreshRejectsAccessToken(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, time.Hour)
	access, _, err := ts.IssueAccess(1, "a@b.com", nil)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	// An access token carries the access audience and must not verify as a
	// refresh token.
	if _, err := ts.VerifyRefresh(access); err == nil {
		t.Fatal("expected access token to be rejected by VerifyRefresh")
	}
}

func TestVerifyRefreshRejectsExpiredToken(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := base
	ts := NewTokenService(testSecret, time.Minute, time.Hour, WithClock(func() time.Time { return clock }))

	refresh, _, _ := ts.IssueRefresh(1, "jti", "fam")

	clock = base.Add(2 * time.Hour) // past the 1-hour refresh TTL
	if _, err := ts.VerifyRefresh(refresh); err == nil {
		t.Fatal("expected expired refresh token to be rejected")
	}
}

func TestNewTokenIDUnique(t *testing.T) {
	a, err := NewTokenID()
	if err != nil {
		t.Fatalf("NewTokenID: %v", err)
	}
	b, err := NewTokenID()
	if err != nil {
		t.Fatalf("NewTokenID: %v", err)
	}
	if a == b {
		t.Fatal("NewTokenID returned the same id twice")
	}
	if len(a) != 32 {
		t.Fatalf("token id length = %d, want 32", len(a))
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := base
	ts := NewTokenService(testSecret, time.Minute, time.Hour, WithClock(func() time.Time { return clock }))

	token, _, _ := ts.IssueAccess(1, "a@b.com", nil)

	clock = base.Add(2 * time.Minute) // past the 1-minute access TTL
	if _, err := ts.Verify(token); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

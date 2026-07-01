package auth

import (
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

	// Flip the last character of the signature.
	bad := token[:len(token)-1] + string(flip(token[len(token)-1]))
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
	refresh, _, err := ts.IssueRefresh(1)
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}
	// A refresh token carries a different audience and must not verify as an
	// access token.
	if _, err := ts.Verify(refresh); err == nil {
		t.Fatal("expected refresh token to be rejected by access-token Verify")
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

func flip(b byte) byte {
	if b == 'a' {
		return 'b'
	}
	return 'a'
}

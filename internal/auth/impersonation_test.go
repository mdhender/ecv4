package auth

import (
	"testing"
	"time"
)

// TestIssueImpersonationRoundTrip mints an impersonation token and verifies it
// carries the target's identity plus the admin actor, with the fixed 15-minute
// lifetime.
func TestIssueImpersonationRoundTrip(t *testing.T) {
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	ts := NewTokenService(testSecret, time.Minute, time.Hour, WithClock(func() time.Time { return base }))

	token, exp, err := ts.IssueImpersonation(42, "player@example.com", []string{"player"}, 7)
	if err != nil {
		t.Fatalf("IssueImpersonation: %v", err)
	}
	if want := base.Add(15 * time.Minute); !exp.Equal(want) {
		t.Fatalf("expiry = %v, want %v (15m after issue, independent of access TTL)", exp, want)
	}
	if ts.ImpersonationTTL() != 15*time.Minute {
		t.Fatalf("ImpersonationTTL = %v, want 15m", ts.ImpersonationTTL())
	}

	claims, err := ts.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.UserID != 42 {
		t.Fatalf("UserID = %d, want 42 (the effective/target identity)", claims.UserID)
	}
	if claims.Actor != 7 {
		t.Fatalf("Actor = %d, want 7 (the impersonating admin)", claims.Actor)
	}
	if !claims.Impersonated() {
		t.Fatal("Impersonated() = false, want true")
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "player" {
		t.Fatalf("Roles = %v, want [player] (target's roles, never admin)", claims.Roles)
	}
}

// TestImpersonationTokenExpiresAt15Minutes pins the lifetime: the token verifies
// just before 15 minutes and is rejected just after.
func TestImpersonationTokenExpiresAt15Minutes(t *testing.T) {
	clock := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	ts := NewTokenService(testSecret, time.Minute, time.Hour, WithClock(func() time.Time { return clock }))

	token, _, err := ts.IssueImpersonation(42, "player@example.com", []string{"player"}, 7)
	if err != nil {
		t.Fatalf("IssueImpersonation: %v", err)
	}

	clock = clock.Add(14 * time.Minute)
	if _, err := ts.Verify(token); err != nil {
		t.Fatalf("Verify at +14m: %v, want success", err)
	}

	clock = clock.Add(2 * time.Minute) // now +16m
	if _, err := ts.Verify(token); err == nil {
		t.Fatal("Verify at +16m succeeded, want expired-token rejection")
	}
}

// TestImpersonationTokenIsNotRefreshable proves an impersonation token cannot be
// traded for a session: it uses the access audience, so the refresh path rejects
// it. Combined with the mint endpoint issuing no refresh token, impersonation is
// access-only.
func TestImpersonationTokenIsNotRefreshable(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, time.Hour)
	token, _, err := ts.IssueImpersonation(42, "player@example.com", []string{"player"}, 7)
	if err != nil {
		t.Fatalf("IssueImpersonation: %v", err)
	}
	if _, err := ts.VerifyRefresh(token); err == nil {
		t.Fatal("VerifyRefresh accepted an impersonation token; it must be non-refreshable")
	}
}

// TestOrdinaryAccessTokenHasNoActor guards the marker: a normal access token is
// not impersonation.
func TestOrdinaryAccessTokenHasNoActor(t *testing.T) {
	ts := NewTokenService(testSecret, time.Minute, time.Hour)
	token, _, err := ts.IssueAccess(42, "player@example.com", []string{"player"})
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	claims, err := ts.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Actor != 0 || claims.Impersonated() {
		t.Fatalf("ordinary token looks impersonated: Actor=%d", claims.Actor)
	}
}

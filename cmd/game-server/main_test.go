package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// quietLogger discards log output so tests don't spam stdout.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestResolveJWTSecretConfigured(t *testing.T) {
	configured := strings.Repeat("k", 32)
	for _, env := range []string{"development", "production", ""} {
		secret, err := resolveJWTSecret(env, configured, quietLogger())
		if err != nil {
			t.Fatalf("env=%q: unexpected error: %v", env, err)
		}
		if string(secret) != configured {
			t.Fatalf("env=%q: got %q, want configured secret", env, secret)
		}
	}
}

func TestResolveJWTSecretTooShort(t *testing.T) {
	if _, err := resolveJWTSecret("production", strings.Repeat("k", 31), quietLogger()); err == nil {
		t.Fatal("expected error for secret shorter than 32 bytes")
	}
}

func TestResolveJWTSecretProductionRequiresSecret(t *testing.T) {
	if _, err := resolveJWTSecret("production", "", quietLogger()); err == nil {
		t.Fatal("expected error when ECV4_ENV=production and no secret is configured")
	}
}

func TestResolveJWTSecretNonProductionGeneratesEphemeral(t *testing.T) {
	for _, env := range []string{"development", "staging", ""} {
		secret, err := resolveJWTSecret(env, "", quietLogger())
		if err != nil {
			t.Fatalf("env=%q: unexpected error: %v", env, err)
		}
		if len(secret) != 32 {
			t.Fatalf("env=%q: got %d-byte ephemeral secret, want 32", env, len(secret))
		}
	}
}

func TestResolveJWTSecretEphemeralIsRandom(t *testing.T) {
	a, err := resolveJWTSecret("development", "", quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	b, err := resolveJWTSecret("development", "", quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatal("expected two ephemeral secrets to differ")
	}
}

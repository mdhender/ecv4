package auth

import "testing"

func ptrU64(v uint64) *uint64 { return &v }

// TestSplitMix64KnownVector checks the implementation against a published
// SplitMix64 output for state 0.
func TestSplitMix64KnownVector(t *testing.T) {
	const want uint64 = 0xe220a8397b1dcdaf
	if got := splitMix64(0); got != want {
		t.Fatalf("splitMix64(0) = %#x, want %#x", got, want)
	}
}

// TestGenerateSecretSeededIsReproducible pins the seed -> passphrase mapping.
// This is the "known random" contract tests rely on; the value changes only if
// the seeding logic or the phrases wordlist changes.
func TestGenerateSecretSeededIsReproducible(t *testing.T) {
	const want = "hut.scout.kite.foil.said.haven"
	for i := 0; i < 2; i++ {
		got, err := GenerateSecret(ptrU64(42))
		if err != nil {
			t.Fatalf("GenerateSecret: %v", err)
		}
		if got != want {
			t.Fatalf("GenerateSecret(42) = %q, want %q", got, want)
		}
	}
}

func TestGenerateSecretDifferentSeedsDiffer(t *testing.T) {
	a, _ := GenerateSecret(ptrU64(42))
	b, _ := GenerateSecret(ptrU64(43))
	if a == b {
		t.Fatalf("seeds 42 and 43 produced the same passphrase %q", a)
	}
}

func TestGenerateSecretUnseededSucceeds(t *testing.T) {
	if _, err := GenerateSecret(nil); err != nil {
		t.Fatalf("GenerateSecret(nil): %v", err)
	}
}

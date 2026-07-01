package auth

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand/v2"

	"github.com/mdhender/ecv4/internal/phrases"
)

// GenerateSecret returns a random six-word passphrase suitable for an account
// secret (the EFF short list carries roughly 10.3 bits per word, so ~62 bits
// total). Callers still hash the result with HashSecret before storing it.
//
// When seed is nil the two PCG seeds come from crypto/rand, so the result is
// unpredictable. When seed is non-nil the PCG is seeded with
// (*seed, splitMix64(*seed)), giving a reproducible "known random" passphrase
// for tests; splitMix64 derives an independent second seed so the PCG's two
// 64-bit lanes are not identical.
func GenerateSecret(seed *uint64) (string, error) {
	var s1, s2 uint64
	if seed != nil {
		s1, s2 = *seed, splitMix64(*seed)
	} else {
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("generate secret: %w", err)
		}
		s1 = binary.LittleEndian.Uint64(buf[0:8])
		s2 = binary.LittleEndian.Uint64(buf[8:16])
	}
	r := mrand.New(mrand.NewPCG(s1, s2))
	return phrases.Generate(r, 6), nil
}

// splitMix64 is the canonical SplitMix64 step: it advances state x by the
// golden-ratio increment and applies the finalizing avalanche mix. It derives a
// second PCG seed from a single seed value.
func splitMix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

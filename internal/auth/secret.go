package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MinSecretLength is the minimum length of an account secret. Secrets are never
// empty and cannot be deleted, so both account creation and update reject
// anything shorter.
const MinSecretLength = 8

// ErrSecretTooShort is returned by HashSecret when the plaintext is shorter than
// MinSecretLength.
var ErrSecretTooShort = errors.New("secret is too short")

// HashSecret validates a plaintext secret against the secret policy and returns
// its bcrypt hash. The CLI and the API both call it, so the policy lives in one
// place. It returns ErrSecretTooShort for a secret below MinSecretLength.
func HashSecret(plaintext string) (string, error) {
	if len(plaintext) < MinSecretLength {
		return "", fmt.Errorf("%w: need at least %d characters", ErrSecretTooShort, MinSecretLength)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash secret: %w", err)
	}
	return string(hash), nil
}

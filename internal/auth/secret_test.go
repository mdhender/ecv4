package auth

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashSecretRejectsShort(t *testing.T) {
	if _, err := HashSecret("1234567"); !errors.Is(err, ErrSecretTooShort) { // 7 chars
		t.Fatalf("HashSecret(7 chars) = %v, want ErrSecretTooShort", err)
	}
}

func TestHashSecretAcceptsBoundary(t *testing.T) {
	if _, err := HashSecret("12345678"); err != nil { // exactly 8
		t.Fatalf("HashSecret(8 chars): %v", err)
	}
}

func TestHashSecretVerifies(t *testing.T) {
	hash, err := HashSecret("correct-horse")
	if err != nil {
		t.Fatalf("HashSecret: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct-horse")); err != nil {
		t.Fatalf("hash does not verify the original secret: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong-secret")) == nil {
		t.Fatal("hash unexpectedly verified a wrong secret")
	}
}

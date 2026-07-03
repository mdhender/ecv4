// Package gamerules holds the canonical game-identifier validation rules — the
// game code and the roster handle — shared by the HTTP handlers and the offline
// CLI verbs. Both layers validate in Go so a bad value is a clear 400 (or CLI
// error) instead of an opaque database CHECK failure; keeping the pattern and its
// error here means the rule has one source of truth rather than a copy per layer.
// The matching games.code / game_account_role.handle CHECKs the migrations apply
// remain the backstop.
package gamerules

import (
	"regexp"

	"github.com/mdhender/ecv4/internal/cerrs"
)

// codePattern mirrors the games.code CHECK (migration 0006): two or more
// uppercase ASCII letters and nothing else.
var codePattern = regexp.MustCompile(`^[A-Z][A-Z]+$`)

// handlePattern mirrors the game_account_role.handle CHECK (migration 0004): two
// or more characters, starting with a letter, using only letters, digits, '.',
// '_' or '-'.
var handlePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]+$`)

// ErrInvalidCode and ErrInvalidHandle are the constant errors returned when a
// value fails its rule. The message doubles as the human-readable description:
// the CLI verbs return the error directly, and the HTTP handlers put its .Error()
// text in the 400 envelope, so the rule and its explanation cannot drift apart.
const (
	ErrInvalidCode   = cerrs.Error("code must be two or more uppercase ASCII letters (A-Z)")
	ErrInvalidHandle = cerrs.Error("handle must be two or more characters, start with a letter, and use only letters, digits, '.', '_' or '-'")
)

// ValidCode reports whether code satisfies the game-code rule.
func ValidCode(code string) bool { return codePattern.MatchString(code) }

// ValidHandle reports whether handle satisfies the roster-handle rule.
func ValidHandle(handle string) bool { return handlePattern.MatchString(handle) }

package gamerules

import "testing"

func TestValidCode(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{"AB", true},
		{"ALPHA", true},
		{"ZZZZ", true},
		{"A", false},    // single letter is too short
		{"", false},     // empty
		{"Ab", false},   // lowercase not allowed
		{"AB1", false},  // digits not allowed
		{"AB-C", false}, // punctuation not allowed
		{"ab", false},   // all lowercase
		{" AB", false},  // leading space (callers trim before validating)
	}
	for _, c := range cases {
		if got := ValidCode(c.code); got != c.want {
			t.Errorf("ValidCode(%q) = %v, want %v", c.code, got, c.want)
		}
	}
}

func TestValidHandle(t *testing.T) {
	cases := []struct {
		handle string
		want   bool
	}{
		{"ab", true},
		{"Alice", true},
		{"player_1", true}, // the 'player_' reservation is a handler concern, not this rule
		{"a.b-c_d", true},
		{"x9", true},
		{"a", false},   // single character is too short
		{"", false},    // empty
		{"1ab", false}, // must start with a letter
		{"_ab", false}, // must start with a letter
		{".ab", false}, // must start with a letter
		{"a b", false}, // space not allowed
		{"a@b", false}, // '@' not allowed
	}
	for _, c := range cases {
		if got := ValidHandle(c.handle); got != c.want {
			t.Errorf("ValidHandle(%q) = %v, want %v", c.handle, got, c.want)
		}
	}
}

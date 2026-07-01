package dotenv

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadRejectsMissingEnv(t *testing.T) {
	if err := Load(""); !errors.Is(err, ErrMissingEnvironment) {
		t.Fatalf("Load(%q) = %v, want ErrMissingEnvironment", "", err)
	}
}

func TestLoadRejectsUnknownEnv(t *testing.T) {
	if err := Load("staging"); !errors.Is(err, ErrUnknownEnvironment) {
		t.Fatalf("Load(%q) = %v, want ErrUnknownEnvironment", "staging", err)
	}
}

// TestLoadPrecedence confirms that the highest-priority file present wins, since
// godotenv.Load does not overwrite a variable already set by an earlier (higher
// priority) file.
func TestLoadPrecedence(t *testing.T) {
	const key = "DOTENV_TEST_VAR"
	t.Chdir(t.TempDir())
	os.Unsetenv(key)
	t.Cleanup(func() { os.Unsetenv(key) })

	write(t, ".env", key+"=from_env\n")
	write(t, ".env.development", key+"=from_env_dev\n")
	write(t, ".env.local", key+"=from_local\n")
	write(t, ".env.development.local", key+"=from_dev_local\n")

	if err := Load("development"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := os.Getenv(key); got != "from_dev_local" {
		t.Fatalf("%s = %q, want from_dev_local (.env.development.local has highest priority)", key, got)
	}
}

// TestLoadMissingFilesAreSkipped confirms that absent files are not an error and
// a lower-priority file still applies when higher-priority ones are missing.
func TestLoadMissingFilesAreSkipped(t *testing.T) {
	const key = "DOTENV_TEST_VAR2"
	t.Chdir(t.TempDir())
	os.Unsetenv(key)
	t.Cleanup(func() { os.Unsetenv(key) })

	// Only the shared, lowest-priority .env exists.
	write(t, filepath.Clean(".env"), key+"=shared\n")

	if err := Load("test"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := os.Getenv(key); got != "shared" {
		t.Fatalf("%s = %q, want shared", key, got)
	}
}

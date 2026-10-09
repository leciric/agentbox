package hostos

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsHome(t *testing.T) {
	users := t.TempDir()
	old := windowsUsers
	windowsUsers = users
	t.Cleanup(func() { windowsUsers = old })
	t.Setenv("USER", "ana")
	t.Setenv(WindowsHomeEnv, "")

	fakeRelease(t, "6.1.0-generic")
	t.Setenv(WindowsHomeEnv, "/mnt/c/Users/Ana")
	if got := WindowsHome(); got != "" {
		t.Errorf("outside WSL: %q, want none", got)
	}

	fakeRelease(t, "6.6.87.2-microsoft-standard-WSL2")
	if got := WindowsHome(); got != "/mnt/c/Users/Ana" {
		t.Errorf("with the front end's: %q", got)
	}
	t.Setenv(WindowsHomeEnv, "")
	if got := WindowsHome(); got != "" {
		t.Errorf("with no such user's folder: %q, want none", got)
	}
	if err := os.Mkdir(filepath.Join(users, "ana"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := WindowsHome(); got != filepath.Join(users, "ana") {
		t.Errorf("found by the user's name: %q", got)
	}
}

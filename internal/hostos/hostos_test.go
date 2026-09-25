package hostos

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeRelease(t *testing.T, release string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "osrelease")
	if release != "" {
		if err := os.WriteFile(path, []byte(release+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := osrelease
	osrelease = path
	t.Cleanup(func() { osrelease = old })
}

func TestOS(t *testing.T) {
	for _, tc := range []struct {
		name, env, release, want string
		vm                       bool
	}{
		{"a Linux machine", "", "6.12.9-arch1-1", "", false},
		{"no /proc", "", "", "", false},
		{"WSL2, found from its kernel", "", "6.6.87.2-microsoft-standard-WSL2", Windows, true},
		{"a front end that says so", "darwin", "6.12.9-arch1-1", "darwin", true},
		{"what a front end says wins", "darwin", "6.6.87.2-microsoft-standard-WSL2", "darwin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(Env, tc.env)
			fakeRelease(t, tc.release)
			if got := OS(); got != tc.want {
				t.Errorf("OS() = %q, want %q", got, tc.want)
			}
			if got := InVM(); got != tc.vm {
				t.Errorf("InVM() = %v, want %v", got, tc.vm)
			}
		})
	}
}

func TestHome(t *testing.T) {
	fakeRelease(t, "6.12.9-arch1-1")
	t.Setenv(HomeEnv, "/Users/alice")
	t.Setenv(Env, "")
	if got := Home(); got != "" {
		t.Errorf("outside a VM, Home() = %q", got)
	}
	t.Setenv(Env, "darwin")
	if got := Home(); got != "/Users/alice" {
		t.Errorf("Home() = %q", got)
	}
	if got := Name(); got != "a Mac" {
		t.Errorf("Name() = %q", got)
	}
}

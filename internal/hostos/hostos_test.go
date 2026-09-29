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

// fakeMarkers stands dir's vm and host-guard.nft in for AgentBox's VM's
// markers.
func fakeMarkers(t *testing.T, dir string) {
	t.Helper()
	old := markers
	markers = []struct{ file, os string }{
		{filepath.Join(dir, "vm"), ""},
		{filepath.Join(dir, "host-guard.nft"), Linux},
	}
	t.Cleanup(func() { markers = old })
}

// AgentBox's VM says what it is to any agentbox in it, whatever its
// environment.
func TestOSFromTheVMsMarker(t *testing.T) {
	fakeRelease(t, "6.12.9-arch1-1")
	t.Setenv(Env, "")
	dir := t.TempDir()
	fakeMarkers(t, dir)
	if got := OS(); got != "" {
		t.Errorf("OS() with no marker = %q, want none", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "host-guard.nft"), []byte("table inet agentbox-host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := OS(); got != Linux {
		t.Errorf("OS() in a Cloud Hypervisor VM made before its marker = %q, want %q", got, Linux)
	}
	if err := os.WriteFile(filepath.Join(dir, "vm"), []byte("darwin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := OS(); got != "darwin" || !InVM() {
		t.Errorf("OS() = %q, InVM() = %t, want darwin, true", got, InVM())
	}
	t.Setenv(Env, "windows")
	if got := OS(); got != "windows" {
		t.Errorf("OS() = %q, want what the front end said to win", got)
	}
}

func TestOS(t *testing.T) {
	fakeMarkers(t, t.TempDir())
	for _, tc := range []struct {
		name, env, release, want string
		vm                       bool
	}{
		{"a Linux machine", "", "6.12.9-arch1-1", "", false},
		{"no /proc", "", "", "", false},
		{"WSL2, found from its kernel", "", "6.6.87.2-microsoft-standard-WSL2", Windows, true},
		{"a front end that says so", "darwin", "6.12.9-arch1-1", "darwin", true},
		{"what a front end says wins", "darwin", "6.6.87.2-microsoft-standard-WSL2", "darwin", true},
		{"a Linux front end's VM", Linux, "6.12.9-arch1-1", Linux, true},
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

func TestNameOnALinuxHost(t *testing.T) {
	t.Setenv(Env, Linux)
	if got := Name(); got != "a Linux host" {
		t.Errorf("Name() = %q", got)
	}
}

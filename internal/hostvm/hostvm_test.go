package hostvm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/hostos"
	"agentbox/internal/paths"
)

// fakeLima is a limactl that records its arguments, one call per line, keeps
// what it's sent on stdin, and answers `list` and the digest check the way
// Lima does.
const fakeLima = `#!/bin/sh
log=$FAKE_DIR/calls
printf '%s\n' "$*" >>"$log"
case "$1" in
  list) cat "$FAKE_DIR/list" 2>/dev/null ;;
  shell)
    case "$*" in
      *sha256sum*) cat "$FAKE_DIR/digest" 2>/dev/null ;;
      *"id -un"*) echo alice ;;
      *"sudo sh -c"*) cat >"$FAKE_DIR/installed" ;;
    esac ;;
esac
exit 0
`

func newFake(t *testing.T) (*VM, string) {
	t.Helper()
	// The tests say which AGENTBOX_ settings there are; the shell's don't count.
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "AGENTBOX_") {
			t.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}
	// Resolved, as New resolves the home directory: macOS's temporary
	// directory is under /var, a symlink to /private/var.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lima := filepath.Join(dir, "limactl")
	if err := os.WriteFile(lima, []byte(fakeLima), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, LinuxBinary)
	if err := os.WriteFile(bin, []byte("the linux agentbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DIR", dir)
	home := filepath.Join(dir, "home")
	_ = os.MkdirAll(home, 0o755)
	vm := &VM{
		Limactl: lima,
		Name:    "agentbox",
		Home:    home,
		Paths:   paths.Paths{Config: filepath.Join(home, ".config", "agentbox"), Data: filepath.Join(home, ".local", "share", "agentbox")},
		Binary:  bin,
		Log:     &bytes.Buffer{},
	}
	return vm, dir
}

func calls(t *testing.T, dir string) []string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestDefinition(t *testing.T) {
	vm, _ := newFake(t)
	def, err := vm.Definition(Size{CPUs: 4, Memory: "8GiB", Disk: "100GiB"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"cpus: 4\n", "memory: 8GiB\n", "disk: 100GiB\n",
		"location: \"" + vm.Home + "\"\n    writable: true",
		"mountType: virtiofs",
		// Lima's own template variable, for the VM's home: left for Lima.
		"guestSocket: \"{{.Home}}/.local/share/agentbox/run/agentbox.sock\"",
		"hostSocket: \"" + vm.Paths.Socket() + "\"",
		"getent ahostsv4 host.lima.internal",
		`iifname "incusbr0" ip daddr $mac drop`,
		"containerd:\n  system: false\n  user: false",
	} {
		if !strings.Contains(def, want) {
			t.Errorf("the definition has no %q:\n%s", want, def)
		}
	}
	if strings.Contains(def, "vmType") {
		t.Error("a definition with no VM type names one: Lima's default is vz on a Mac")
	}
	vm.VMType = "qemu"
	def, _ = vm.Definition(DefaultSize())
	if !strings.Contains(def, "vmType: qemu\n") {
		t.Errorf("no vmType: qemu in:\n%s", def)
	}
}

func TestState(t *testing.T) {
	vm, dir := newFake(t)
	st, err := vm.State(context.Background())
	if err != nil || st.Exists {
		t.Fatalf("no instances: got %+v, %v", st, err)
	}
	list := `{"name":"other","status":"Running"}
{"name":"agentbox","status":"Stopped","dir":"/x/agentbox","cpus":4,"memory":8589934592,"disk":107374182400,"arch":"aarch64"}
`
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(list), 0o644)
	st, err = vm.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.Status != "Stopped" || st.CPUs != 4 || st.Arch != "aarch64" {
		t.Fatalf("got %+v", st)
	}
}

func TestUp(t *testing.T) {
	vm, dir := newFake(t)
	if err := vm.Up(context.Background()); !errors.Is(err, ErrNotCreated) {
		t.Fatalf("a VM that doesn't exist: got %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Stopped"}`+"\n"), 0o644)
	if err := vm.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(calls(t, dir), "start --tty=false agentbox") {
		t.Errorf("a stopped VM wasn't started: %q", calls(t, dir))
	}
	if _, err := os.Stat(filepath.Dir(vm.Paths.Socket())); err != nil {
		t.Error("the socket's directory wasn't made, and Lima needs it")
	}
}

// A VM whose agentbox differs from the one shipped gets the shipped one; one
// that matches is left alone.
func TestReady(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(dir, "list"), []byte(`{"name":"agentbox","status":"Running"}`+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "digest"), []byte("0000\n"), 0o644)
	if err := vm.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "installed"))
	if err != nil || string(got) != "the linux agentbox" {
		t.Fatalf("the binary wasn't installed: %q, %v", got, err)
	}

	_ = os.Remove(filepath.Join(dir, "installed"))
	want, _ := vm.Digest()
	_ = os.WriteFile(filepath.Join(dir, "digest"), []byte(want+"\n"), 0o644)
	if err := vm.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "installed")); err == nil {
		t.Error("an up-to-date binary was installed again")
	}
}

func TestForwardArgs(t *testing.T) {
	vm, _ := newFake(t)
	// Host-side settings stay behind; the rest go to the VM.
	t.Setenv("AGENTBOX_SOCKET", "/Users/alice/sock")
	t.Setenv("AGENTBOX_PREVIEW_ADDR", "127.0.0.1:17777")
	got := vm.ForwardArgs("/Users/alice/app", []string{"create", "app", "--name", "it's mine"})
	want := []string{vm.Limactl, "shell", "--workdir", "/Users/alice/app", "agentbox", "--", "env",
		hostos.Env + "=" + goos(), hostos.HomeEnv + "=" + vm.Home, "AGENTBOX_WORKTREES=" + filepath.Join(vm.Paths.Data, "worktrees"),
		"AGENTBOX_PREVIEW_ADDR=127.0.0.1:17777",
		"/usr/local/bin/agentbox", "create", "app", "--name", "it's mine"}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// DO_NOT_TRACK set on the Mac has to reach the daemon in the VM, whose update
// check is what honours it.
func TestForwardDoNotTrack(t *testing.T) {
	vm, _ := newFake(t)
	t.Setenv("DO_NOT_TRACK", "1")
	if got := vm.ForwardArgs(vm.Home, []string{"version"}); !slices.Contains(got, "DO_NOT_TRACK=1") {
		t.Errorf("DO_NOT_TRACK isn't forwarded: %q", got)
	}
}

// Only the home directory is in the VM: a command run anywhere else runs in it.
func TestWorkdir(t *testing.T) {
	vm, dir := newFake(t)
	inside := filepath.Join(vm.Home, "code", "app")
	_ = os.MkdirAll(inside, 0o755)
	t.Chdir(inside)
	if got := vm.workdir(); got != inside {
		t.Errorf("under the home directory: got %s", got)
	}
	t.Chdir(dir)
	if got := vm.workdir(); got != vm.Home {
		t.Errorf("outside it: got %s, want %s", got, vm.Home)
	}
	sibling := vm.Home + "-other"
	_ = os.MkdirAll(sibling, 0o755)
	t.Chdir(sibling)
	if got := vm.workdir(); got != vm.Home {
		t.Errorf("a sibling that shares the home's prefix: got %s", got)
	}
}

func TestSetup(t *testing.T) {
	vm, dir := newFake(t)
	_ = os.WriteFile(filepath.Join(vm.Home, ".gitconfig"), []byte("[user]\n"), 0o644)
	if err := vm.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	log := strings.Join(calls(t, dir), "\n")
	for _, want := range []string{
		"sudo /usr/local/bin/agentbox host setup --user alice",
		"ln -sfn '" + filepath.Join(vm.Home, ".gitconfig") + "' ~/.gitconfig",
		"env " + hostos.Env + "=" + goos(),
		"cat >/etc/profile.d/agentbox-host.sh",
		"/usr/local/bin/agentbox daemon start",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("setup didn't run %q; it ran:\n%s", want, log)
		}
	}
}

func goos() string { return runtime.GOOS }

// A shell in the VM gets the settings the front end gives every command, so a
// daemon started from it puts worktrees on the share too.
func TestProfile(t *testing.T) {
	vm, _ := newFake(t)
	vm.Home = "/Users/o'brien"
	got := vm.profile()
	for _, want := range []string{
		"export " + hostos.Env + "=" + shellQuote(goos()) + "\n",
		"export " + hostos.HomeEnv + `='/Users/o'\''brien'` + "\n",
		"export AGENTBOX_WORKTREES=" + shellQuote(vm.Paths.Worktrees()) + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the profile has no %q:\n%s", want, got)
		}
	}
}

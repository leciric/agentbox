package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/testutil"
)

// shimDirs lays out a shim directory with go and cargo linked to the shim,
// ahead of a directory with fake real ones that say how they were run.
func shimDirs(t *testing.T) (shims, real string) {
	t.Helper()
	root := t.TempDir()
	shims, real = filepath.Join(root, "shims"), filepath.Join(root, "real")
	for _, d := range []string{shims, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Not os.WriteFile: a parallel test's fork could hold the file open and
	// running it would fail with "text file busy".
	testutil.WriteExecutable(t, filepath.Join(shims, "agentbox-shim"), []byte(agent.ShimScript()))
	for _, name := range []string{"go", "cargo"} {
		if err := os.Symlink("agentbox-shim", filepath.Join(shims, name)); err != nil {
			t.Fatal(err)
		}
		fake := "#!/bin/sh\necho real " + name + " \"$@\"\n"
		testutil.WriteExecutable(t, filepath.Join(real, name), []byte(fake))
	}
	return shims, real
}

// The shim runs the real command, past its own directory in PATH, straight
// from the shell for what an editor runs all the time and inside a heavy
// command, without asking anything.
func TestShimRunsTheRealCommand(t *testing.T) {
	t.Parallel()
	shims, real := shimDirs(t)
	run := func(env []string, name string, args ...string) string {
		cmd := exec.Command(filepath.Join(shims, name), args...)
		cmd.Env = append([]string{"PATH=" + shims + ":" + real + ":/usr/bin:/bin"}, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run(nil, "go", "list", "./..."); got != "real go list ./..." {
		t.Errorf("go list = %q", got)
	}
	if got := run([]string{agent.HeavyEnv + "=heavy-1"}, "cargo", "build", "--release"); got != "real cargo build --release" {
		t.Errorf("cargo build inside a heavy command = %q", got)
	}
}

// heavy-shim runs a command that isn't heavy in its own place, and finds
// the real one past the shims.
func TestHeavyShimRunsALightCommandAsItIs(t *testing.T) {
	shims, real := shimDirs(t)
	t.Setenv("PATH", shims+":"+real)
	var ran []string
	was := execReal
	execReal = func(path string, argv, _ []string) error {
		ran = append([]string{path}, argv...)
		return nil
	}
	defer func() { execReal = was }()
	var stderr bytes.Buffer
	if code := heavyShim(context.Background(), "cargo", shims, []string{"fmt"}, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if want := []string{filepath.Join(real, "cargo"), "cargo", "fmt"}; strings.Join(ran, " ") != strings.Join(want, " ") {
		t.Errorf("ran %v, want %v", ran, want)
	}
	if code := heavyShim(context.Background(), "nothere", shims, nil, &stderr); code != 127 {
		t.Errorf("a command that isn't there: exit %d", code)
	}
}

// A heavy command runs at once, in its run (AGENTBOX_HEAVY, so the shims
// under it don't ask again), with the worker counts the daemon answered; a
// daemon that answers none (one before 20 Nov 2026) leaves them as they are.
func TestHeavyCommandRunsWithWorkerCounts(t *testing.T) {
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("AGENTBOX_CPUS", "") // restored after; unset meanwhile
	_ = os.Unsetenv("AGENTBOX_CPUS")
	out := filepath.Join(t.TempDir(), "env")
	for _, tc := range []struct {
		workers int
		flags   string
	}{{6, "-mod=mod -p=6"}, {0, "-mod=mod"}} {
		f := &fakeHeavy{start: true, workers: tc.workers}
		run := heavyCommandRun{path: "/bin/sh", argv: []string{"sh", "-c", `echo "$AGENTBOX_HEAVY|$GOFLAGS" > ` + out}, command: "go test ./x"}
		var stderr bytes.Buffer
		if code, err := runHeavyCommand(context.Background(), f, run, &stderr); code != 0 || err != nil {
			t.Fatalf("exit %d, %v", code, err)
		}
		b, _ := os.ReadFile(out)
		key, flags, _ := strings.Cut(strings.TrimSpace(string(b)), "|")
		if !strings.HasPrefix(key, "heavy-") || flags != tc.flags {
			t.Errorf("%d workers: the command saw %q, want its key and GOFLAGS %q", tc.workers, b, tc.flags)
		}
		if len(f.asked) != 1 || len(f.ended) != 1 {
			t.Errorf("asked %v, ended %v", f.asked, f.ended)
		}
	}
}

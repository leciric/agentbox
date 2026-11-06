package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentbox/internal/agent"
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
	if err := os.WriteFile(filepath.Join(shims, "agentbox-shim"), []byte(agent.ShimScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go", "cargo"} {
		if err := os.Symlink("agentbox-shim", filepath.Join(shims, name)); err != nil {
			t.Fatal(err)
		}
		fake := "#!/bin/sh\necho real " + name + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(real, name), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
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

// A heavy command runs once it may, in its run (AGENTBOX_HEAVY, so the
// shims under it don't ask again), with the worker counts set, and halved
// when it had to wait.
func TestHeavyCommandRunsWithWorkerCounts(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	was := workersFor
	var waitedSeen []bool
	workersFor = func(env []string, waited bool) []string {
		waitedSeen = append(waitedSeen, waited)
		return agent.WorkerEnv(env, 8, 0, waited)
	}
	defer func() { workersFor = was }()
	for _, tc := range []struct {
		wait  int
		jobs  string
		waits bool
	}{{0, "-p=8", false}, {2, "-p=4", true}} {
		f := &fakeHeavy{}
		var c heavyClient = f
		if tc.wait > 0 {
			c = &waitThenStart{fakeHeavy: f, after: tc.wait}
		} else {
			f.start = true
		}
		run := heavyCommandRun{path: "/bin/sh", argv: []string{"sh", "-c", `echo "$AGENTBOX_HEAVY $GOFLAGS" > ` + out}, command: "go test ./x"}
		var stderr bytes.Buffer
		if code, err := runHeavyCommand(context.Background(), c, run, &stderr); code != 0 || err != nil {
			t.Fatalf("exit %d, %v", code, err)
		}
		b, _ := os.ReadFile(out)
		fields := strings.Fields(string(b))
		if len(fields) != 2 || !strings.HasPrefix(fields[0], "heavy-") || fields[1] != tc.jobs {
			t.Errorf("waited %d: the command saw %q, want its key and %s", tc.wait, b, tc.jobs)
		}
		if len(f.ended) != 1 {
			t.Errorf("ended %v", f.ended)
		}
	}
	if len(waitedSeen) != 2 || waitedSeen[0] || !waitedSeen[1] {
		t.Errorf("waited = %v", waitedSeen)
	}
}

// The cores a heavy command counts are the machine's share, as nproc says:
// its cgroup's cpu.max quota, rounded up, never more than it may run on.
func TestMachineCPUs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for content, want := range map[string]int{"200000 100000": 2, "150000 100000": 2, "max 100000": runtime.NumCPU(), "50000 100000": 1} {
		p := filepath.Join(dir, "cpu.max")
		if err := os.WriteFile(p, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := machineCPUs(p); got != min(want, runtime.NumCPU()) {
			t.Errorf("%q: %d, want %d", content, got, want)
		}
	}
	if got := machineCPUs(filepath.Join(dir, "none")); got != runtime.NumCPU() {
		t.Errorf("no cpu.max: %d", got)
	}
}

package iossim

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// serve runs the helper on fakes until the test ends.
func serve(t *testing.T, dir, version string) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	x := &fakeXcrun{}
	go func() {
		done <- Serve(ctx, ServeOptions{ConfigDir: dir, Version: version, Runner: x.run, Launcher: newFakeSimhost().launch})
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Serve: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func waitForHelper(t *testing.T, dir, version string) State {
	t.Helper()
	ctx := testContext(t)
	for {
		if v, err := local(StateFile(dir)).Version(ctx); err == nil && v == version {
			st, _ := ReadState(StateFile(dir))
			return st
		}
		select {
		case <-ctx.Done():
			t.Fatal("the helper didn't come up")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestServeWritesAPrivateStateFileAndOnlyOneRuns(t *testing.T) {
	dir := t.TempDir()
	stop := serve(t, dir, "1.0.0")
	st := waitForHelper(t, dir, "1.0.0")
	if st.PID != os.Getpid() || len(st.Token) != 64 || st.Host != "" {
		t.Errorf("state = %+v", st)
	}
	fi, err := os.Stat(StateFile(dir))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v, %v", fi.Mode(), err)
	}
	// A second one finds the first holding the lock and leaves.
	if err := Serve(testContext(t), ServeOptions{ConfigDir: dir, Version: "1.0.0", Runner: (&fakeXcrun{}).run}); err != nil {
		t.Fatal(err)
	}
	if again, _ := ReadState(StateFile(dir)); again != st {
		t.Errorf("the second helper changed the state file: %+v", again)
	}
	stop()
	if _, err := os.Stat(StateFile(dir)); !os.IsNotExist(err) {
		t.Errorf("the state file outlived the helper: %v", err)
	}
}

func TestEnsureLeavesARunningHelperOfThisVersion(t *testing.T) {
	dir := t.TempDir()
	serve(t, dir, "1.0.0")
	waitForHelper(t, dir, "1.0.0")
	started := false
	err := Ensure(testContext(t), EnsureOptions{ConfigDir: dir, Version: "1.0.0", Exe: "agentbox", Start: func(*exec.Cmd) error {
		started = true
		return nil
	}})
	if err != nil || started {
		t.Errorf("Ensure: %v, started %v", err, started)
	}
}

func TestEnsureStartsOneAndReplacesAnOlderOne(t *testing.T) {
	dir := t.TempDir()
	stopOld := serve(t, dir, "1.0.0")
	waitForHelper(t, dir, "1.0.0")
	var stopped []int
	old := stopHelper
	t.Cleanup(func() { stopHelper = old })
	stopHelper = func(pid int) {
		stopped = append(stopped, pid)
		stopOld()
	}
	var args []string
	err := Ensure(testContext(t), EnsureOptions{
		ConfigDir: dir, Version: "1.1.0", Exe: "/Applications/AgentBox.app/agentbox", Roots: []string{"/Users/dev/worktrees"},
		Start: func(cmd *exec.Cmd) error {
			args = cmd.Args
			if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
				t.Error("the helper isn't started in a session of its own, to outlive the command")
			}
			serve(t, dir, "1.1.0")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []int{os.Getpid()}) {
		t.Errorf("stopped %v, want the old helper", stopped)
	}
	want := []string{"/Applications/AgentBox.app/agentbox", HelperCommand, "--config", dir, "--root", "/Users/dev/worktrees"}
	if !slices.Equal(args, want) {
		t.Errorf("started %v, want %v", args, want)
	}
}

func TestEnsureSaysWhenTheHelperDoesntStartAndWaitsBeforeTryingAgain(t *testing.T) {
	dir := t.TempDir()
	starts := 0
	o := EnsureOptions{ConfigDir: dir, Version: "1", Exe: "agentbox", Wait: 50 * time.Millisecond, Start: func(*exec.Cmd) error { starts++; return nil }}
	err := Ensure(testContext(t), o)
	if err == nil || !strings.Contains(err.Error(), "ios-helper.log") {
		t.Errorf("Ensure = %v", err)
	}
	// The next command doesn't wait for it again.
	if err := Ensure(testContext(t), o); err == nil || starts != 1 {
		t.Errorf("Ensure again = %v, after %d starts", err, starts)
	}
	// Once it runs, it's found, and the failure forgotten.
	serve(t, dir, "1")
	waitForHelper(t, dir, "1")
	if err := Ensure(testContext(t), o); err != nil {
		t.Errorf("Ensure with the helper up = %v", err)
	}
}

func TestTheDaemonsClientSaysWhyItCantReachTheHelper(t *testing.T) {
	ctx := testContext(t)
	if _, err := NewClient("").Devices(ctx); err == nil || !strings.Contains(Problem(err), "runs on a Mac") {
		t.Errorf("no state file named: %v", err)
	}
	_, err := NewClient(filepath.Join(t.TempDir(), "ios-helper.json")).Devices(ctx)
	if err == nil || !strings.Contains(Problem(err), "isn't running on your Mac") {
		t.Errorf("no state file: %q", Problem(err))
	}
}

func TestBuildCompilesOncePerSource(t *testing.T) {
	dir := t.TempDir()
	var compiles int
	run := func(ctx context.Context, stdin io.Reader, name string, args ...string) ([]byte, error) {
		compiles++
		if name != "xcrun" || args[0] != "clang" || !slices.Contains(args, "-fobjc-arc") || !slices.Contains(args, "VideoToolbox") {
			t.Errorf("compiled with %s %v", name, args)
		}
		out := args[slices.Index(args, "-o")+1]
		return nil, os.WriteFile(out, []byte("binary"), 0o755)
	}
	if err := os.WriteFile(filepath.Join(dir, "simhost-000000000000"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := Build(testContext(t), dir, run)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Build(testContext(t), dir, run)
	if err != nil || again != bin || compiles != 1 {
		t.Errorf("second build: %s, %v, %d compiles", again, err, compiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "simhost-000000000000")); !os.IsNotExist(err) {
		t.Error("an older simhost was kept")
	}
	src, _ := os.ReadFile(filepath.Join(dir, "simhost.m"))
	if string(src) != string(simhostSource) {
		t.Error("simhost.m isn't the embedded source")
	}
}

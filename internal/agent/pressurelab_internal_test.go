//go:build pressurelab

package agent

// The pressure lab: several "agents" each running a real heavy command at
// once, in cgroups laid out the way Incus lays out agents' machines, under a
// parent capped like a small VM, with the daemon's governor (package pressure)
// reading that parent's PSI and pausing, reclaiming and resuming runs through
// the functions the daemon uses. It needs root and cgroup2, and takes minutes:
//
//	sudo -E env PATH=$PATH go test -tags pressurelab -run TestPressureLab -v -timeout 30m ./internal/agent
//
// LAB_AGENTS (6), LAB_MEMORY (VM size, 4G), LAB_CMD (the heavy command,
// `go test -count=1 ./internal/state ./internal/pressure` with its own build
// cache per agent, under LAB_CACHE) say what it runs, and LAB_FREEZE the full
// pressure that pauses runs, to see pausing at a lower pressure than the
// daemon's.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/pressure"
)

func labEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func TestPressureLab(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the lab needs root, to make cgroups")
	}
	agents, _ := strconv.Atoi(labEnv("LAB_AGENTS", "6"))
	vm := parseLabSize(t, labEnv("LAB_MEMORY", "4G"))
	command := labEnv("LAB_CMD", "go test -count=1 ./internal/state ./internal/pressure")
	repo, _ := filepath.Abs("../..")

	root := filepath.Join(cgroupRoot, "agentbox-lab")
	_ = os.Mkdir(root, 0o755)
	t.Cleanup(func() { removeLab(root) })
	mustWrite(t, filepath.Join(root, "memory.max"), strconv.FormatInt(vm, 10))
	mustWrite(t, filepath.Join(root, "memory.swap.max"), "max")
	mustWrite(t, filepath.Join(root, "cgroup.subtree_control"), "+memory")
	limit := MemoryLimit(vm)
	t.Logf("VM %s, %d agents, each memory.max %s, running %q", HumanBytes(vm), agents, HumanBytes(limit), command)

	type labAgent struct {
		instance, key string
		cmd           *exec.Cmd
		go_           chan struct{}
		done          chan error
		finished      bool
		err           error
		start, end    time.Time
	}
	var all []*labAgent
	policy := pressure.DefaultPolicy
	if v, err := strconv.ParseFloat(os.Getenv("LAB_FREEZE"), 64); err == nil {
		policy.Freeze = v
	}
	g := pressure.NewGovernor(policy)
	t.Logf("policy %+v", policy)
	now := time.Now()
	for i := range agents {
		a := &labAgent{instance: fmt.Sprintf("lab-%02d", i), key: "bash-run", go_: make(chan struct{}), done: make(chan error, 1)}
		machine := agentCgroup(root, a.instance)
		_ = os.Mkdir(machine, 0o755)
		mustWrite(t, filepath.Join(machine, "cgroup.subtree_control"), "+memory")
		if err := setCgroupLimit(machine, "memory.max", limit, writeCgroup); err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(machine, "user.slice")
		_ = os.Mkdir(base, 0o755)
		// The agent's shell: it waits until its run may start and has joined,
		// then runs the command, the way the Bash tool's shell does.
		a.cmd = exec.Command("sh", "-c", "read go; exec "+command)
		a.cmd.Dir = repo
		a.cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(labEnv("LAB_CACHE", os.TempDir()), "lab-cache-"+a.instance), "GOFLAGS=")
		stdin, err := a.cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		logFile, _ := os.Create(filepath.Join(os.TempDir(), "lab-"+a.instance+".log"))
		a.cmd.Stdout, a.cmd.Stderr = logFile, logFile
		if err := a.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(base, "cgroup.procs"), strconv.Itoa(a.cmd.Process.Pid))
		go func() {
			<-a.go_
			_, _ = stdin.Write([]byte("go\n"))
			_ = stdin.Close()
		}()
		go func() { a.done <- a.cmd.Wait() }()
		g.Ask(a.instance, a.key, command, time.Hour, now)
		all = append(all, a)
	}

	byInstance := map[string]*labAgent{}
	for _, a := range all {
		byInstance[a.instance] = a
	}
	psiFile := filepath.Join(root, "memory.pressure")
	var pauses, resumes, worstPaused int
	deadline := time.Now().Add(25 * time.Minute)
	for tick := 0; ; tick++ {
		if time.Now().After(deadline) {
			t.Fatal("the lab ran out of time")
		}
		now := time.Now()
		for _, a := range all {
			select {
			case err := <-a.done:
				a.finished, a.err, a.end = true, err, now
				if r := g.End(a.instance, a.key); r != nil {
					_ = freezeRun(root, a.instance, a.key, false, writeCgroup)
				}
			default:
			}
		}
		psi, err := pressure.Read(psiFile)
		if err != nil {
			t.Fatal(err)
		}
		acts := g.Step(now, psi)
		for _, r := range acts.Start {
			a := byInstance[r.Agent]
			if err := placeRun(root, "/proc", a.instance, a.key, a.cmd.Process.Pid, writeCgroup); err != nil {
				t.Fatalf("placing %s: %v", a.instance, err)
			}
			g.Place(a.instance, a.key)
			a.start = now
			close(a.go_)
		}
		for _, r := range acts.Pause {
			pauses++
			if err := freezeRun(root, r.Agent, r.Key, true, writeCgroup); err != nil {
				t.Fatal(err)
			}
			go func(instance, key string) { _ = reclaimRun(root, instance, key, writeCgroup) }(r.Agent, r.Key)
		}
		for _, r := range acts.Resume {
			resumes++
			if err := freezeRun(root, r.Agent, r.Key, false, writeCgroup); err != nil {
				t.Fatal(err)
			}
		}
		running, paused := 0, 0
		for _, r := range g.Running() {
			if r.Paused {
				paused++
			} else {
				running++
			}
		}
		waiting := len(g.Waiting())
		worstPaused = max(worstPaused, paused)
		if running == 0 && paused+waiting > 0 {
			t.Fatalf("at %ds nothing runs: %d paused, %d waiting", tick, paused, waiting)
		}
		if tick%5 == 0 || !acts.Empty() {
			current, _ := os.ReadFile(filepath.Join(root, "memory.current"))
			used, _ := strconv.ParseInt(strings.TrimSpace(string(current)), 10, 64)
			t.Logf("%4ds some %5.1f%% full %5.1f%%  used %-8s running %d paused %d waiting %d  %s",
				tick, psi.Some.Avg10, psi.Full.Avg10, HumanBytes(used), running, paused, waiting, labActions(acts))
		}
		left := 0
		for _, a := range all {
			if !a.finished {
				left++
			}
		}
		if left == 0 {
			break
		}
		time.Sleep(time.Second)
	}
	var kills int64
	for _, a := range all {
		c, _ := memoryEvents(agentCgroup(root, a.instance))
		k := c.Kills
		kills += k
		status := "ok"
		if a.err != nil {
			status = a.err.Error()
		}
		t.Logf("%s: waited %s, ran %s, %s, %d killed for memory.max", a.instance,
			a.start.Sub(now).Round(time.Second), a.end.Sub(a.start).Round(time.Second), status, k)
	}
	t.Logf("%d pauses, %d resumes, at most %d paused at once, %d processes killed for memory.max", pauses, resumes, worstPaused, kills)
}

func labActions(a pressure.Actions) string {
	var parts []string
	for _, r := range a.Start {
		parts = append(parts, "start "+r.Agent)
	}
	for _, r := range a.Pause {
		parts = append(parts, "PAUSE "+r.Agent)
	}
	for _, r := range a.Resume {
		parts = append(parts, "resume "+r.Agent)
	}
	return strings.Join(parts, ", ")
}

func parseLabSize(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(strings.TrimRight(s, "G"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n << 30
}

func mustWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// removeLab kills what's left in the lab's cgroups and removes them, deepest
// first.
func removeLab(root string) {
	_ = os.WriteFile(filepath.Join(root, "cgroup.kill"), []byte("1"), 0)
	time.Sleep(500 * time.Millisecond)
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

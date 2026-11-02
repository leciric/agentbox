package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// idleMachine is an idle agent's machine as the VM sees it: its system, its
// chat's session with a dev server the chat left running, its desktop, Docker
// and a heavy run of the governor's.
func idleMachine(t *testing.T) (root, proc, machine string) {
	t.Helper()
	root, proc = t.TempDir(), t.TempDir()
	machine = filepath.Join(root, "lxc.payload.m")
	cgroups := map[string][]int{
		"init.scope":                                   {1},
		"system.slice/systemd-networkd.service":        {225},
		"system.slice/containerd.service":              {288},
		"system.slice/docker.service":                  {341},
		"user.slice/user-1000.slice/session-c13.scope": {1188, 1189, 1214, 1260, 1300, 1777, 1778},
		"user.slice/user-1000.slice/session-c8.scope":  {823, 937},
		"agentbox.run.k":                               {5000},
	}
	writeCgroupFile(t, filepath.Join(machine, "cgroup.procs"), "")
	for dir, pids := range cgroups {
		var b strings.Builder
		for _, pid := range pids {
			fmt.Fprintf(&b, "%d\n", pid)
		}
		writeCgroupFile(t, filepath.Join(machine, dir, "cgroup.procs"), b.String())
		writeCgroupFile(t, filepath.Join(machine, dir, "cgroup.freeze"), "0\n")
	}
	procs := []struct {
		pid, ppid int
		name, cmd string
		rssMB     int
	}{
		{1, 0, "systemd", "/sbin/init", 12},
		{225, 1, "systemd-network", "/usr/lib/systemd/systemd-networkd", 8},
		{288, 1, "containerd", "/usr/bin/containerd", 60},
		{341, 1, "dockerd", "/usr/bin/dockerd -H fd://", 90},
		{1188, 1, "runuser", "runuser -l lint -c cd /w && exec claude-agent-acp", 4},
		{1189, 1188, "node", "node /home/lint/.local/share/mise/installs/npm-agentclientprotocol-claude-code-acp/dist/index.js", 120},
		{1214, 1189, "claude", "/home/lint/.local/share/mise/installs/npm-agentclientprotocol-claude-code-acp/node_modules/claude", 600},
		{1260, 1214, "agentbox", "/usr/local/bin/agentbox memory mcp", 20},
		{1300, 1214, "node", "node /home/lint/.npm/_npx/x/node_modules/@playwright/mcp/cli.js", 80},
		{1777, 1214, "bash", "/bin/bash -c source /home/lint/.claude/shell-snapshots/snapshot-bash.sh && npm run dev", 4},
		{1778, 1777, "node", "node /w/claude-app/node_modules/.bin/vite --host", 1500},
		{823, 1, "Xvnc", "Xvnc :99 -geometry 1440x900", 150},
		{937, 1, "chromium", "/usr/lib/chromium/chromium", 400},
		{5000, 1214, "go", "go test ./...", 900},
	}
	for _, p := range procs {
		writeCgroupFile(t, filepath.Join(proc, fmt.Sprint(p.pid), "status"),
			fmt.Sprintf("Name:\t%s\nPPid:\t%d\nVmRSS:\t%d kB\n", p.name, p.ppid, p.rssMB<<10))
		writeCgroupFile(t, filepath.Join(proc, fmt.Sprint(p.pid), "cmdline"), strings.ReplaceAll(p.cmd, " ", "\x00")+"\x00")
	}
	return root, proc, machine
}

// idleWrite is a cgroup write that a test can read back.
func idleWrite(t *testing.T, wrote *[]string, fail func(path, value string) bool) func(path, value string) error {
	return func(path, value string) error {
		if fail != nil && fail(path, value) {
			return errors.New("refused")
		}
		*wrote = append(*wrote, path+"="+value)
		return os.WriteFile(path, []byte(value+"\n"), 0o644)
	}
}

func TestFreezeIdleFreezesWhatTheChatLeftRunningAndKeepsTheChat(t *testing.T) {
	t.Parallel()
	root, proc, machine := idleMachine(t)
	var wrote []string
	var stopped []int
	f, err := freezeIdle(root, proc, "m", idleWrite(t, &wrote, nil), func(pid int) error { stopped = append(stopped, pid); return nil })
	if err != nil {
		t.Fatal(err)
	}
	var inPlace []string
	for _, dir := range f.InPlace {
		rel, _ := filepath.Rel(machine, dir)
		inPlace = append(inPlace, rel)
	}
	want := []string{"system.slice/containerd.service", "system.slice/docker.service", "user.slice/user-1000.slice/session-c8.scope"}
	if !slices.Equal(inPlace, want) {
		t.Errorf("froze in place %v, want %v: Docker and the desktop, never init, systemd's own or the chat's session", inPlace, want)
	}
	session := filepath.Join(machine, "user.slice/user-1000.slice/session-c13.scope")
	if len(f.Moved) != 2 || f.Moved[1777] != session || f.Moved[1778] != session {
		t.Errorf("moved %v, want the dev server and its shell out of the chat's session", f.Moved)
	}
	if len(stopped) != 0 || len(f.Stopped) != 0 {
		t.Errorf("stopped %v, %v; everything here could be frozen", stopped, f.Stopped)
	}
	if f.Frozen[0].PID != 1778 || f.Frozen[0].RSS != 1500<<20 {
		t.Errorf("the biggest frozen is %+v, want the dev server", f.Frozen[0])
	}
	idle := filepath.Join(machine, idleCgroup)
	if b, _ := os.ReadFile(filepath.Join(idle, "cgroup.freeze")); strings.TrimSpace(string(b)) != "1" {
		t.Error("the idle cgroup isn't frozen")
	}
	for _, w := range wrote {
		if strings.Contains(w, "agentbox.run.") || strings.Contains(w, "init.scope") || strings.Contains(w, "session-c13.scope/cgroup.freeze") || strings.HasPrefix(w, machine+"/cgroup.freeze") {
			t.Errorf("wrote %s: a heavy run, init, the chat or the whole machine", w)
		}
	}
	// The idle cgroup is frozen before anything moves in.
	freezeAt := slices.Index(wrote, filepath.Join(idle, "cgroup.freeze")+"=1")
	moveAt := slices.IndexFunc(wrote, func(w string) bool { return strings.HasPrefix(w, filepath.Join(idle, "cgroup.procs")) })
	if freezeAt < 0 || moveAt < freezeAt {
		t.Errorf("froze the idle cgroup at %d, moved into it at %d", freezeAt, moveAt)
	}

	wrote = nil
	if err := thawIdle(root, "m", f, idleWrite(t, &wrote, nil)); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		filepath.Join(session, "cgroup.procs") + "=1777",
		filepath.Join(session, "cgroup.procs") + "=1778",
		filepath.Join(idle, "cgroup.freeze") + "=0",
		filepath.Join(machine, "system.slice/docker.service/cgroup.freeze") + "=0",
		filepath.Join(machine, "user.slice/user-1000.slice/session-c8.scope/cgroup.freeze") + "=0",
	} {
		if !slices.Contains(wrote, w) {
			t.Errorf("thawing didn't write %s; wrote %v", w, wrote)
		}
	}
}

func TestFreezeIdleStopsWhatItCantMoveWhenItsBig(t *testing.T) {
	t.Parallel()
	root, proc, machine := idleMachine(t)
	idleProcs := filepath.Join(machine, idleCgroup, "cgroup.procs")
	var wrote []string
	var stopped []int
	f, err := freezeIdle(root, proc, "m", idleWrite(t, &wrote, func(path, value string) bool {
		return path == idleProcs // nothing moves
	}), func(pid int) error { stopped = append(stopped, pid); return nil })
	if err != nil {
		t.Fatal(err)
	}
	// The shell and its dev server are one tree: stopped from the top.
	if !slices.Equal(stopped, []int{1777}) {
		t.Errorf("stopped %v, want the dev server's tree, from its shell", stopped)
	}
	if len(f.Stopped) != 1 || f.Stopped[0].RSS != 1504<<20 || !strings.Contains(f.Stopped[0].Command, "npm run dev") {
		t.Errorf("reported stopping %+v", f.Stopped)
	}
	if len(f.Moved) != 0 {
		t.Errorf("moved %v", f.Moved)
	}
}

func TestStopStuckLeavesSmallProcesses(t *testing.T) {
	t.Parallel()
	procs := map[int]*idleProc{
		10: {IdleProcess: IdleProcess{PID: 10, RSS: 4 << 20}},
		11: {IdleProcess: IdleProcess{PID: 11, RSS: 8 << 20}, ppid: 10},
	}
	var stopped []int
	out := stopStuck(procs, []*idleProc{procs[10], procs[11]}, func(pid int) error { stopped = append(stopped, pid); return nil })
	if len(out) != 0 || len(stopped) != 0 {
		t.Errorf("stopped %v for 12 MB", stopped)
	}
}

func TestThawIdleWithNothingKnownWakesWhatIsFrozenButRuns(t *testing.T) {
	t.Parallel()
	root, proc, machine := idleMachine(t)
	var wrote []string
	if _, err := freezeIdle(root, proc, "m", idleWrite(t, &wrote, nil), func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// The governor paused its run meanwhile.
	writeCgroupFile(t, filepath.Join(machine, "agentbox.run.k", "cgroup.freeze"), "1\n")
	wrote = nil
	if err := thawIdle(root, "m", nil, idleWrite(t, &wrote, nil)); err != nil {
		t.Fatal(err)
	}
	for _, w := range wrote {
		if strings.Contains(w, "agentbox.run.") {
			t.Errorf("woke the governor's run: %s", w)
		}
	}
	for _, dir := range []string{idleCgroup, "system.slice/docker.service", "user.slice/user-1000.slice/session-c8.scope"} {
		if !slices.Contains(wrote, filepath.Join(machine, dir, "cgroup.freeze")+"=0") {
			t.Errorf("didn't wake %s; wrote %v", dir, wrote)
		}
	}
}

func TestReclaimIdleAsksForAllOfTheMachine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	machine := filepath.Join(root, "lxc.payload.m")
	writeCgroupFile(t, filepath.Join(machine, "memory.current"), "3000\n")
	var asked string
	before, after, err := reclaimIdle(root, "m", func(path, value string) error {
		asked = filepath.Base(path) + "=" + value
		writeCgroupFile(t, filepath.Join(machine, "memory.current"), "1000\n")
		return errReclaimShort
	})
	if err != nil || before != 3000 || after != 1000 || asked != "memory.reclaim=3000" {
		t.Errorf("reclaimIdle = %d, %d, %v after asking %q", before, after, err, asked)
	}
}

func TestKeepsRunning(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]bool{
		"/usr/local/bin/agentbox connector mcp hatch":           true,
		"/usr/bin/codex-acp":                                    true,
		"node /opt/opencode/bin/opencode acp":                   true,
		"node /w/claude-clone/node_modules/.bin/vite":           false, // a dev server in a directory named for Claude
		"/bin/bash -c source /home/u/.claude/snap.sh && make w": false,
		"/usr/lib/chromium/chromium --type=renderer":            false,
	} {
		if got := keepsRunning(&idleProc{IdleProcess: IdleProcess{Name: "x", Command: cmd}}); got != want {
			t.Errorf("keepsRunning(%q) = %v, want %v", cmd, got, want)
		}
	}
	if !keepsRunning(&idleProc{IdleProcess: IdleProcess{Name: "systemd-journal"}}) {
		t.Error("froze the journal")
	}
}

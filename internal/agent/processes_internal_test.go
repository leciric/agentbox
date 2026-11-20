package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// busyMachine is an agent's machine as the VM sees it: its system, the
// chat's session with the AI tool, a dev server the tool left running in the
// background and one the user typed in the terminal, the desktop with its
// browser, Docker running a database, and AgentBox's browser watcher.
// Numbers in the machine (NSpid) differ from the VM's by 1000.
func busyMachine(t *testing.T) (root, proc string) {
	t.Helper()
	root, proc = t.TempDir(), t.TempDir()
	machine := filepath.Join(root, "lxc.payload.m")
	cgroups := map[string][]int{
		"init.scope":                                   {1001},
		"system.slice/systemd-journald.service":        {1131},
		"system.slice/docker.service":                  {1341, 1342},
		"system.slice/containerd.service":              {1288},
		"system.slice/docker-abc.scope":                {1400, 1401, 1402},
		"user.slice/user-1000.slice/session-c13.scope": {1188, 1189, 1214, 1260, 1777, 1778, 1779},
		"user.slice/user-1000.slice/session-c2.scope":  {1522, 1523, 1600, 1601},
		"user.slice/user-1000.slice/session-c8.scope":  {1823, 1937, 1938, 1253, 1266},
	}
	writeCgroupFile(t, filepath.Join(machine, "cgroup.procs"), "")
	for dir, pids := range cgroups {
		var b strings.Builder
		for _, pid := range pids {
			fmt.Fprintf(&b, "%d\n", pid)
		}
		writeCgroupFile(t, filepath.Join(machine, dir, "cgroup.procs"), b.String())
	}
	writeCgroupFile(t, filepath.Join(proc, "stat"), "cpu  1 2 3\nbtime 1700000000\n")
	for _, p := range []fakeProc{
		{1001, 0, "systemd", "/sbin/init", 12, 0, 0},
		{1131, 1001, "systemd-journal", "/usr/lib/systemd/systemd-journald", 10, 0, 0},
		{1288, 1001, "containerd", "/usr/bin/containerd", 60, 0, 0},
		{1341, 1001, "dockerd", "/usr/bin/dockerd -H fd://", 90, 0, 0},
		{1342, 1341, "docker-proxy", "/usr/bin/docker-proxy -proto tcp -host-port 5432", 5, 0, 0},
		{1400, 1001, "containerd-shim", "/usr/bin/containerd-shim-runc-v2 -namespace moby -id abc -address /run/containerd/containerd.sock", 10, 0, 0},
		{1401, 1400, "postgres", "postgres", 300, 0, 0},
		{1402, 1401, "postgres", "postgres: checkpointer", 20, 0, 0},
		// The chat's session: runuser starts the adapter, which runs the
		// tool, which runs its MCP server and a background shell.
		{1188, 999, "runuser", "runuser -l lint -c cd /w && exec claude-agent-acp", 4, 0, 0},
		{1189, 1188, "node", "node /home/lint/.local/share/mise/installs/npm-agentclientprotocol-claude-agent-acp/dist/index.js", 120, 0, 0},
		{1214, 1189, "claude", "/home/lint/.local/share/mise/installs/npm-agentclientprotocol-claude-agent-acp/node_modules/claude --output-format stream-json", 600, 3000, 0},
		{1260, 1214, "agentbox", "/usr/local/bin/agentbox memory mcp", 20, 0, 0},
		{1777, 1214, "bash", `/bin/bash -c source /home/lint/.claude/shell-snapshots/snapshot-mcp-1.sh 2>/dev/null || true && eval 'npx nest start --watch' \< /dev/null && pwd -P >| /t/claude-1-cwd`, 4, 1, 100_000},
		{1778, 1777, "node", "node /w/app/node_modules/.bin/nest start --watch", 900, 2000, 100_000},
		{1779, 1778, "node", "node --enable-source-maps /w/app/dist/main", 1400, 400, 200_000},
		// The terminal: tmux, its shell, and what the user typed in it.
		{1522, 1001, "tmux: server", "tmux new-session -d -s main", 3, 0, 0},
		{1523, 1522, "bash", "-bash", 4, 0, 0},
		{1600, 1523, "npm run dev", "npm run dev", 80, 50, 400_000},
		{1601, 1600, "node", "node /w/web/node_modules/vite/bin/vite.js --host", 200, 80, 400_000},
		// The desktop, its browser, and AgentBox's watcher on it.
		{1823, 1001, "Xvnc", "Xvnc :99 -geometry 1440x900", 150, 0, 0},
		{1937, 1001, "chromium", "/usr/lib/chromium/chromium --remote-debugging-port=9222", 400, 0, 0},
		{1938, 1937, "chromium", "/usr/lib/chromium/chromium --type=renderer", 250, 0, 0},
		{1253, 1001, "sh", "sh /usr/local/bin/agentbox-browser watch", 2, 0, 0},
		{1266, 1253, "xev", "xev -display :99 -root -event property", 3, 0, 0},
	} {
		p.write(t, proc)
	}
	return root, proc
}

type fakeProc struct {
	pid, ppid  int
	name, cmd  string
	rssMB      int
	cpu, start uint64
}

func (p fakeProc) write(t *testing.T, proc string) {
	t.Helper()
	dir := filepath.Join(proc, fmt.Sprint(p.pid))
	writeCgroupFile(t, filepath.Join(dir, "status"),
		fmt.Sprintf("Name:\t%s\nPPid:\t%d\nVmRSS:\t%d kB\nNSpid:\t%d\t%d\n", p.name, p.ppid, p.rssMB<<10, p.pid, p.pid-1000))
	writeCgroupFile(t, filepath.Join(dir, "cmdline"), strings.ReplaceAll(p.cmd, " ", "\x00")+"\x00")
	// utime is the CPU, stime 0, starttime the start; the command has a
	// space and a parenthesis of its own, as a real one may.
	writeCgroupFile(t, filepath.Join(dir, "stat"),
		fmt.Sprintf("%d (%s) x) S %d 1 1 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 %d 1000 100\n", p.pid, p.name, p.ppid, p.cpu, p.start))
}

func treeByLabel(trees []ProcessTree, label string) (ProcessTree, bool) {
	i := slices.IndexFunc(trees, func(t ProcessTree) bool { return t.Label == label })
	if i < 0 {
		return ProcessTree{}, false
	}
	return trees[i], true
}

func machinePIDs(t ProcessTree) []int {
	var out []int
	for _, p := range t.Processes {
		out = append(out, p.MachinePID)
	}
	return out
}

func TestMachineProcessTreesGroupWhatTheAgentStartedAndProtectTheRest(t *testing.T) {
	t.Parallel()
	root, proc := busyMachine(t)
	trees, err := machineProcessTrees(root, proc, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, tr := range trees {
		labels = append(labels, tr.Label)
	}
	want := []string{"npx nest start --watch", "Claude Code", "chromium --remote-debugging-port=9222", "System", "container: postgres", "npm run dev"}
	if !slices.Equal(labels, want) {
		t.Fatalf("trees %q, want %q, the biggest first", labels, want)
	}

	nest, _ := treeByLabel(trees, "npx nest start --watch")
	if nest.Protected() || !slices.Equal(machinePIDs(nest), []int{777, 778, 779}) {
		t.Errorf("the dev server's tree is %v (protected %v), want its shell, nest and the app it runs, by their numbers in the machine", machinePIDs(nest), nest.Protected())
	}
	if mem, rss, _ := nest.Memory(); mem != (4+900+1400)<<20 || rss != mem {
		t.Errorf("the dev server holds %d (rss %d), want every process's resident memory added up", mem, rss)
	}
	if nest.CPUTicks() != 2401 {
		t.Errorf("the dev server used %d ticks, want its processes' added up", nest.CPUTicks())
	}
	if got := nest.Root().Started; !got.Equal(time.Unix(1700000000+1000, 0)) {
		t.Errorf("the dev server started %v, want boot plus its start ticks", got)
	}

	tool, _ := treeByLabel(trees, "Claude Code")
	if tool.Kind != TreeTool || !tool.Protected() || !slices.Equal(machinePIDs(tool), []int{189, 214, 260}) {
		t.Errorf("the tool's tree is %v (%q), want the adapter, the tool and its MCP server, protected", machinePIDs(tool), tool.Kind)
	}
	system, _ := treeByLabel(trees, "System")
	for _, pid := range []int{1, 131, 188, 288, 341, 342, 522, 523, 823, 253, 266} {
		if !slices.Contains(machinePIDs(system), pid) {
			t.Errorf("pid %d isn't the machine's own: init, systemd, runuser, Docker's daemons, tmux and its shell, the display and AgentBox's watcher are", pid)
		}
	}
	if system.Root().MachinePID != 1 {
		t.Errorf("the system's tree starts from %d, want init", system.Root().MachinePID)
	}
	npm, _ := treeByLabel(trees, "npm run dev")
	if npm.Protected() || !slices.Equal(machinePIDs(npm), []int{600, 601}) {
		t.Errorf("what the user typed in the terminal is %v, want npm and vite, apart from the shell", machinePIDs(npm))
	}
	pg, _ := treeByLabel(trees, "container: postgres")
	if pg.Protected() || len(pg.Processes) != 3 {
		t.Errorf("the container's tree is %v, want its shim and what runs in it", machinePIDs(pg))
	}
}

func TestMachineProcessTreesPreferPSS(t *testing.T) {
	t.Parallel()
	root, proc := busyMachine(t)
	trees, err := machineProcessTrees(root, proc, "m", func(_ string, pids []int) map[int]int64 {
		out := map[int]int64{}
		for _, pid := range pids {
			if pid == 1937 || pid == 1938 {
				out[pid] = 100 << 20 // chromium's processes share most of theirs
			}
		}
		return out
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := treeByLabel(trees, "chromium --remote-debugging-port=9222")
	if mem, rss, pss := c.Memory(); mem != 200<<20 || pss != 200<<20 || rss != 650<<20 {
		t.Errorf("chromium holds %d (rss %d, pss %d), want its proportional share", mem, rss, pss)
	}
}

func TestMachineProcessTreesOfAStoppedMachine(t *testing.T) {
	t.Parallel()
	if _, err := machineProcessTrees(t.TempDir(), t.TempDir(), "gone", nil); !errors.Is(err, ErrMachineNotRunning) {
		t.Errorf("err = %v, want ErrMachineNotRunning", err)
	}
}

func TestParsePSSLines(t *testing.T) {
	t.Parallel()
	got := parsePSSLines("/proc", "/proc/12/smaps_rollup:Pss:                1024 kB\n/proc/x/smaps_rollup:Pss: 1 kB\n/proc/13/smaps_rollup:Pss:  2 kB\n")
	if len(got) != 2 || got[12] != 1<<20 || got[13] != 2<<10 {
		t.Errorf("got %v", got)
	}
}

func TestShortCommand(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{`/bin/bash -c source /home/x/.claude/shell-snapshots/s.sh 2>/dev/null || true && eval 'npm run dev -- --port '"'"'3000'"'"'' \< /dev/null && pwd -P >| /t/c`, "npm run dev -- --port '3000'"},
		{`/bin/bash -c -l source /s.sh && eval "go test ./..." && pwd -P`, "go test ./..."},
		{"sh -c sleep 600", "sleep 600"},
		{"node /w/app/node_modules/.bin/nest start --watch", "nest start --watch"},
		{"node --inspect /w/web/node_modules/vite/bin/vite.js --host", "vite --host"},
		{"node /w/web/node_modules/@angular/cli/bin/ng serve", "@angular/cli serve"},
		{"/usr/bin/python3 /w/manage.py runserver", "manage.py runserver"},
		{"/usr/lib/chromium/chromium --headless", "chromium --headless"},
		{"node dist/main", "main"},
	} {
		if got := ShortCommand(tc.in); got != tc.want {
			t.Errorf("ShortCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// killRecorder signals a fake /proc: SIGTERM ends what isn't in stubborn,
// SIGKILL ends anything.
func killRecorder(proc string, stubborn ...int) (signaler, *[]string) {
	var sent []string
	return func(pids []int, sig syscall.Signal) error {
		for _, pid := range pids {
			sent = append(sent, fmt.Sprintf("%d:%d", pid, sig))
			if sig == syscall.SIGKILL || !slices.Contains(stubborn, pid) {
				_ = os.RemoveAll(filepath.Join(proc, fmt.Sprint(pid)))
			}
		}
		return nil
	}, &sent
}

func TestKillMachineProcessKillsTheTreeAndForcesWhatStays(t *testing.T) {
	t.Parallel()
	root, proc := busyMachine(t)
	signal, sent := killRecorder(proc, 1779)
	k, err := killMachineProcess(context.Background(), root, proc, "m", 777, 50*time.Millisecond, signal)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1777:15", "1778:15", "1779:15", "1779:9"}
	if !slices.Equal(*sent, want) {
		t.Errorf("sent %v, want SIGTERM to the shell, nest and the app, then SIGKILL to the app that stayed", *sent)
	}
	if k.Tree.Label != "npx nest start --watch" || len(k.Tree.Processes) != 3 || k.Forced != 1 || k.Left != 0 {
		t.Errorf("killed %+v", k)
	}
	if _, err := os.Stat(filepath.Join(proc, "1214")); err != nil {
		t.Error("the tool that started the dev server was killed with it")
	}
}

func TestKillMachineProcessRefusesTheToolsAndTheMachines(t *testing.T) {
	t.Parallel()
	root, proc := busyMachine(t)
	for _, pid := range []int{214, 189, 260, 1, 188, 523, 341, 823, 253} {
		signal, sent := killRecorder(proc)
		_, err := killMachineProcess(context.Background(), root, proc, "m", pid, time.Millisecond, signal)
		if !errors.Is(err, ErrProtectedProcess) {
			t.Errorf("killing %d: err = %v, want ErrProtectedProcess", pid, err)
		}
		if len(*sent) != 0 {
			t.Errorf("killing %d sent %v", pid, *sent)
		}
	}
	signal, _ := killRecorder(proc)
	if _, err := killMachineProcess(context.Background(), root, proc, "m", 4242, time.Millisecond, signal); !errors.Is(err, ErrNoSuchProcess) {
		t.Errorf("killing a pid that isn't there: err = %v", err)
	}
	// A VM's number isn't the machine's: 1777 is nobody's in the machine.
	if _, err := killMachineProcess(context.Background(), root, proc, "m", 1777, time.Millisecond, signal); !errors.Is(err, ErrNoSuchProcess) {
		t.Errorf("killing by the VM's number: err = %v", err)
	}
}

func TestKillMachineProcessDoesntKillALaterProcessWithTheSameNumber(t *testing.T) {
	t.Parallel()
	root, proc := busyMachine(t)
	var sent []string
	signal := func(pids []int, sig syscall.Signal) error {
		for _, pid := range pids {
			sent = append(sent, fmt.Sprintf("%d:%d", pid, sig))
		}
		if sig == syscall.SIGTERM {
			// The app ends and its number goes to another process at once.
			fakeProc{1779, 1, "node", "node other", 10, 0, 999_999}.write(t, proc)
			_ = os.RemoveAll(filepath.Join(proc, "1777"))
			_ = os.RemoveAll(filepath.Join(proc, "1778"))
		}
		return nil
	}
	k, err := killMachineProcess(context.Background(), root, proc, "m", 777, 50*time.Millisecond, signal)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(sent, "1779:9") || k.Forced != 0 {
		t.Errorf("sent %v (forced %d): SIGKILL to a process that only has the dead one's number", sent, k.Forced)
	}
}

// TestMachineProcessesLive reads this machine's own processes, when asked
// to (AGENTBOX_LIVE_PROCS=1): a check of the parsing against a real /proc.
func TestMachineProcessesLive(t *testing.T) {
	if os.Getenv("AGENTBOX_LIVE_PROCS") == "" {
		t.Skip("set AGENTBOX_LIVE_PROCS=1 to read this machine's processes")
	}
	procs, err := processesIn("/sys/fs/cgroup", "/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range groupTrees(procs) {
		mem, _, _ := tr.Memory()
		t.Logf("%-8s %6d %8s %3d procs  %s", tr.Kind, tr.Root().MachinePID, HumanBytes(mem), len(tr.Processes), tr.Label)
		if os.Getenv("AGENTBOX_LIVE_PROCS") == "v" {
			for _, p := range tr.Processes {
				t.Logf("    %6d %s", p.MachinePID, clip(p.Command, 150))
			}
		}
	}
}

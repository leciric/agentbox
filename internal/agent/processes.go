package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentbox/internal/hostos"
)

// What an agent still has running (the app's Running view, `agentbox ps`
// and `agentbox kill`): every process in its machine, read from the VM's own
// /proc and the machine's cgroups the way idle reclaim reads them, with no
// command run inside the machine. They are grouped in trees, so a dev server
// and what it started count as one thing to see and to kill: a process the
// AI tool or the machine didn't start itself is the root of one, with its
// descendants. The AI tool's own (its adapter, the tool, its MCP servers) and
// the machine's (init, systemd, tmux and the shells that run the tool) are
// grouped as well, and protected: they're shown, so the totals add up, but
// never killed, as the agent's chat dies with them.

// ErrMachineNotRunning is reading the processes of a machine that has none:
// stopped, or never started.
var ErrMachineNotRunning = errors.New("its machine isn't running")

// ErrProtectedProcess is asking to kill a process the machine or its AI tool
// needs.
var ErrProtectedProcess = errors.New("protected")

// ErrNoSuchProcess is asking about a process that isn't in the machine.
var ErrNoSuchProcess = errors.New("no such process in its machine")

// Tree kinds: what started a tree, and whether it may be killed.
const (
	// TreeAgent is what the agent (or the user, in its terminal) started:
	// a dev server, a watcher, a test run, the desktop's browser.
	TreeAgent = ""
	// TreeTool is the AI tool's own: its ACP adapter, the tool and its MCP
	// servers. Protected.
	TreeTool = "tool"
	// TreeSystem is the machine's own: init, systemd and its services that
	// keep the machine up, tmux, the shells the tool runs under and
	// AgentBox's scripts. Protected.
	TreeSystem = "system"
)

// MachineProcess is one process in an agent's machine.
type MachineProcess struct {
	// PID is its number in the VM, what the daemon signals.
	PID int
	// MachinePID is its number as the machine sees it: what the agent's own
	// ps shows, what the API and the CLI name it by.
	MachinePID int
	PPID       int // the parent's number in the VM
	Name       string
	Command    string
	// Started is when it started, and StartTicks the same in clock ticks
	// since boot: with PID, what tells it from a later process given the
	// same number.
	Started    time.Time
	StartTicks uint64
	// CPUTicks is the CPU time it has used, user and system, in USER_HZ.
	CPUTicks uint64
	// RSS is its resident memory (VmRSS), and PSS its proportional share
	// (smaps_rollup's Pss), which divides what processes share between them;
	// 0 when it couldn't be read.
	RSS int64
	PSS int64
	// Cgroup is the cgroup it is in, below the machine's own.
	Cgroup string

	kind string // TreeTool, TreeSystem or TreeAgent
}

// Memory is what a process holds: its proportional share when that could
// be read, its resident memory otherwise.
func (p MachineProcess) Memory() int64 {
	if p.PSS > 0 {
		return p.PSS
	}
	return p.RSS
}

// ProcessTree is one group of processes: a root and every process under it
// of the same kind.
type ProcessTree struct {
	Kind  string
	Label string
	// Processes are the tree's, its root first, then each the order its
	// parent came.
	Processes []MachineProcess
}

// Root is the process the tree starts from.
func (t ProcessTree) Root() MachineProcess { return t.Processes[0] }

// Protected says whether the tree is the machine's or its AI tool's.
func (t ProcessTree) Protected() bool { return t.Kind != TreeAgent }

// Memory is the tree's memory, every process's added up.
func (t ProcessTree) Memory() (memory, rss, pss int64) {
	for _, p := range t.Processes {
		memory += p.Memory()
		rss += p.RSS
		pss += p.PSS
	}
	return memory, rss, pss
}

// CPUTicks is the CPU time the tree's processes have used.
func (t ProcessTree) CPUTicks() uint64 {
	var n uint64
	for _, p := range t.Processes {
		n += p.CPUTicks
	}
	return n
}

// MachineProcesses reads the processes in an agent's machine, grouped in
// trees, the biggest first. pss says whether to read each one's
// proportional memory too, which in AgentBox's VM takes one sudo for all.
func MachineProcesses(instance string, pss bool) ([]ProcessTree, error) {
	var read pssReader
	if pss {
		read = readPSS
	}
	return machineProcessTrees(cgroupRoot, "/proc", instance, read)
}

// pssReader reads the proportional memory of processes, by number in the VM.
type pssReader func(proc string, pids []int) map[int]int64

func machineProcessTrees(root, proc, instance string, readPSS pssReader) ([]ProcessTree, error) {
	procs, err := machineProcesses(root, proc, instance)
	if err != nil {
		return nil, err
	}
	if readPSS != nil {
		pids := make([]int, 0, len(procs))
		for pid := range procs {
			pids = append(pids, pid)
		}
		for pid, n := range readPSS(proc, pids) {
			if p := procs[pid]; p != nil {
				p.PSS = n
			}
		}
	}
	return groupTrees(procs), nil
}

// machineProcesses reads every process in an agent's machine, by number in
// the VM, each marked with its kind. Unlike idle reclaim's machineProcs, it
// reads them all: a heavy run's, and what idle reclaim froze.
func machineProcesses(root, proc, instance string) (map[int]*MachineProcess, error) {
	return processesIn(agentCgroup(root, instance), proc)
}

// processesIn reads every process in a cgroup and the ones below it.
func processesIn(machine, proc string) (map[int]*MachineProcess, error) {
	if _, err := os.Stat(machine); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrMachineNotRunning
		}
		return nil, err
	}
	boot := bootTime(proc)
	procs := map[int]*MachineProcess{}
	err := filepath.WalkDir(machine, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == machine {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(machine, path)
		for _, f := range strings.Fields(string(b)) {
			pid, err := strconv.Atoi(f)
			if err != nil {
				continue
			}
			if p, ok := readMachineProcess(proc, pid, boot); ok {
				p.Cgroup = rel
				procs[pid] = p
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(procs) == 0 {
		return nil, ErrMachineNotRunning
	}
	markKinds(procs)
	return procs, nil
}

// readMachineProcess reads one process from proc. One that ended meanwhile
// isn't there.
func readMachineProcess(proc string, pid int, boot time.Time) (*MachineProcess, bool) {
	dir := filepath.Join(proc, strconv.Itoa(pid))
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return nil, false
	}
	p := &MachineProcess{PID: pid, MachinePID: pid}
	for _, line := range strings.Split(string(status), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Name":
			p.Name = value
		case "PPid":
			p.PPID, _ = strconv.Atoi(value)
		case "VmRSS":
			kb, _ := strconv.ParseInt(strings.TrimSuffix(value, " kB"), 10, 64)
			p.RSS = kb << 10
		case "NSpid":
			// The VM's number, then the machine's, then any namespace's
			// inside it (a Docker container's).
			if f := strings.Fields(value); len(f) >= 2 {
				p.MachinePID, _ = strconv.Atoi(f[1])
			}
		}
	}
	if cmd, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		p.Command = strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))
	}
	if p.Command == "" {
		p.Command = "[" + p.Name + "]"
	}
	if stat, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
		p.CPUTicks, p.StartTicks = parseStat(string(stat))
		if !boot.IsZero() {
			p.Started = boot.Add(time.Duration(p.StartTicks) * time.Second / ClockTicks)
		}
	}
	return p, true
}

// ClockTicks is USER_HZ, what /proc counts CPU time and start times in: 100
// on every Linux AgentBox runs on.
const ClockTicks = 100

// parseStat reads a /proc/<pid>/stat's CPU time (utime and stime) and start
// time. Its fields are counted after the command's closing parenthesis, as
// the command may hold spaces and parentheses of its own.
func parseStat(stat string) (cpu, start uint64) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, 0
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 20 {
		return 0, 0
	}
	utime, _ := strconv.ParseUint(f[11], 10, 64)
	stime, _ := strconv.ParseUint(f[12], 10, 64)
	start, _ = strconv.ParseUint(f[19], 10, 64)
	return utime + stime, start
}

// bootTime is when the VM booted, from /proc/stat's btime.
func bootTime(proc string) time.Time {
	f, err := os.Open(filepath.Join(proc, "stat"))
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}

// markKinds marks each process the AI tool's, the machine's, or neither
// (the agent's). The tool's and the machine's are what idle reclaim keeps
// running (markKept): the tool, its adapter, its MCP servers, AgentBox's own
// commands, the system's processes and the ones those run under. To them it
// adds what only the machine runs, which idle reclaim may freeze but a kill
// shouldn't reach: the shells tmux runs (the agent's terminal, not a command
// in it), and AgentBox's own scripts with what they start.
func markKinds(procs map[int]*MachineProcess) {
	idle := make(map[int]*idleProc, len(procs))
	for pid, p := range procs {
		idle[pid] = &idleProc{IdleProcess: IdleProcess{PID: pid, Name: p.Name, Command: p.Command}, ppid: p.PPID}
	}
	markToolKept(idle)
	tool := map[int]bool{}
	for pid, ip := range idle {
		if ip.keep && isToolProcess(ip) {
			tool[pid] = true
		}
	}
	// An MCP server the tool started is the tool's too (markKept), but not
	// a shell it runs a command in that happens to run the tool again.
	for pid, ip := range idle {
		if ip.keep && !tool[pid] && tool[ip.ppid] && !systemProcs[ip.Name] && !isShell(ip.Command) {
			tool[pid] = true
		}
	}
	for pid, p := range procs {
		switch {
		case tool[pid]:
			p.kind = TreeTool
		case idle[pid].keep:
			p.kind = TreeSystem
		default:
			p.kind = TreeAgent
		}
	}
	// Then what only the machine runs, which needs every other kind known
	// first: it's decided by the parent's.
	var own []*MachineProcess
	for _, p := range procs {
		if p.kind == TreeAgent && machineOwn(p, procs) {
			own = append(own, p)
		}
	}
	for _, p := range own {
		p.kind = TreeSystem
	}
}

// markToolKept is markKept, but for the agentbox commands that wrap one of
// the agent's own (heavy-shim, heavy-hook, heavy-join): they are part of the
// command they run, and killed with it, rather than AgentBox's to keep.
// Idle reclaim keeps them running, as freezing either half would stop both.
func markToolKept(procs map[int]*idleProc) {
	for _, p := range procs {
		if !keepsRunning(p) || wrapsAgentCommand(p.Command) {
			continue
		}
		for q := p; q != nil && !q.keep; q = procs[q.ppid] {
			q.keep = true
		}
	}
	// Not a shell running a script, though: the Bash tool's command line is
	// the whole command, which may mention MCP without being a server.
	for _, p := range procs {
		if parent := procs[p.ppid]; parent != nil && parent.keep && !p.keep && strings.Contains(strings.ToLower(p.Command), "mcp") && !shellScript(p.Command) {
			p.keep = true
		}
	}
}

// shellScript says whether a command line is a shell running a script
// given on it (sh -c '...').
func shellScript(command string) bool {
	return isShell(command) && strings.Contains(" "+command+" ", " -c ")
}

// wrapsAgentCommand says whether a command line is AgentBox running one of
// the agent's commands for it: agentbox heavy-shim go test ./..., say.
func wrapsAgentCommand(command string) bool {
	argv := strings.Fields(command)
	return len(argv) > 1 && filepath.Base(argv[0]) == "agentbox" && strings.HasPrefix(argv[1], "heavy")
}

// isToolProcess says whether a process markKept keeps is the AI tool's or
// AgentBox's own, rather than the system's or one it runs under.
func isToolProcess(p *idleProc) bool {
	if systemProcs[p.Name] || strings.HasPrefix(p.Name, "systemd") {
		return false
	}
	if keepsRunning(p) {
		return true
	}
	// The ACP adapter, kept as the tool's parent: node running a script
	// from the adapter's package, which keepsRunning doesn't look that far
	// for. Not a shell, though, whatever its script mentions.
	lower := strings.ToLower(p.Command)
	return !isShell(p.Command) && slices.ContainsFunc(chatWords, func(w string) bool { return strings.Contains(lower, w) })
}

// machineOwn says whether a process the tool doesn't need is still the
// machine's: one of machineServices, an interactive shell (no script) under
// one that is, as tmux's, or an AgentBox script and anything under it.
func machineOwn(p *MachineProcess, procs map[int]*MachineProcess) bool {
	if machineServices[p.Name] {
		return true
	}
	for q, depth := p, 0; q != nil && depth < 64; q, depth = procs[q.PPID], depth+1 {
		if agentboxScript(q.Command) {
			return true
		}
	}
	parent := procs[p.PPID]
	return parent != nil && parent.kind != TreeAgent && isShell(p.Command) && !shellScript(p.Command)
}

// machineServices are the machine's own programs that idle reclaim may
// freeze but that a kill shouldn't reach: Docker's daemons (the containers
// they run are the agent's) and the desktop's display, window manager and
// dock (its browser is the agent's, and opens again when next used).
var machineServices = map[string]bool{
	"dockerd": true, "containerd": true, "docker-proxy": true,
	"Xvnc": true, "Xvfb": true, "openbox": true, "tint2": true,
}

// agentboxScript says whether a command line runs one of AgentBox's own
// commands in the machine: agentbox-browser, agentbox-android and the like,
// whether run directly or by a shell.
func agentboxScript(command string) bool {
	argv := strings.Fields(command)
	for _, arg := range argv[:min(len(argv), 2)] {
		if strings.HasPrefix(filepath.Base(arg), "agentbox-") {
			return true
		}
	}
	return false
}

// shellPrograms are the programs a command line is a shell for.
var shellPrograms = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true, "ash": true}

// labelMost is how long a tree's label gets, in characters.
const labelMost = 200

func isShell(command string) bool {
	argv := strings.Fields(command)
	if len(argv) == 0 {
		return false
	}
	return shellPrograms[strings.TrimPrefix(filepath.Base(argv[0]), "-")]
}

// groupTrees groups processes in trees: each process whose parent is of
// another kind (or isn't in the machine) starts one, and takes every
// descendant of its own kind. The machine's own processes are one tree,
// whatever their parents, so the system shows once. The biggest come first.
func groupTrees(procs map[int]*MachineProcess) []ProcessTree {
	children := map[int][]int{}
	for pid, p := range procs {
		children[p.PPID] = append(children[p.PPID], pid)
	}
	for _, c := range children {
		slices.Sort(c)
	}
	var roots []int
	for pid, p := range procs {
		parent := procs[p.PPID]
		if parent == nil || parent.kind != p.kind {
			roots = append(roots, pid)
		}
	}
	slices.Sort(roots)
	var trees []ProcessTree
	var system *ProcessTree
	for _, pid := range roots {
		root := procs[pid]
		var members []MachineProcess
		var walk func(pid int)
		walk = func(pid int) {
			p := procs[pid]
			members = append(members, *p)
			for _, c := range children[pid] {
				if procs[c].kind == root.kind {
					walk(c)
				}
			}
		}
		walk(pid)
		if root.kind == TreeSystem {
			if system == nil {
				system = &ProcessTree{Kind: TreeSystem, Label: "System"}
			}
			system.Processes = append(system.Processes, members...)
			continue
		}
		t := ProcessTree{Kind: root.kind, Processes: members}
		t.Label = treeLabel(t, procs, children)
		trees = append(trees, t)
	}
	if system != nil {
		// The machine's init first, the tree's root.
		slices.SortStableFunc(system.Processes, func(a, b MachineProcess) int {
			return compareBool(a.MachinePID != 1, b.MachinePID != 1)
		})
		trees = append(trees, *system)
	}
	slices.SortStableFunc(trees, func(a, b ProcessTree) int {
		am, _, _ := a.Memory()
		bm, _, _ := b.Memory()
		switch {
		case am > bm:
			return -1
		case am < bm:
			return 1
		}
		return a.Root().PID - b.Root().PID
	})
	return trees
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// toolNames are the names the AI tools' trees are shown by, by a word their
// processes' command lines have.
var toolNames = []struct{ word, name string }{
	{"claude", "Claude Code"},
	{"codex", "Codex"},
	{"opencode", "OpenCode"},
	{"cursor-agent", "Cursor"},
	{"gemini", "Gemini CLI"},
	{"copilot", "Copilot"},
}

// treeLabel is the few words a tree is shown by: the AI tool's name for the
// tool's, and otherwise the command its root runs, the way it was typed.
func treeLabel(t ProcessTree, procs map[int]*MachineProcess, children map[int][]int) string {
	root := t.Root()
	if t.Kind == TreeTool {
		for _, p := range t.Processes {
			lower := strings.ToLower(p.Command)
			for _, tn := range toolNames {
				if strings.Contains(lower, tn.word) {
					return tn.name
				}
			}
		}
		return "AgentBox"
	}
	if strings.HasPrefix(root.Name, "containerd-shim") {
		// A Docker container: what runs in it says more than its shim.
		for _, c := range children[root.PID] {
			if p := procs[c]; p != nil {
				return "container: " + ShortCommand(p.Command)
			}
		}
	}
	return ShortCommand(root.Command)
}

// ShortCommand is a command line the way someone would have typed it: a
// shell's script rather than the shell (and the command Claude Code's Bash
// tool wraps in its eval rather than the wrapping), a program by its name
// rather than its path, and a Node or Python script by its own.
func ShortCommand(command string) string {
	argv := strings.Fields(command)
	if len(argv) == 0 {
		return command
	}
	if isShell(command) {
		if i := strings.Index(command, " -c "); i >= 0 {
			script := strings.TrimSpace(command[i+4:])
			// bash -c -l <script>: Claude Code's.
			script = strings.TrimSpace(strings.TrimPrefix(script, "-l "))
			if inner, ok := evalCommand(script); ok {
				return clip(inner, labelMost)
			}
			return clip(script, labelMost)
		}
	}
	name := filepath.Base(argv[0])
	rest := argv[1:]
	if runtimes[name] {
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			rest = rest[1:]
		}
		if len(rest) > 0 && strings.Contains(rest[0], "/") {
			name, rest = scriptName(rest[0]), rest[1:]
		}
	}
	return clip(strings.Join(append([]string{name}, rest...), " "), labelMost)
}

// runtimes are the programs that run a script named next on the command
// line.
var runtimes = map[string]bool{"node": true, "bun": true, "deno": true, "python": true, "python3": true, "ruby": true, "tsx": true, "ts-node": true}

// scriptName is a script's name: the file's, or a package's for a script
// inside node_modules (node_modules/vite/bin/vite.js is vite).
func scriptName(path string) string {
	if i := strings.LastIndex(path, "node_modules/"); i >= 0 {
		rest := path[i+len("node_modules/"):]
		parts := strings.Split(rest, "/")
		switch {
		case parts[0] == ".bin" && len(parts) > 1:
			return parts[1]
		case strings.HasPrefix(parts[0], "@") && len(parts) > 1:
			return parts[0] + "/" + parts[1]
		default:
			return parts[0]
		}
	}
	return filepath.Base(path)
}

// evalCommand finds the command in a Claude Code Bash tool's script, which
// sources its shell snapshot and then runs the command as eval '<command>',
// quoted for the shell.
func evalCommand(script string) (string, bool) {
	i := strings.Index(script, "eval ")
	if i < 0 {
		return "", false
	}
	word, ok := shellWord(script[i+len("eval "):])
	if !ok || strings.TrimSpace(word) == "" {
		return "", false
	}
	return word, true
}

// shellWord reads one shell word from the front of s: single-quoted,
// double-quoted and bare parts run together, as '"'"' puts a quote inside a
// single-quoted string.
func shellWord(s string) (string, bool) {
	var b strings.Builder
	i := 0
	for i < len(s) {
		switch c := s[i]; c {
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			b.WriteString(s[i+1 : i+1+end])
			i += end + 2
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return "", false
			}
			i++
		case ' ', '\t', '\n':
			return b.String(), true
		case '\\':
			if i+1 < len(s) {
				b.WriteByte(s[i+1])
			}
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), true
}

// readPSS reads processes' proportional memory from their smaps_rollup,
// which only their owner and root may read: what the daemon can't read
// itself, it reads with one sudo for all of them in AgentBox's VM, and goes
// without outside it.
func readPSS(proc string, pids []int) map[int]int64 {
	out := map[int]int64{}
	var denied []string
	for _, pid := range pids {
		path := filepath.Join(proc, strconv.Itoa(pid), "smaps_rollup")
		b, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				denied = append(denied, path)
			}
			continue
		}
		if n, ok := pssOf(string(b)); ok {
			out[pid] = n
		}
	}
	if len(denied) == 0 || !hostos.InVM() {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// -H names the file on each line, so a process that ended meanwhile
	// costs only its own line.
	b, _ := exec.CommandContext(ctx, "sudo", append([]string{"-n", "grep", "-sH", "^Pss:"}, denied...)...).Output()
	for pid, n := range parsePSSLines(proc, string(b)) {
		out[pid] = n
	}
	return out
}

// pssOf reads the Pss line of one smaps_rollup, in bytes.
func pssOf(rollup string) (int64, bool) {
	for _, line := range strings.Split(rollup, "\n") {
		if v, ok := strings.CutPrefix(line, "Pss:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")), 10, 64)
			return kb << 10, err == nil
		}
	}
	return 0, false
}

// parsePSSLines reads grep -H's "<proc>/<pid>/smaps_rollup:Pss: <n> kB" lines.
func parsePSSLines(proc, out string) map[int]int64 {
	got := map[int]int64{}
	for _, line := range strings.Split(out, "\n") {
		path, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		rel, err := filepath.Rel(proc, filepath.Dir(path))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(rel)
		if err != nil {
			continue
		}
		if n, ok := pssOf(rest); ok {
			got[pid] = n
		}
	}
	return got
}

// KillGrace is how long a killed tree has to end after SIGTERM before what
// is left of it gets SIGKILL.
const KillGrace = 5 * time.Second

// Killed is what killing a process tree did.
type Killed struct {
	// Tree is the tree as it was before the kill: the process asked for and
	// every descendant that isn't the machine's or the AI tool's.
	Tree ProcessTree
	// Forced is how many didn't end on SIGTERM within the grace and got
	// SIGKILL; Left is how many hadn't ended even then.
	Forced, Left int
}

// KillMachineProcess kills a process in an agent's machine, by its number
// there, with every process under it: SIGTERM to all, then SIGKILL to what
// is left after grace. A process of the machine's own or its AI tool's is
// refused with ErrProtectedProcess, as is one under them only by way of it.
func KillMachineProcess(ctx context.Context, instance string, machinePID int, grace time.Duration) (Killed, error) {
	return killMachineProcess(ctx, cgroupRoot, "/proc", instance, machinePID, grace, signalProcesses)
}

// signaler sends sig to processes, by number in the VM.
type signaler func(pids []int, sig syscall.Signal) error

func killMachineProcess(ctx context.Context, root, proc, instance string, machinePID int, grace time.Duration, signal signaler) (Killed, error) {
	procs, err := machineProcesses(root, proc, instance)
	if err != nil {
		return Killed{}, err
	}
	var target *MachineProcess
	for _, p := range procs {
		if p.MachinePID == machinePID {
			target = p
			break
		}
	}
	if target == nil {
		return Killed{}, fmt.Errorf("pid %d: %w", machinePID, ErrNoSuchProcess)
	}
	if target.kind != TreeAgent {
		whose := "the machine's own"
		if target.kind == TreeTool {
			whose = "the AI tool's own"
		}
		return Killed{}, fmt.Errorf("pid %d (%s) is %s, which the agent's chat needs: %w", machinePID, target.Name, whose, ErrProtectedProcess)
	}
	tree := subtree(procs, target)
	tree.Label = ShortCommand(target.Command)
	for _, t := range groupTrees(procs) {
		if t.Root().PID == target.PID {
			tree.Label = t.Label // a container's, say
		}
	}
	out := Killed{Tree: tree}
	pids := make([]int, len(tree.Processes))
	for i, p := range tree.Processes {
		pids[i] = p.PID
	}
	if err := signal(pids, syscall.SIGTERM); err != nil && !anyGone(proc, tree.Processes) {
		return out, fmt.Errorf("killing pid %d: %w", machinePID, err)
	}
	left := waitGone(ctx, proc, tree.Processes, grace)
	if len(left) == 0 {
		return out, nil
	}
	out.Forced = len(left)
	pids = pids[:0]
	for _, p := range left {
		pids = append(pids, p.PID)
	}
	_ = signal(pids, syscall.SIGKILL)
	out.Left = len(waitGone(ctx, proc, left, 2*time.Second))
	return out, nil
}

// subtree is p and every process under it the agent's, not the machine's
// or the tool's: a shell the dev server runs `claude` in keeps it.
func subtree(procs map[int]*MachineProcess, p *MachineProcess) ProcessTree {
	children := map[int][]int{}
	for pid, q := range procs {
		children[q.PPID] = append(children[q.PPID], pid)
	}
	t := ProcessTree{Kind: TreeAgent}
	var walk func(p *MachineProcess)
	walk = func(p *MachineProcess) {
		t.Processes = append(t.Processes, *p)
		c := children[p.PID]
		slices.Sort(c)
		for _, pid := range c {
			if q := procs[pid]; q.kind == TreeAgent {
				walk(q)
			}
		}
	}
	walk(p)
	return t
}

// alive says whether a process still runs: there, not a zombie, and the
// same one, not a later process given its number.
func alive(proc string, p MachineProcess) bool {
	b, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(p.PID), "stat"))
	if err != nil {
		return false
	}
	stat := string(b)
	if i := strings.LastIndexByte(stat, ')'); i >= 0 {
		if f := strings.Fields(stat[i+1:]); len(f) > 0 && (f[0] == "Z" || f[0] == "X") {
			return false
		}
	}
	_, start := parseStat(stat)
	return p.StartTicks == 0 || start == p.StartTicks
}

func anyGone(proc string, ps []MachineProcess) bool {
	return slices.ContainsFunc(ps, func(p MachineProcess) bool { return !alive(proc, p) })
}

// waitGone waits up to grace for processes to end, and gives the ones that
// haven't.
func waitGone(ctx context.Context, proc string, ps []MachineProcess, grace time.Duration) []MachineProcess {
	deadline := time.Now().Add(grace)
	for {
		var left []MachineProcess
		for _, p := range ps {
			if alive(proc, p) {
				left = append(left, p)
			}
		}
		if len(left) == 0 || !time.Now().Before(deadline) {
			return left
		}
		select {
		case <-ctx.Done():
			return left
		case <-time.After(100 * time.Millisecond):
		}
		ps = left
	}
}

// signalProcesses sends sig to processes, through sudo when they aren't the
// daemon's and it runs in AgentBox's VM (a machine's processes run as its
// own users). One that ended meanwhile isn't an error.
func signalProcesses(pids []int, sig syscall.Signal) error {
	var denied []string
	var firstErr error
	for _, pid := range pids {
		err := syscall.Kill(pid, sig)
		switch {
		case err == nil, errors.Is(err, syscall.ESRCH):
		case errors.Is(err, syscall.EPERM) && hostos.InVM():
			denied = append(denied, strconv.Itoa(pid))
		default:
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if len(denied) > 0 {
		name := "-TERM"
		if sig == syscall.SIGKILL {
			name = "-KILL"
		}
		// kill exits 1 when any of them ended meanwhile, having signalled
		// the rest: what is still there afterwards tells.
		_ = sudo("kill", "", append([]string{name, "--"}, denied...)...)
	}
	return firstErr
}

package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"agentbox/internal/hostos"
)

// Idle reclaim (internal/daemon/idlereclaim.go): an agent whose chat has
// been idle a while has what it left running frozen and its memory pushed
// out to swap, and woken again when something touches it. What stays
// running is what the machine and its chat need to take the next message:
// its init and system daemons, the AI tool with its adapter and MCP servers,
// and the processes those run under. Everything else in the machine — dev
// servers, the desktop's Xvnc and Chromium, watchers, Docker and its
// containers — is frozen.
//
// A cgroup whose processes all freeze is frozen where it is, so the
// machine's systemd keeps its units as they were. One that also holds what
// must keep running (the chat's session, which ran the dev server the agent
// left in the background) has the rest moved into a cgroup of their own
// beside the machine's, like a heavy run's, and moved back as they wake.
// What can't be moved can't be frozen without the chat, and is stopped
// instead when it is big enough to matter. Then the whole machine's memory
// is pushed out, the chat's included: what keeps running pages back in
// whatever it touches, and that is all.

// idleCgroup names the cgroup what idle reclaim moved is frozen in, beside
// the machine's own and its runs'.
const idleCgroup = "agentbox.idle"

// IdleStopMinRSS is how big a process tree that can't be frozen has to be
// for idle reclaim to stop it: anything smaller isn't worth losing.
const IdleStopMinRSS = 64 << 20

// IdleFreeze is what FreezeIdle did to one agent's machine, which ThawIdle
// undoes.
type IdleFreeze struct {
	// InPlace are the cgroups frozen where they were.
	InPlace []string `json:"inPlace,omitempty"`
	// Moved is where each process moved into the idle cgroup came from, by
	// its number in the VM.
	Moved map[int]string `json:"moved,omitempty"`
	// Frozen are the processes frozen, the biggest first.
	Frozen []IdleProcess `json:"frozen,omitempty"`
	// Stopped are the processes that couldn't be frozen and were stopped.
	Stopped []IdleProcess `json:"stopped,omitempty"`
}

// IdleProcess is one process idle reclaim froze or stopped.
type IdleProcess struct {
	PID     int    `json:"pid"`
	Name    string `json:"name"`
	Command string `json:"command"`
	RSS     int64  `json:"rss"`
}

// FreezeIdle freezes what an idle agent's machine left running, as the
// comment above says. A machine with nothing to freeze gives an empty
// IdleFreeze. It doesn't push memory out: ReclaimIdle does, after.
func FreezeIdle(instance string) (*IdleFreeze, error) {
	return freezeIdle(cgroupRoot, "/proc", instance, writeCgroup, stopProcess)
}

// idleProc is a process in an agent's machine as freezeIdle sees it.
type idleProc struct {
	IdleProcess
	ppid int
	dir  string
	keep bool
}

func freezeIdle(root, proc, instance string, write func(path, value string) error, stop func(pid int) error) (*IdleFreeze, error) {
	machine := agentCgroup(root, instance)
	procs, err := machineProcs(machine, proc)
	if err != nil {
		return nil, err
	}
	markKept(procs)
	out := &IdleFreeze{Moved: map[int]string{}}
	byDir := map[string][]*idleProc{}
	var dirs []string
	for _, p := range procs {
		if byDir[p.dir] == nil {
			dirs = append(dirs, p.dir)
		}
		byDir[p.dir] = append(byDir[p.dir], p)
	}
	slices.Sort(dirs)
	// A cgroup freezes with everything under it: one holding anything that
	// keeps running, itself or below, can't freeze where it is, nor can the
	// machine's own, which would pause the whole machine.
	holds := map[string]bool{machine: true}
	for _, p := range procs {
		if p.keep {
			for dir := p.dir; dir != machine && strings.HasPrefix(dir, machine); dir = filepath.Dir(dir) {
				holds[dir] = true
			}
		}
	}
	var move []*idleProc
	for _, dir := range dirs {
		inDir := byDir[dir]
		if !holds[dir] {
			if err := write(filepath.Join(dir, "cgroup.freeze"), "1"); err == nil {
				out.InPlace = append(out.InPlace, dir)
				for _, p := range inDir {
					out.Frozen = append(out.Frozen, p.IdleProcess)
				}
				continue
			}
			// A cgroup that won't freeze (a threaded one) has its processes
			// moved like a shared one's.
		}
		for _, p := range inDir {
			if !p.keep {
				move = append(move, p)
			}
		}
	}
	if len(move) > 0 {
		idle := filepath.Join(machine, idleCgroup)
		var stuck []*idleProc
		if err := makeCgroup(idle); err != nil {
			stuck = move
		} else {
			// Frozen before anything moves in, so nothing it holds runs
			// another moment where it lands.
			if err := write(filepath.Join(idle, "cgroup.freeze"), "1"); err != nil {
				stuck = move
			} else {
				for _, p := range move {
					if err := write(filepath.Join(idle, "cgroup.procs"), strconv.Itoa(p.PID)); err != nil {
						stuck = append(stuck, p)
						continue
					}
					out.Moved[p.PID] = p.dir
					out.Frozen = append(out.Frozen, p.IdleProcess)
				}
			}
		}
		out.Stopped = stopStuck(procs, stuck, stop)
	}
	slices.SortFunc(out.Frozen, func(a, b IdleProcess) int { return int(b.RSS - a.RSS) })
	return out, nil
}

// stopStuck stops the process trees idle reclaim couldn't freeze, those of
// them big enough to matter: each from its topmost process stuck, which
// takes what it started with it.
func stopStuck(procs map[int]*idleProc, stuck []*idleProc, stop func(pid int) error) []IdleProcess {
	isStuck := map[int]bool{}
	for _, p := range stuck {
		isStuck[p.PID] = true
	}
	tree := map[int]int64{}
	var tops []*idleProc
	for _, p := range stuck {
		top := p
		for parent := procs[top.ppid]; parent != nil && isStuck[parent.PID]; parent = procs[parent.ppid] {
			top = parent
		}
		if top == p {
			tops = append(tops, p)
		}
		tree[top.PID] += p.RSS
	}
	var out []IdleProcess
	for _, p := range tops {
		if tree[p.PID] < IdleStopMinRSS {
			continue
		}
		if err := stop(p.PID); err != nil {
			continue
		}
		ip := p.IdleProcess
		ip.RSS = tree[p.PID]
		out = append(out, ip)
	}
	return out
}

// machineProcs reads every process in a machine's cgroups but its runs' (the
// governor's) and the idle cgroup's (frozen already), by number in the VM.
func machineProcs(machine, proc string) (map[int]*idleProc, error) {
	procs := map[int]*idleProc{}
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
		if path != machine && (strings.HasPrefix(d.Name(), runCgroupPrefix) || d.Name() == idleCgroup) {
			return fs.SkipDir
		}
		b, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return nil
		}
		for _, f := range strings.Fields(string(b)) {
			pid, err := strconv.Atoi(f)
			if err != nil {
				continue
			}
			if p, ok := readIdleProc(proc, pid); ok {
				p.dir = path
				procs[pid] = p
			}
		}
		return nil
	})
	return procs, err
}

// readIdleProc reads a process's name, parent, command line and resident
// memory. A process that ended meanwhile isn't there.
func readIdleProc(proc string, pid int) (*idleProc, bool) {
	dir := filepath.Join(proc, strconv.Itoa(pid))
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return nil, false
	}
	p := &idleProc{IdleProcess: IdleProcess{PID: pid}}
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
			p.ppid, _ = strconv.Atoi(value)
		case "VmRSS":
			kb, _ := strconv.ParseInt(strings.TrimSuffix(value, " kB"), 10, 64)
			p.RSS = kb << 10
		}
	}
	if cmd, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		p.Command = strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))
	}
	return p, true
}

// systemProcs are the names of processes the machine itself needs, which
// idle reclaim leaves running: they are small, and the machine and its
// shells depend on them.
var systemProcs = map[string]bool{
	"init": true, "(sd-pam)": true, "dbus-daemon": true, "dbus-broker": true, "dbus-broker-lau": true,
	"polkitd": true, "agetty": true, "sshd": true, "cron": true, "rsyslogd": true,
	"incus-agent": true, "lxd-agent": true, "runuser": true, "su": true, "sudo": true, "login": true,
	"tmux": true, "tmux: server": true,
}

// chatWords are what an AI tool's processes, its adapter's and AgentBox's
// own in the machine have in their command lines.
var chatWords = []string{"claude", "agentclientprotocol", "codex", "opencode", "cursor-agent"}

// keepsRunning says whether a process is one the machine or its chat needs.
func keepsRunning(p *idleProc) bool {
	if systemProcs[p.Name] || strings.HasPrefix(p.Name, "systemd") {
		return true
	}
	// The program and the script it runs, not the rest: the shell Claude Code
	// runs a command in names its snapshot only as an argument. And not a
	// project's own tools: a dev server in a directory named for Claude is
	// still a dev server.
	argv := strings.Fields(p.Command)
	if len(argv) > 0 && filepath.Base(argv[0]) == "agentbox" {
		return true // its MCP servers, heavy-join: AgentBox's own in the machine
	}
	for _, arg := range argv[:min(len(argv), 2)] {
		if strings.Contains(arg, "/node_modules/.bin/") {
			continue
		}
		lower := strings.ToLower(arg)
		if slices.ContainsFunc(chatWords, func(w string) bool { return strings.Contains(lower, w) }) {
			return true
		}
	}
	return false
}

// markKept marks what keeps running: what keepsRunning names, the processes
// those run under, and the MCP servers the chat started.
func markKept(procs map[int]*idleProc) {
	for _, p := range procs {
		if !keepsRunning(p) {
			continue
		}
		for q := p; q != nil && !q.keep; q = procs[q.ppid] {
			q.keep = true
		}
	}
	for _, p := range procs {
		if parent := procs[p.ppid]; parent != nil && parent.keep && !p.keep && strings.Contains(strings.ToLower(p.Command), "mcp") {
			p.keep = true
		}
	}
}

// ThawIdle wakes what FreezeIdle froze: the processes it moved go back where
// they were, and every cgroup it froze resumes. Given nil, as after the
// daemon restarted, it wakes every cgroup in the machine frozen but the
// machine itself (paused as a whole) and its runs (the governor's), and what
// was moved stays where it is, awake.
func ThawIdle(instance string, f *IdleFreeze) error {
	return thawIdle(cgroupRoot, instance, f, writeCgroup)
}

func thawIdle(root, instance string, f *IdleFreeze, write func(path, value string) error) error {
	machine := agentCgroup(root, instance)
	idle := filepath.Join(machine, idleCgroup)
	var errs []error
	if f == nil {
		f = &IdleFreeze{InPlace: frozenCgroups(machine)}
	}
	// Moved back first, each process waking as it lands, so nothing a woken
	// process starts is left behind in the idle cgroup.
	pids := make([]int, 0, len(f.Moved))
	for pid := range f.Moved {
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	for _, pid := range pids {
		dir := f.Moved[pid]
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		_ = write(filepath.Join(dir, "cgroup.procs"), strconv.Itoa(pid)) // ended meanwhile, or its cgroup did
	}
	if _, err := os.Stat(filepath.Join(idle, "cgroup.freeze")); err == nil {
		if err := write(filepath.Join(idle, "cgroup.freeze"), "0"); err != nil {
			errs = append(errs, err)
		}
		if !cgroupPopulated(idle) {
			_ = removeCgroup(idle)
		}
	}
	for _, dir := range f.InPlace {
		if err := write(filepath.Join(dir, "cgroup.freeze"), "0"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// frozenCgroups are the cgroups under a machine's frozen of their own, but
// its runs'.
func frozenCgroups(machine string) []string {
	var out []string
	_ = filepath.WalkDir(machine, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == machine {
			return nil
		}
		if strings.HasPrefix(d.Name(), runCgroupPrefix) {
			return fs.SkipDir
		}
		if b, err := os.ReadFile(filepath.Join(path, "cgroup.freeze")); err == nil && strings.TrimSpace(string(b)) == "1" {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func cgroupPopulated(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "cgroup.events"))
	return err == nil && strings.Contains(string(b), "populated 1")
}

// ReclaimIdle pushes an idle agent's machine's memory out to swap, all of
// it, and drops its page cache: what keeps running pages back in what it
// touches. It says how much the machine held before and after.
func ReclaimIdle(instance string) (before, after int64, err error) {
	return reclaimIdle(cgroupRoot, instance, writeCgroup)
}

func reclaimIdle(root, instance string, write func(path, value string) error) (before, after int64, err error) {
	machine := agentCgroup(root, instance)
	if before, err = cgroupCurrent(machine); err != nil || before <= 0 {
		return before, before, err
	}
	if err := write(filepath.Join(machine, "memory.reclaim"), strconv.FormatInt(before, 10)); err != nil && !errors.Is(err, errReclaimShort) {
		return before, before, err
	}
	after, _ = cgroupCurrent(machine)
	return before, after, nil
}

func cgroupCurrent(dir string) (int64, error) {
	b, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// stopProcess asks a process to end (SIGTERM), through sudo when it isn't
// the daemon's and the daemon runs in AgentBox's VM.
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	err = p.Signal(syscall.SIGTERM)
	if err == nil || !errors.Is(err, fs.ErrPermission) || !hostos.InVM() {
		return err
	}
	return sudo("kill", "", "-TERM", strconv.Itoa(pid))
}

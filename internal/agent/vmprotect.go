package agent

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// What keeps AgentBox itself responsive in its VM whatever the agents do:
// the daemon, which every request of the app goes through, Incus, which it
// asks about every machine, the services that carry the app's requests in
// (sshd, the vsock forwarders, lxcfs) and the lead chats. The VM has no swap
// (zram.go), and agents' memory is bounded by their own memory.max and by
// agentbox.agents' memory.high and memory.max (sharedmemory.go), so an
// agent that wants more is throttled and then has a process killed. What was
// left was the kernel's reclaim: with the VM short, it took the daemon's and
// Incus's pages (their code, the state database's cache) as readily as an
// agent's, and they read them back from the disk the agents kept busy. On 19
// Nov 2026 the app froze along with the agents for minutes at a time.
//
//   - Memory protection (ProtectServices, daemon's scope): system.slice, where
//     Incus, sshd, lxcfs and the forwarders run, gets a memory.min and a
//     memory.low, and so do Incus and the daemon's own scope, and
//     agentbox.leads a memory.low: the
//     kernel takes from agents first, and from these only once nothing else
//     is left. A protection counts only if every cgroup above has one too,
//     unless cgroup2 is mounted with memory_recursiveprot, which then shares
//     a slice's among everything in it; the VM's isn't (RecursiveProt turns it
//     on), so the units that matter get their own as well.
//   - The daemon's own cgroup (AdoptDaemon): it ran in the cgroup of the ssh
//     session that started it, under user.slice, where no protection could
//     reach it. As it starts it moves itself into a scope of its own in
//     system.slice. Lead chats it starts are moved on into agentbox.leads.
//   - The OOM killer (OOMScorer): when the VM itself runs out, the kernel
//     kills the process with the highest score, which is mostly its size.
//     Agents' processes get AgentOOMScoreAdj on top, so one of theirs goes
//     first, never Incus, sshd or lxcfs; the daemon (and the lead chats it
//     starts, which inherit it) are at -900 (daemon/pressure.go).
//   - Swap: none for any of these (MemorySwapMax=0), should a swapfile be
//     given by hand.
//   - CPU and disk: agents' cpu.weight and io.weight are a tenth and a
//     quarter of everything else's (caps.go, ioweight.go); the daemon's
//     scope gets twice the default on top.
//
// All of it is written as the daemon starts, at runtime only, so a VM from
// any release gets it without being rebuilt, and nothing outlives the VM.

const (
	// ServicesMin and ServicesLow are system.slice's memory.min and
	// memory.low. What runs there — incusd, lxcfs, sshd, journald, the
	// forwarders, dbus — holds 300 to 600 MiB, and the daemon's scope is
	// there too, with its own (DaemonMin, DaemonLow). Protection only covers
	// what a cgroup uses, so a slice using less leaves the rest to the
	// agents.
	//
	// With no swap, a cgroup's anonymous memory can't be taken anyway: what
	// reclaim takes is its file pages, its code and its caches, and a
	// memory.min below its anonymous memory protects none of them. So each
	// floor covers what the cgroup holds in all, not its files alone. In a
	// lab on the VM's kernel (an agent filling its cgroup while a process
	// beside it read its 256 MiB working set again every 200 ms), a
	// memory.min short of that left the reader 430k refaults and 1.9 GiB
	// read back from the disk in a minute; one covering it, 370 MiB and a
	// p95 of 77 ms, against 5.9 GiB and 4.4 s unprotected with swap. And
	// memory.low gives way before an OOM kill does, which with no swap comes
	// soon: it is for what's beyond the floor.
	ServicesMin = int64(1536) << 20
	ServicesLow = int64(3) << 30
	// IncusMin is incus.service's memory.min, for a VM whose cgroup2 has no
	// memory_recursiveprot: incusd and what it caches of its database.
	IncusMin = int64(512) << 20
	// DaemonMin and DaemonLow are the daemon's scope's: what it holds after
	// a day (700 MB, lockmem_linux.go) and its code, which a memory.min
	// keeps even when only an OOM kill would do; its caches beyond that,
	// best effort.
	DaemonMin = int64(1) << 30
	DaemonLow = int64(2) << 30
	// DaemonWeight is the daemon's scope's cpu.weight and io.weight, twice
	// the default 100 of everything beside it.
	DaemonWeight = 200
	// AgentOOMScoreAdj is what agents' processes' oom_score_adj is raised to:
	// half of the whole VM's memory counted on top of each one's own, so a
	// small agent process scores above a large service.
	AgentOOMScoreAdj = 500
)

// cgroupMount is where cgroup2 is mounted.
const cgroupMount = "/sys/fs/cgroup"

// cgroup2Flags are the cgroup2 mount options a remount must name again to
// keep: the kernel clears any it isn't given.
var cgroup2Flags = []string{"nsdelegate", "favordynmods", "memory_localevents", "memory_recursiveprot", "memory_hugetlb_accounting", "pids_localevents"}

// RecursiveProt remounts cgroup2 with memory_recursiveprot when it isn't, and
// says what it did: "" when it was already. systemd mounts it so where the
// kernel allows it; the VM's kernel does, but its cgroup2 was mounted without.
func RecursiveProt() (string, error) {
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	opts, ok := recursiveProtOpts(string(mountinfo))
	if !ok {
		return "", nil
	}
	if opts == "" {
		return "", errors.New("cgroup2 isn't mounted at " + cgroupMount)
	}
	if err := sudo("mount", "", "-o", opts, cgroupMount); err != nil {
		return "", err
	}
	return "remounted cgroup2 with memory_recursiveprot, so a slice's memory protection covers what runs in it", nil
}

// recursiveProtOpts is the mount -o argument that remounts cgroup2 with
// memory_recursiveprot and every flag it has now, from mountinfo; ok is false
// when it has it already. opts is "" when cgroup2 isn't mounted.
func recursiveProtOpts(mountinfo string) (opts string, ok bool) {
	for _, line := range strings.Split(mountinfo, "\n") {
		// id parent major:minor root mountpoint options [optional...] - fstype source superoptions
		pre, post, found := strings.Cut(line, " - ")
		f, g := strings.Fields(pre), strings.Fields(post)
		if !found || len(f) < 6 || len(g) < 3 || f[4] != cgroupMount || g[0] != "cgroup2" {
			continue
		}
		have := map[string]bool{}
		for _, o := range strings.Split(g[2], ",") {
			have[o] = true
		}
		if have["memory_recursiveprot"] {
			return "", false
		}
		want := []string{"remount"}
		for _, o := range strings.Split(f[5], ",") {
			if o != "rw" && o != "ro" {
				want = append(want, o)
			}
		}
		for _, o := range cgroup2Flags {
			if have[o] || o == "memory_recursiveprot" {
				want = append(want, o)
			}
		}
		return strings.Join(want, ","), true
	}
	return "", true
}

// ProtectServices gives system.slice and Incus their memory protection, and
// takes swap from them, for as long as the VM runs (systemctl --runtime).
func ProtectServices() error {
	var errs []error
	for _, u := range serviceProtections() {
		if err := sudo("systemctl", "", append([]string{"set-property", "--runtime", u.unit}, u.props...)...); err != nil {
			if u.optional && strings.Contains(err.Error(), "not loaded") {
				continue
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type unitProtection struct {
	unit     string
	props    []string
	optional bool // a unit the VM may not have
}

func serviceProtections() []unitProtection {
	return []unitProtection{
		{unit: "system.slice", props: []string{
			"MemoryMin=" + strconv.FormatInt(ServicesMin, 10),
			"MemoryLow=" + strconv.FormatInt(ServicesLow, 10),
			"MemorySwapMax=0",
		}},
		{unit: "incus.service", optional: true, props: []string{
			"MemoryMin=" + strconv.FormatInt(IncusMin, 10),
			"MemorySwapMax=0",
		}},
	}
}

// DaemonScope is the name of the scope AdoptDaemon moves the daemon of pid
// into: a name of its own each time, as a daemon before it may have left
// something running in its own.
func DaemonScope(pid int) string { return "agentbox-daemon-" + strconv.Itoa(pid) + ".scope" }

// AdoptDaemon moves the daemon, running as pid, out of whatever cgroup
// started it and into a scope of its own in system.slice, with its memory
// protection and its weights (systemd's StartTransientUnit, which takes in a
// running process). What it starts from then on starts there.
func AdoptDaemon(pid int) error {
	if _, err := exec.LookPath("busctl"); err != nil {
		return errors.New("the VM has no busctl to ask systemd with")
	}
	return sudo("busctl", "", adoptArgs(pid)...)
}

func adoptArgs(pid int) []string {
	props := [][3]string{
		{"Description", "s", "AgentBox daemon"},
		{"Slice", "s", "system.slice"},
		{"MemoryMin", "t", strconv.FormatInt(DaemonMin, 10)},
		{"MemoryLow", "t", strconv.FormatInt(DaemonLow, 10)},
		{"MemorySwapMax", "t", "0"},
		{"CPUWeight", "t", strconv.Itoa(DaemonWeight)},
		{"IOWeight", "t", strconv.Itoa(DaemonWeight)},
	}
	args := []string{
		"call", "--quiet", "org.freedesktop.systemd1", "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager",
		"StartTransientUnit", "ssa(sv)a(sa(sv))", DaemonScope(pid), "fail",
		strconv.Itoa(len(props) + 1), "PIDs", "au", "1", strconv.Itoa(pid),
	}
	for _, p := range props {
		args = append(args, p[0], p[1], p[2])
	}
	return append(args, "0")
}

// OOMScorer raises the oom_score_adj of agents' processes to
// AgentOOMScoreAdj, through one helper running as root for as long as the
// daemon does, rather than a sudo for every look. The helper runs without
// CAP_SYS_RESOURCE: a privileged write would also set the process's floor
// (oom_score_adj_min), below which nothing it starts could go, and an
// agent's Docker sets its containers' to 0 and would fail to start them.
type OOMScorer struct {
	// Start starts the helper and gives its stdin, to which a line of pids
	// is written for each look; a variable for tests.
	Start func(adj int) (io.WriteCloser, error)
	// Proc is where /proc is, and a variable for tests.
	Proc string

	mu   sync.Mutex
	in   io.WriteCloser
	seen map[string]map[int]bool // by cgroup: pids already at AgentOOMScoreAdj
}

// NewOOMScorer is an OOMScorer for the VM's own processes.
func NewOOMScorer() *OOMScorer {
	return &OOMScorer{Start: startOOMHelper, Proc: "/proc"}
}

// oomHelperScript sets the oom_score_adj of every pid on each line it reads
// to $1; a process that has ended meanwhile is skipped.
const oomHelperScript = `exec 2>/dev/null; while read -r line; do for p in $line; do echo "$1" > /proc/"$p"/oom_score_adj; done; done`

func startOOMHelper(adj int) (io.WriteCloser, error) {
	args := []string{"setpriv", "--inh-caps=-sys_resource", "--bounding-set=-sys_resource", "sh", "-c", oomHelperScript, "sh", strconv.Itoa(adj)}
	if os.Geteuid() != 0 {
		args = append([]string{"sudo", "-n"}, args...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return in, nil
}

// Raise raises the oom_score_adj of every process in the agent machine of
// instance (its cgroup and every cgroup below) that is under
// AgentOOMScoreAdj, and says how many it raised. Each process is looked at
// once; what it starts inherits it, but for what Incus starts in it
// (`incus exec`), which inherits Incus's.
func (o *OOMScorer) Raise(instance string) (int, error) {
	return o.raise(instance, agentCgroup(cgroupRoot, instance))
}

func (o *OOMScorer) raise(key, dir string) (int, error) {
	pids, err := cgroupPids(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			o.Forget(key)
			return 0, nil
		}
		return 0, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.seen == nil {
		o.seen = map[string]map[int]bool{}
	}
	before := o.seen[key]
	now := make(map[int]bool, len(pids))
	var low []string
	for _, pid := range pids {
		if before[pid] {
			now[pid] = true
			continue
		}
		b, err := os.ReadFile(filepath.Join(o.Proc, strconv.Itoa(pid), "oom_score_adj"))
		if err != nil {
			continue // gone
		}
		if adj, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && adj < AgentOOMScoreAdj {
			low = append(low, strconv.Itoa(pid))
		}
		now[pid] = true
	}
	o.seen[key] = now
	if len(low) == 0 {
		return 0, nil
	}
	if o.in == nil {
		in, err := o.Start(AgentOOMScoreAdj)
		if err != nil {
			o.seen[key] = before
			return 0, fmt.Errorf("starting the helper that raises agents' OOM scores: %w", err)
		}
		o.in = in
	}
	if _, err := o.in.Write([]byte(strings.Join(low, " ") + "\n")); err != nil {
		// The helper is gone: the next look starts another and tries again.
		_ = o.in.Close()
		o.in = nil
		o.seen[key] = before
		return 0, err
	}
	return len(low), nil
}

// Forget drops what was seen of a machine that stopped.
func (o *OOMScorer) Forget(instance string) {
	o.mu.Lock()
	delete(o.seen, instance)
	o.mu.Unlock()
}

// Close ends the helper.
func (o *OOMScorer) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.in == nil {
		return nil
	}
	err := o.in.Close()
	o.in = nil
	return err
}

// cgroupPids is every process in the cgroup dir and those below it.
func cgroupPids(dir string) ([]int, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	var pids []int
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A cgroup removed while walking.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		f, err := os.Open(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return nil
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			if pid, err := strconv.Atoi(strings.TrimSpace(s.Text())); err == nil {
				pids = append(pids, pid)
			}
		}
		_ = f.Close()
		return nil
	})
	return pids, err
}

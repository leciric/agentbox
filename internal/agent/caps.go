package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Three more caps on an agent's machine's cgroup, beside memory.max
// (memorylimit.go) and io.weight (ioweight.go), each so one agent can't take
// the whole VM down with it, and so the PC it runs on doesn't freeze:
//
//   - memory.swap.max: memory.max bounds an agent's RAM, not its swap. An
//     agent at its limit with the VM's swap free isn't killed: the kernel
//     swaps its pages out to the VM's zram, which is RAM the agent isn't
//     charged for, and the machine crawls faulting them back. With no bound
//     it could fill all of it, and the VM went short for everyone. Now it
//     may swap a quarter of its memory.max (SwapLimit): past that its
//     process is killed at once, as memory.max meant. That is still room
//     for a frozen idle agent's or a paused run's cold memory to go out to
//     swap (reclaim.go), which is what the VM's swap is for.
//   - cpu.weight: AgentCPUWeight, against the 100 of system.slice (Incus,
//     the daemon when it is a service), user.slice and init.scope, which are
//     the agents' machines' siblings under the cgroup root. A build on every
//     core no longer gets the CPU before the daemon answers the app or Incus
//     answers it. It is a weight, not a cap: an idle VM's CPU is all the
//     agents'.
//   - pids.max: AgentPidsMax tasks (processes and threads) in one machine,
//     so a fork bomb or a test that leaks processes fails inside it rather
//     than fill the VM's process table and its kernel memory.
//
// Incus has keys for each (limits.memory.swap, limits.cpu.priority,
// limits.processes), but earlier releases' are taken off every machine
// (oldlimits.go), and a key needs Incus to answer, the one thing a VM short of
// memory or CPU doesn't do; these are written live, every few seconds, from
// the moment a machine's cgroup is there (internal/daemon/pressure.go).

// AgentCPUWeight is an agent's machine's cpu.weight, against the default 100
// of everything else under the cgroup root. Ten agents busy together weigh as
// much as the VM's own services: they get the cores the daemon and Incus
// leave, and the daemon gets the ones it asks for at once.
const AgentCPUWeight = 10

// AgentPidsMax is the most tasks one agent's machine may run. A busy agent —
// its AI tool, a browser, Docker with a few services, a parallel build or
// test run — uses one or two thousand; a fork bomb reaches this in well
// under a second, and then every fork in it fails.
const AgentPidsMax = 8192

// Caps is what SetCaps writes to an agent's machine's cgroup. A zero field
// is left as it is.
type Caps struct {
	SwapMax   int64 // memory.swap.max in bytes; -1 for none ("max")
	CPUWeight int   // cpu.weight, 1 to 10000
	PidsMax   int   // pids.max
}

// AgentCaps is the caps for an agent whose memory.max is memoryLimit, in a
// VM with swapTotal bytes of swap and an agent memory share of share percent
// (MemoryLimit's). swapTotal below 0 is swap that couldn't be read: its cap
// is left as it is.
func AgentCaps(memoryLimit, swapTotal int64, share int) Caps {
	c := Caps{CPUWeight: AgentCPUWeight, PidsMax: AgentPidsMax}
	if swapTotal >= 0 {
		c.SwapMax = SwapLimit(memoryLimit, swapTotal, share)
	}
	return c
}

// SwapDivisor is how much smaller an agent's memory.swap.max is than its
// memory.max: a quarter. With the default half of the VM each, an agent may
// put an eighth of the VM's size in its zram (which zram's half of the VM
// holds four of), costing a few percent of its RAM once compressed.
const SwapDivisor = 4

// SwapLimit is memory.swap.max for an agent whose memory.max is
// memoryLimit, in a VM with swapTotal bytes of swap: a SwapDivisor-th of
// memoryLimit, and never more than the agent's share of that swap (a VM with
// a small swapfile and no zram). A VM with no swap gives -1, none, so the
// limit doesn't stand in the way when it gets some (`agentbox vm swap on`, or
// zram as the daemon starts) before the next look; there is nothing to swap
// to meanwhile. So does an agent with no memory.max.
func SwapLimit(memoryLimit, swapTotal int64, share int) int64 {
	if swapTotal <= 0 || memoryLimit <= 0 {
		return -1
	}
	// MemoryLimit is the same share of whatever total it is given.
	return max(min(memoryLimit/SwapDivisor, MemoryLimit(swapTotal, share)), 4096)
}

// VMSwap is the VM's swap in bytes, from /proc/meminfo; -1 when it can't be
// read.
func VMSwap() int64 {
	total, _, err := hostSwap()
	if err != nil {
		return -1
	}
	return total
}

// SetCaps writes caps to an agent's machine's cgroup. It writes only what
// changes, and skips a file the kernel doesn't have (a controller not
// enabled) and a machine whose cgroup isn't there: stopped, or not on
// cgroup2.
func SetCaps(instance string, caps Caps) error {
	return setCaps(agentCgroup(cgroupRoot, instance), caps, writeCgroup)
}

func setCaps(dir string, caps Caps, write func(path, value string) error) error {
	var errs []error
	if caps.SwapMax != 0 {
		limit := caps.SwapMax
		if limit < 0 {
			limit = 0 // setCgroupLimit's "max"
		}
		errs = append(errs, setCgroupLimit(dir, "memory.swap.max", limit, write))
	}
	if caps.CPUWeight > 0 {
		errs = append(errs, setCgroupValue(dir, "cpu.weight", strconv.Itoa(caps.CPUWeight), write))
	}
	if caps.PidsMax > 0 {
		errs = append(errs, setCgroupValue(dir, "pids.max", strconv.Itoa(caps.PidsMax), write))
	}
	return errors.Join(errs...)
}

// setCgroupValue writes want to a cgroup file unless it holds it already,
// and does nothing when the file isn't there.
func setCgroupValue(dir, file, want string, write func(path, value string) error) error {
	path := filepath.Join(dir, file)
	have, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(have)) == want {
		return nil
	}
	return write(path, want)
}

package hostvm

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// The least a VM may be resized to: the daemon, Incus and one agent with its
// Docker and its browser. DefaultSize never goes below two CPUs either.
const (
	MinCPUs   = 2
	MinMemory = 4 << 30
	// macReserve is the memory left to the Mac itself, with the app and an
	// editor on it: a VM given all of it pushes the Mac into swap.
	macReserve = 2 << 30
)

// Limits are the sizes `agentbox vm resize` takes on this machine, which the
// app's controls are bounded by.
type Limits struct {
	MinCPUs   int   `json:"minCpus"`
	MaxCPUs   int   `json:"maxCpus"`
	MinMemory int64 `json:"minMemory"` // bytes
	MaxMemory int64 `json:"maxMemory"` // bytes
}

// HostLimits are the Limits for this machine: every core it has, and all of
// its memory but what the Mac keeps for itself. A machine too small for the
// minimum gets the minimum, and Lima is left to say whether it can.
func HostLimits() Limits {
	return limitsFor(runtime.NumCPU(), hostMemory())
}

func limitsFor(cpus int, memory int64) Limits {
	l := Limits{MinCPUs: MinCPUs, MaxCPUs: max(cpus, MinCPUs), MinMemory: MinMemory, MaxMemory: MinMemory}
	if memory > 0 {
		l.MaxMemory = max(memory-macReserve, MinMemory)
	}
	return l
}

// Check says what's wrong with cpus and memory (bytes) for this machine; zero
// is one left as it is.
func (l Limits) Check(cpus int, memory int64) error {
	var errs []error
	if cpus != 0 && (cpus < l.MinCPUs || cpus > l.MaxCPUs) {
		errs = append(errs, fmt.Errorf("%d CPUs: the VM takes %d to %d on this machine", cpus, l.MinCPUs, l.MaxCPUs))
	}
	if memory != 0 && (memory < l.MinMemory || memory > l.MaxMemory) {
		errs = append(errs, fmt.Errorf("%s of memory: the VM takes %s to %s on this machine, which keeps %s for itself",
			sizeWords(memory), sizeWords(l.MinMemory), sizeWords(l.MaxMemory), sizeWords(macReserve)))
	}
	return errors.Join(errs...)
}

// ParseMemory reads a memory size the way `vm init --memory` and Lima take
// one: 6GiB, 6G, 6GB, 6144MiB or 6144M, or a bare number of GiB. Lima counts
// every one of them in powers of two, and so does this.
func ParseMemory(s string) (int64, error) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	num, unit := s, ""
	if i >= 0 {
		num, unit = s[:i], strings.TrimSpace(s[i:])
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q isn't a memory size: say it like 8GiB", s)
	}
	var scale float64
	switch strings.ToLower(unit) {
	case "", "g", "gb", "gib":
		scale = 1 << 30
	case "m", "mb", "mib":
		scale = 1 << 20
	case "t", "tb", "tib":
		scale = 1 << 40
	default:
		return 0, fmt.Errorf("%q isn't a memory size: say it like 8GiB", s)
	}
	return int64(n * scale), nil
}

// sizeWords is a size the way a person says it: 6GiB, 6.5GiB, 512MiB.
func sizeWords(bytes int64) string {
	if bytes%(1<<30) == 0 || bytes >= 10<<30 {
		return strconv.FormatFloat(float64(bytes)/(1<<30), 'f', -1, 64) + "GiB"
	}
	if bytes >= 1<<30 {
		return strconv.FormatFloat(float64(bytes)/(1<<30), 'f', 1, 64) + "GiB"
	}
	return strconv.FormatInt(bytes>>20, 10) + "MiB"
}

// Resize gives the VM cpus CPUs and memory bytes of memory; zero leaves one as
// it is. Lima only edits a stopped VM, so a running one is stopped — and every
// agent with it — edited, started again, and its daemon restarted, the way
// `vm upgrade` restarts it. A stopped VM is edited and left stopped: it has
// the new size when it next starts.
func (v *VM) Resize(ctx context.Context, cpus int, memory int64) error {
	unlock, err := v.lock(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := v.State(ctx)
	if err != nil {
		return err
	}
	switch {
	case !st.Exists:
		return ErrNotCreated
	case st.Status == "Broken":
		return fmt.Errorf("Lima says AgentBox's VM is broken, so it can't be resized: see limactl list")
	}
	if (cpus == 0 || cpus == st.CPUs) && (memory == 0 || memory == st.Memory) {
		fmt.Fprintf(v.Log, "AgentBox's VM already has %d CPUs and %s of memory.\n", st.CPUs, sizeWords(st.Memory))
		return nil
	}
	running := st.Status == "Running"
	if running {
		fmt.Fprintln(v.Log, "==> Stopping AgentBox's VM, and every agent in it")
		if err := v.Stop(ctx); err != nil {
			return err
		}
	}
	args := []string{"edit", "--tty=false"}
	if cpus != 0 {
		args = append(args, "--cpus", strconv.Itoa(cpus))
	} else {
		cpus = st.CPUs
	}
	if memory != 0 {
		// Lima's --memory is a number of GiB.
		args = append(args, "--memory", strconv.FormatFloat(float64(memory)/(1<<30), 'f', -1, 64))
	} else {
		memory = st.Memory
	}
	fmt.Fprintf(v.Log, "==> Giving the VM %d CPUs and %s of memory\n", cpus, sizeWords(memory))
	if err := v.limaLog(ctx, append(args, v.Name)...); err != nil {
		if running {
			// Put back what was running, at the size it had.
			return errors.Join(err, v.Start(ctx), v.restartDaemon(ctx))
		}
		return err
	}
	if !running {
		fmt.Fprintln(v.Log, "The VM is stopped: it has the new size when it next starts.")
		return nil
	}
	if err := v.Start(ctx); err != nil {
		return err
	}
	if err := v.ready(ctx); err != nil {
		return err
	}
	return v.restartDaemon(ctx)
}

// restartDaemon stops the VM's daemon, if one runs, and starts it again with
// the host's settings.
func (v *VM) restartDaemon(ctx context.Context) error {
	fmt.Fprintln(v.Log, "==> Starting the daemon")
	// A daemon that isn't running has nothing to stop.
	v.shellLog(ctx, vmBinary, "daemon", "stop")
	return v.shellLog(ctx, append([]string{"env"}, append(v.forwardEnv(), vmBinary, "daemon", "start")...)...)
}

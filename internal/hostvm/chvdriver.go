package hostvm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostvm/chv"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// CHV is the Cloud Hypervisor side of a VM, on a Linux host (package chv):
// the VM as `agentbox vm init` made it, and where its files are.
type CHV struct {
	Config chv.Config
	Layout chv.Layout
	// Self is this agentbox. It is also the agentbox the VM runs, since host
	// and VM are both Linux on the same architecture, and ssh's ProxyCommand
	// (`agentbox vm proxy 22`).
	Self string
}

// The parts of package chv this package runs, as variables so its tests can
// stand in for a VM (and for ssh) where there's none to run.
var (
	chvEnsureTools     = chv.EnsureTools
	chvMakeDisks       = chv.MakeDisks
	chvStart           = chv.Start
	chvWaitProvisioned = chv.WaitProvisioned
	chvStop            = chv.Stop
	chvStatus          = chv.Status
	chvSSHArgs         = chv.SSHArgs
)

// self is this executable, with no symlinks in its path.
func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// newCHV is New on a Linux host: the VM from its Config, or ErrNotCreated with
// the VM's name and the host's paths set when there's none.
func newCHV() (*VM, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	name := env("AGENTBOX_VM", chv.DefaultName)
	c, err := chv.Load(p, name)
	if errors.Is(err, chv.ErrNotCreated) {
		return &VM{Name: name, Paths: p, Log: os.Stderr}, ErrNotCreated
	}
	if err != nil {
		return nil, err
	}
	return NewCHV(p, c)
}

// NewCHV describes the Cloud Hypervisor VM c, on a host with paths p.
func NewCHV(p paths.Paths, c chv.Config) (*VM, error) {
	exe, err := self()
	if err != nil {
		return nil, err
	}
	return &VM{
		Name:   c.Name,
		Home:   c.Home,
		Paths:  p,
		Binary: exe,
		Log:    os.Stderr,
		CHV:    &CHV{Config: c, Layout: chv.NewLayout(p, c.Name), Self: exe},
	}, nil
}

// linuxReserve is what a Linux host keeps of its memory for itself, its
// desktop and the app, when a VM's cap is worked out for it.
const linuxReserve = 4 * chv.GiB

// DefaultConfig is the VM `agentbox vm init` makes for a user on a host with
// cpus cores and memory bytes of memory: DefaultSize's CPUs; chv's minimum to
// boot with, growing as agents need it to three quarters of the host's memory
// but never to more than all of it less linuxReserve; chv's pool disk. The
// VM's user is the host's, with the same IDs, and its home is its own, beside
// the host's (which the VM has at the same path, shared).
func DefaultConfig(name, username string, uid, gid int, home string, cpus int, memory int64) chv.Config {
	// In whole GiB, which is how people say it back (vm resize --memory-cap).
	capacity := min(memory-linuxReserve, memory/4*3) / chv.GiB * chv.GiB
	return chv.Config{
		Name:      name,
		CPUs:      defaultCPUs(cpus),
		MemoryMin: chv.DefaultMemoryMin,
		MemoryCap: max(capacity, chv.DefaultMemoryMin),
		Disk:      chv.DefaultDisk,
		User:      username,
		UID:       uid,
		GID:       gid,
		Home:      home,
		GuestHome: "/home/" + username + ".linux",
	}
}

// currentConfig is DefaultConfig for whoever runs this, on this machine.
func currentConfig(name string) (chv.Config, error) {
	u, err := user.Current()
	if err != nil {
		return chv.Config{}, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return chv.Config{}, fmt.Errorf("user %s's uid %q: %w", u.Username, u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return chv.Config{}, fmt.Errorf("user %s's gid %q: %w", u.Username, u.Gid, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return chv.Config{}, err
	}
	// The share's path as the VM sees it, with no symlinks in it.
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	return DefaultConfig(name, u.Username, uid, gid, home, numCPU(), hostMemory()), nil
}

// checkConfig says what's wrong with the sizes asked of `vm init`.
func checkConfig(c chv.Config, hostCPUs int, hostMem int64) error {
	var errs []error
	if c.CPUs < 1 || c.CPUs > max(hostCPUs, 1) {
		errs = append(errs, fmt.Errorf("%d CPUs: the VM takes 1 to %d on this machine", c.CPUs, max(hostCPUs, 1)))
	}
	if c.MemoryMin < 1*chv.GiB {
		errs = append(errs, fmt.Errorf("--memory-min %s: the VM needs at least 1GiB to boot, and %s to run agents", sizeWords(c.MemoryMin), sizeWords(chv.DefaultMemoryMin)))
	}
	if c.MemoryCap < c.MemoryMin {
		errs = append(errs, fmt.Errorf("--memory-cap %s is less than --memory-min %s", sizeWords(c.MemoryCap), sizeWords(c.MemoryMin)))
	}
	if hostMem > 0 && c.MemoryCap > hostMem {
		errs = append(errs, fmt.Errorf("--memory-cap %s is more than this machine has (%s)", sizeWords(c.MemoryCap), sizeWords(hostMem)))
	}
	if c.Disk < 20*chv.GiB {
		errs = append(errs, fmt.Errorf("--disk %s: the VM's pool needs at least 20GiB", sizeWords(c.Disk)))
	}
	return errors.Join(errs...)
}

// hostModeInUse says why this Linux machine can't be switched to a VM yet, or
// nil: a daemon of its own answering on the socket (the VM's daemon would be
// forwarded to the same one), or agents of its own. Those agents run in Incus
// on this machine, and stay there: nothing moves them into the VM, and the
// VM's agents would get worktrees where theirs are. So the switch waits until
// the user has stopped the daemon and removed them, rather than doing either.
func hostModeInUse(ctx context.Context, p paths.Paths) error {
	var why []string
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if api.NewClient(p.Socket()).Ping(ctx) == nil {
		why = append(why, fmt.Sprintf("Its daemon is running (%s): stop it first, with agentbox daemon stop, and agentbox daemon uninstall if you made it a service.", p.Socket()))
	}
	if agents := hostAgents(p); len(agents) > 0 {
		shown := agents
		if len(shown) > 3 {
			shown = append(shown[:3:3], "…")
		}
		why = append(why, fmt.Sprintf("It has %d agent(s) of its own (%s), which stay on this machine and aren't moved into the VM: finish with them and remove them first, with agentbox destroy <project/agent>.", len(agents), strings.Join(shown, ", ")))
	}
	if len(why) == 0 {
		return nil
	}
	return fmt.Errorf("this machine runs AgentBox itself (host setup), so it isn't switched to a VM:\n  - %s\nThen run agentbox vm init again", strings.Join(why, "\n  - "))
}

// hostAgents are the agents this machine has worktrees for, as project/agent:
// every agent has one, where the VM's agents would have theirs
// (paths.Worktree). A project's lead has one too, which it resets whenever it
// starts, so the VM's lead can have it.
func hostAgents(p paths.Paths) []string {
	projects, err := os.ReadDir(p.Worktrees())
	if err != nil {
		return nil
	}
	var agents []string
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(p.Worktrees(), project.Name()))
		if err != nil {
			continue
		}
		for _, agent := range entries {
			if agent.IsDir() && agent.Name() != state.LeadName {
				agents = append(agents, project.Name()+"/"+agent.Name())
			}
		}
	}
	return agents
}

// checkKVM says why this user can't run a VM, or nil.
func checkKVM() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("the VM needs /dev/kvm, which this user can't open (%w): turn virtualization on in the firmware, and join the kvm group (sudo usermod -aG kvm $USER, then log in again)", err)
	}
	return f.Close()
}

// initCHV is `agentbox vm init` on a Linux host: it makes the VM want asks
// for, unless there's one, then fetches what runs it, makes its disks, boots
// it, waits for its first boot to set it up, and sets AgentBox up in it. Like
// Lima's, it is safe to run again: a step already done is quick. Every step
// says how long it took.
func initCHV(ctx context.Context, p paths.Paths, want chv.Config, sizesSet bool, log io.Writer) error {
	began := time.Now()
	c, err := chv.Load(p, want.Name)
	made := false
	switch {
	case errors.Is(err, chv.ErrNotCreated):
		// Nothing is written before these pass: until the Config exists this
		// machine stays as it was.
		if err := hostModeInUse(ctx, p); err != nil {
			return err
		}
		if err := checkKVM(); err != nil {
			return err
		}
		c = want
		c.Created = time.Now().UTC()
		if err := c.Save(p); err != nil {
			return err
		}
		made = true
		_, _ = fmt.Fprintf(log, "==> AgentBox's VM %s: %d CPUs, %s of memory growing to at most %s, a %s disk (%s)\n",
			c.Name, c.CPUs, sizeWords(c.MemoryMin), sizeWords(c.MemoryCap), sizeWords(c.Disk), chv.ConfigFile(p, c.Name))
	case err != nil:
		return err
	case sizesSet:
		_, _ = fmt.Fprintln(log, "note: AgentBox's VM exists already and keeps its size: change it with agentbox vm resize")
	}
	vm, err := NewCHV(p, c)
	if err != nil {
		return err
	}
	vm.Log = log
	err = vm.CHV.setUp(ctx, vm)
	if err != nil && made {
		err = fmt.Errorf("%w\nThis machine now runs AgentBox in a VM: run agentbox vm init again to carry on, or agentbox vm delete --yes to go back", err)
	}
	if err == nil {
		_, _ = fmt.Fprintf(log, "==> AgentBox's VM is set up (took %s in all)\n", time.Since(began).Round(100*time.Millisecond))
	}
	return err
}

func (h *CHV) setUp(ctx context.Context, v *VM) error {
	// Commands waiting to use the VM (the app's agentbox daemon start) wait
	// for this instead of booting it under it.
	unlock, err := v.lock(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	for _, step := range []struct {
		what string
		do   func() error
	}{
		{"Fetching the programs that run the VM", func() error { return chvEnsureTools(ctx, h.Layout, v.Log) }},
		{"Making the VM's disks", func() error { return chvMakeDisks(ctx, h.Config, h.Layout, v.Log) }},
		{"Starting the VM", func() error { return chvStart(ctx, h.Config, h.Layout, v.Paths, v.Log) }},
		{"Waiting for the VM's first boot to set it up", func() error { return chvWaitProvisioned(ctx, h.Config, h.Layout, v.Log) }},
	} {
		_, _ = fmt.Fprintf(v.Log, "==> %s\n", step.what)
		start := time.Now()
		if err := step.do(); err != nil {
			return err
		}
		v.took(start)
	}
	return v.Setup(ctx)
}

// up starts the VM unless it runs, or resumes it when it's paused.
func (h *CHV) up(ctx context.Context, v *VM) error {
	switch st := chvStatus(ctx, h.Config, h.Layout, v.Paths); st.State {
	case api.VMRunning:
		return nil
	case api.VMMissing:
		if st.Problem != "" {
			return errors.New(st.Problem)
		}
		return ErrNotCreated
	}
	return h.start(ctx, v)
}

func (h *CHV) start(ctx context.Context, v *VM) error {
	if chvStatus(ctx, h.Config, h.Layout, v.Paths).State == api.VMPaused {
		_, _ = fmt.Fprintf(v.Log, "==> Resuming AgentBox's VM (%s)\n", v.Name)
		return chvStart(ctx, h.Config, h.Layout, v.Paths, v.Log)
	}
	_, _ = fmt.Fprintf(v.Log, "==> Starting AgentBox's VM (%s)\n", v.Name)
	// What an update of AgentBox changed reaches a VM made by an older one
	// at its next start: programs at new pins, and a seed with the new
	// user-data, which cloud-init applies on that boot (its instance-id is
	// the seed's hash). Both do nothing when nothing changed.
	if err := chvEnsureTools(ctx, h.Layout, v.Log); err != nil {
		return err
	}
	if err := chvMakeDisks(ctx, h.Config, h.Layout, v.Log); err != nil {
		return err
	}
	return chvStart(ctx, h.Config, h.Layout, v.Paths, v.Log)
}

// daemonUp starts the VM's daemon unless it answers already, and waits until
// it answers on this machine's socket, where the VM's is forwarded: `vm start`
// and `vm resume` return then, so the app can use it at once.
func (v *VM) daemonUp(ctx context.Context) error {
	c := api.NewClient(v.Paths.Socket())
	ping := func(timeout time.Duration) bool {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return c.Ping(ctx) == nil
	}
	if ping(2 * time.Second) {
		return nil
	}
	_, _ = fmt.Fprintln(v.Log, "==> Starting the daemon")
	if err := v.execLog(ctx, append([]string{"env"}, append(v.forwardEnv(), vmBinary, "daemon", "start")...)...); err != nil {
		return err
	}
	for deadline := time.Now().Add(daemonWait); ; {
		if ping(2 * time.Second) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the VM's daemon runs, but doesn't answer on %s", v.Paths.Socket())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// daemonWait is how long daemonUp waits for the VM's daemon to be forwarded.
var daemonWait = 60 * time.Second

// exec runs argv in the VM over ssh, in the VM user's own home, and returns
// what it printed.
func (h *CHV) exec(ctx context.Context, v *VM, stdin io.Reader, argv []string) (string, error) {
	args := chvSSHArgs(h.Config, h.Layout, h.Self, h.Config.GuestHome, false, argv)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("in the VM, %s: %w: %s", argv[0], err, lastLine(msg))
		}
		return stdout.String(), fmt.Errorf("in the VM, %s: %w", strings.Join(argv, " "), err)
	}
	return stdout.String(), nil
}

// execLog runs argv in the VM with its output going to the log.
func (h *CHV) execLog(ctx context.Context, v *VM, argv []string) error {
	args := chvSSHArgs(h.Config, h.Layout, h.Self, h.Config.GuestHome, false, argv)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = v.Log, v.Log
	if err := cmd.Run(); err != nil {
		name := argv[0]
		if name == "sudo" || name == "env" {
			name = strings.Join(argv[:min(len(argv), 3)], " ")
		}
		return fmt.Errorf("in the VM, %s: %w", name, err)
	}
	return nil
}

// delete stops the VM and removes it: its disks, its ssh key and its Config,
// which puts this machine back to running AgentBox itself (host setup). The
// programs that run VMs and the downloads they're made from stay, under
// paths.VM, for the next.
func (h *CHV) delete(ctx context.Context, v *VM) error {
	unlock, err := v.lock(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	switch chvStatus(ctx, h.Config, h.Layout, v.Paths).State {
	case api.VMOff, api.VMMissing:
	default:
		_, _ = fmt.Fprintln(v.Log, "==> Stopping AgentBox's VM, and every agent in it")
		if err := chvStop(ctx, h.Config, h.Layout, v.Paths, false, v.Log); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(v.Log, "==> Removing %s\n", h.Layout.Dir())
	if err := os.RemoveAll(h.Layout.Dir()); err != nil {
		return err
	}
	if err := os.Remove(chv.ConfigFile(v.Paths, v.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _ = fmt.Fprintln(v.Log, "AgentBox's VM is gone: this machine runs AgentBox itself again, once it's set up for it (agentbox host setup).")
	return nil
}

// resize gives the VM cpus CPUs and a memory cap of capacity bytes; zero
// leaves one as it is. The Config always changes, for the VM's next start. A
// running VM changes too, without a restart, when the new size fits in the
// room it booted with (chv.Room: every core and all of the host's memory,
// for a VM started by this agentbox): its supervisor hotplugs the CPUs and
// moves the memory policy's cap, and the daemon in it is restarted to see
// them, which leaves every agent running. A size that doesn't fit, or a VM
// started by an older agentbox, needs the VM restarted, which stops every
// agent: with restart, resize does it; without, it says so and stops there.
func (h *CHV) resize(ctx context.Context, v *VM, cpus int, capacity int64, restart bool) error {
	unlock, err := v.lock(ctx, true)
	if err != nil {
		return err
	}
	c := h.Config
	if cpus != 0 {
		c.CPUs = cpus
	}
	if capacity != 0 {
		if capacity < c.MemoryMin {
			unlock()
			return fmt.Errorf("a memory cap of %s is less than the %s the VM boots with", sizeWords(capacity), sizeWords(c.MemoryMin))
		}
		c.MemoryCap = capacity
	}
	if c != h.Config {
		if err := c.Save(v.Paths); err != nil {
			unlock()
			return err
		}
		h.Config = c
	}
	size := fmt.Sprintf("%d CPUs and a memory cap of %s", c.CPUs, sizeWords(c.MemoryCap))
	st := chvStatus(ctx, c, h.Layout, v.Paths)
	switch st.State {
	case api.VMOff, api.VMMissing:
		unlock()
		_, _ = fmt.Fprintf(v.Log, "AgentBox's VM has %s when it next starts.\n", size)
		return nil
	}
	wantCap := chv.CapFor(c)
	if st.CPUs == c.CPUs && st.Memory.Cap == wantCap {
		unlock()
		_, _ = fmt.Fprintf(v.Log, "AgentBox's VM has %s already.\n", size)
		return nil
	}
	if st.State != api.VMPaused && fitsLive(st.Live, c.CPUs, c.MemoryCap) {
		_, _ = fmt.Fprintf(v.Log, "==> Giving AgentBox's VM %s, while it runs\n", size)
		err := chv.Resize(ctx, h.Layout, v.Paths, api.VMResizeRequest{CPUs: c.CPUs, MemoryCap: c.MemoryCap})
		unlock()
		if err != nil {
			return err
		}
		// The daemon reads the VM's cores and memory cap as it starts: a
		// new one sees the new size. Agents keep running.
		if err := v.writeProfile(ctx); err != nil {
			return err
		}
		if err := v.restartDaemon(ctx); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(v.Log, "AgentBox's VM has %s now, and every agent kept running.\n", size)
		return nil
	}
	unlock()
	if !restart {
		_, _ = fmt.Fprintf(v.Log, "AgentBox's VM has %s from when it next starts: it can't change that much while it runs. Run this again with --restart to restart it now, which stops every agent.\n", size)
		return nil
	}
	_, _ = fmt.Fprintf(v.Log, "==> Restarting AgentBox's VM to give it %s: every agent in it stops\n", size)
	if err := v.Stop(ctx, true); err != nil {
		return err
	}
	if err := v.Ready(ctx); err != nil {
		return err
	}
	if err := v.writeProfile(ctx); err != nil {
		return err
	}
	if err := v.daemonUp(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(v.Log, "AgentBox's VM has %s now.\n", size)
	return nil
}

// fitsLive says whether cpus and a memory cap fit in what a running VM can
// be resized to without a restart; a VM that doesn't say (one started by an
// older agentbox) fits nothing.
func fitsLive(live *api.VMLimits, cpus int, capacity int64) bool {
	return live != nil && cpus >= live.MinCPUs && cpus <= live.MaxCPUs && capacity >= live.MinMemory && capacity <= live.MaxMemory
}

// hostModeStatus is `agentbox vm status --json` on a Linux machine that runs
// AgentBox itself: no VM, and nothing else to say.
func hostModeStatus() api.VMStatus {
	return api.VMStatus{Mode: api.ModeHost, State: api.VMOff}
}

// describe is a Cloud Hypervisor VM's status the way a person reads it.
func describe(st api.VMStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", st.Name, st.State)
	if !st.Since.IsZero() {
		fmt.Fprintf(&b, " since %s", st.Since.Local().Format("Jan 2 15:04"))
	}
	if st.CPUs > 0 {
		fmt.Fprintf(&b, ", %d CPUs", st.CPUs)
	}
	m := st.Memory
	if m.Cap > 0 {
		if m.Granted > 0 {
			fmt.Fprintf(&b, ", %s of memory (%s in use), growing to at most %s", sizeWords(m.Granted), sizeWords(m.Used), sizeWords(m.Cap))
		} else {
			fmt.Fprintf(&b, ", memory from %s to %s", sizeWords(m.Min), sizeWords(m.Cap))
		}
	}
	if st.Disk.Size > 0 {
		fmt.Fprintf(&b, ", a %s disk (%s on this machine's)", sizeWords(st.Disk.Size), sizeWords(st.Disk.Used))
	}
	if st.Problem != "" {
		fmt.Fprintf(&b, "\n%s", st.Problem)
	}
	return b.String()
}

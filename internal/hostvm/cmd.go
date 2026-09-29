package hostvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/hostvm/chv"
	"agentbox/internal/paths"
)

// Main is the agentbox command on a host that runs AgentBox in a VM: the `vm`
// commands here, and every other command in the VM.
func Main(args []string, version string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Printf("agentbox version %s\n", version)
		return 0
	}
	if inVM() {
		fmt.Fprintln(os.Stderr, "error:", errInVM)
		return 1
	}
	if len(args) == 0 || args[0] != "vm" {
		vm, err := New()
		if err == nil {
			err = vm.Forward(context.Background(), args) // only returns on failure
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newVMCmd(version)
	root.SetArgs(args[1:])
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newVMCmd(version string) *cobra.Command {
	if runtime.GOOS == "darwin" && !useLima() {
		return newVZCmd(version)
	}
	if !useLima() {
		return newCHVCmd(version)
	}
	root := &cobra.Command{
		Use:   "agentbox vm",
		Short: "Manage the Linux VM AgentBox runs in on this machine",
		Long: `On this machine AgentBox runs in a Linux VM, made with Lima: the daemon, Incus and
every agent are in there, and every agentbox command other than these runs there too.
Your home directory is shared with the VM at the same path.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newInitCmd(), newStartCmd(), newStopCmd(), newStatusCmd(), newPowerCmd(), newShellCmd(), newResizeCmd(), newUpgradeCmd(), newDeleteCmd())
	return root
}

// newCHVCmd is `agentbox vm` on Linux, where the VM is Cloud Hypervisor's and
// optional: a machine runs AgentBox either itself (agentbox host setup) or in
// the VM, as `vm init` chose.
func newCHVCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "agentbox vm",
		Short: "Run AgentBox in a Linux VM on this machine, and manage that VM",
		Long: `AgentBox runs in a VM of its own, made with Cloud Hypervisor (agentbox vm init, which
needs no password), or on this machine itself (agentbox host setup, which installs
Incus). The VM is the recommended way: the daemon, Incus and every agent are in
there, with the CPUs and memory you give it rather than all of this machine's, and
every agentbox command other than these runs there too. Your home directory is
shared with the VM at the same path. The VM starts with a little memory, takes more
as its agents need it, up to a cap, and gives it back; stopping it gives back all of
it. agentbox vm resize changes its CPUs and cap, while it runs.

A machine that already runs AgentBox itself moves into the VM with agentbox vm migrate.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newCHVInitCmd(), newMigrateCmd(), newStartCmd(), newStopCmd(), newPauseCmd(), newResumeCmd(), newStatusCmd(), newPowerCmd(),
		newShellCmd(), newCHVResizeCmd(), newUpgradeCmd(), newDeleteCmd(), newRunCmd(), newProxyCmd())
	return root
}

// newVZCmd is `agentbox vm` on a Mac whose VM is the vz driver's: Cloud
// Hypervisor's commands, less what only a Linux machine has (vm migrate), and
// with the Mac's vm init.
func newVZCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "agentbox vm",
		Short: "Manage the Linux VM AgentBox runs in on this Mac (experimental vz driver)",
		Long: `On this Mac AgentBox runs in a Linux VM that it runs itself with Apple's Virtualization
framework, without Lima: the vz driver, which is experimental. The daemon, Incus and every
agent are in there, and every agentbox command other than these runs there too. Your home
directory is shared with the VM at the same path. agentbox vm delete --yes removes it, and
agentbox vm init then makes Lima's VM, the default, again.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newInitCmd(), newStartCmd(), newStopCmd(), newPauseCmd(), newResumeCmd(), newStatusCmd(), newPowerCmd(),
		newShellCmd(), newCHVResizeCmd(), newUpgradeCmd(), newDeleteCmd(), newRunCmd(), newProxyCmd())
	return root
}

// chvVM is the command's VM on Linux, or ErrNotCreated.
func chvVM() (*VM, error) {
	vm, err := New()
	if err != nil {
		return nil, err
	}
	if vm.CHV == nil {
		return nil, ErrNotCreated
	}
	return vm, nil
}

func newCHVInitCmd() *cobra.Command {
	var cpus int
	var memoryCap, memoryMin, disk string
	defaults, defaultsErr := currentConfig(env("AGENTBOX_VM", chv.DefaultName))
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Make AgentBox's VM, set AgentBox up in it and start its daemon (safe to run again)",
		Long: `Makes AgentBox's VM and runs AgentBox in it from now on, instead of on this machine
itself. It needs /dev/kvm, and no password: what runs the VM is fetched into
~/.local/share/agentbox/vm and runs as you. The VM boots with --memory-min and takes
more as its agents need it, up to --memory-cap.

A machine already set up to run AgentBox itself keeps its agents there: they aren't
moved into the VM, so this stops until its daemon is stopped and its agents removed.
agentbox vm delete --yes goes back.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if defaultsErr != nil {
				return defaultsErr
			}
			c := defaults
			c.CPUs = cpus
			var err error
			if c.MemoryCap, err = ParseMemory(memoryCap); err != nil {
				return fmt.Errorf("--memory-cap: %w", err)
			}
			if c.MemoryMin, err = ParseMemory(memoryMin); err != nil {
				return fmt.Errorf("--memory-min: %w", err)
			}
			if c.Disk, err = ParseMemory(disk); err != nil {
				return fmt.Errorf("--disk: %w", err)
			}
			if err := checkConfig(c, numCPU(), hostMemory()); err != nil {
				return err
			}
			p, err := paths.Default()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			sizes := f.Changed("cpus") || f.Changed("memory-cap") || f.Changed("memory-min") || f.Changed("disk")
			if err := initCHV(cmd.Context(), p, c, sizes, cmd.ErrOrStderr()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), `AgentBox's VM is ready. Next:
  agentbox image build          the machine every agent is copied from (the app's Setup page does this too)
  agentbox auth claude          a Claude Code login for your agents`)
			return nil
		},
	}
	cmd.Flags().IntVar(&cpus, "cpus", defaults.CPUs, "CPUs for the VM")
	cmd.Flags().StringVar(&memoryCap, "memory-cap", sizeWords(defaults.MemoryCap), "the most memory the VM is given, like 20GiB")
	cmd.Flags().StringVar(&memoryMin, "memory-min", sizeWords(chv.DefaultMemoryMin), "the memory the VM boots with, and never gives back")
	cmd.Flags().StringVar(&disk, "disk", sizeWords(chv.DefaultDisk), "the VM's disk for agents, like 100GiB (allocated as it's used)")
	return cmd
}

func newMigrateCmd() *cobra.Command {
	var cpus int
	var memoryCap, memoryMin, disk string
	var removeOld, yes, status, asJSON bool
	defaults, defaultsErr := currentConfig(env("AGENTBOX_VM", chv.DefaultName))
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move this machine's own AgentBox into AgentBox's VM: projects, agents, chats and all (safe to run again)",
		Long: `Moves a machine that runs AgentBox itself (host setup) into AgentBox's VM, making the
VM first when there's none (as agentbox vm init does; the size flags are its).

Everything comes along: projects, settings, accounts, notes, memory, chats and their
history, media, and every agent, with its title, model, limits, branch and worktree,
uncommitted changes included (worktrees stay where they are, on your home, which the
VM shares). Each agent gets a new machine in the VM, from the VM's base image, and
its chat resumes its session. What was only inside an agent's old machine doesn't
come along: packages installed in it, and its home directory outside the worktree.

It stops this machine's AgentBox, backs its state.db up, and checks that everything
arrived before it calls the move done. Stopped half-way, it carries on where it was
when run again. Nothing of this machine's is deleted: its agents' old machines stay,
stopped, in its Incus until you remove them, once you've checked the VM, with
agentbox vm migrate --remove-old. That removes only AgentBox's own machines, by name:
anything else in Incus, its network, its storage pool and Incus itself stay.
Until then, agentbox vm delete --yes goes back to running AgentBox on this machine.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			switch {
			case status:
				st, err := migrationStatus(cmd.Context(), p)
				if err != nil {
					return err
				}
				if asJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(st)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), describeMigration(st))
				return nil
			case removeOld:
				confirm := func(names []string) bool {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "AgentBox's old machines in this machine's Incus:\n  %s\n", strings.Join(names, "\n  "))
					if yes {
						return true
					}
					if !stdinTerminal() {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Run it again with --yes to remove them.")
						return false
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Remove these %d? Nothing else in Incus is touched. [y/N] ", len(names))
					var answer string
					_, _ = fmt.Fscanln(cmd.InOrStdin(), &answer)
					return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
				}
				return removeOldMachines(cmd.Context(), p, confirm, cmd.ErrOrStderr())
			}
			if defaultsErr != nil {
				return defaultsErr
			}
			c := defaults
			c.CPUs = cpus
			if c.MemoryCap, err = ParseMemory(memoryCap); err != nil {
				return fmt.Errorf("--memory-cap: %w", err)
			}
			if c.MemoryMin, err = ParseMemory(memoryMin); err != nil {
				return fmt.Errorf("--memory-min: %w", err)
			}
			if c.Disk, err = ParseMemory(disk); err != nil {
				return fmt.Errorf("--disk: %w", err)
			}
			if err := checkConfig(c, numCPU(), hostMemory()); err != nil {
				return err
			}
			f := cmd.Flags()
			sizes := f.Changed("cpus") || f.Changed("memory-cap") || f.Changed("memory-min") || f.Changed("disk")
			return migrateCHV(cmd.Context(), p, MigrateOptions{Want: c, SizesSet: sizes, Log: cmd.ErrOrStderr()})
		},
	}
	cmd.Flags().IntVar(&cpus, "cpus", defaults.CPUs, "CPUs for the VM, when it's made")
	cmd.Flags().StringVar(&memoryCap, "memory-cap", sizeWords(defaults.MemoryCap), "the most memory the VM is given, like 20GiB")
	cmd.Flags().StringVar(&memoryMin, "memory-min", sizeWords(chv.DefaultMemoryMin), "the memory the VM boots with, and never gives back")
	cmd.Flags().StringVar(&disk, "disk", sizeWords(chv.DefaultDisk), "the VM's disk for agents, like 100GiB (allocated as it's used)")
	cmd.Flags().BoolVar(&removeOld, "remove-old", false, "once the move is checked: remove AgentBox's old machines from this machine's Incus")
	cmd.Flags().BoolVar(&yes, "yes", false, "with --remove-old: don't ask first")
	cmd.Flags().BoolVar(&status, "status", false, "say what there is to move, and how far a move got")
	cmd.Flags().BoolVar(&asJSON, "json", false, "with --status: print it as JSON")
	return cmd
}

// describeMigration is --status the way a person reads it.
func describeMigration(st MigrationStatus) string {
	switch st.State {
	case "none":
		return "This machine has no AgentBox of its own to move into the VM."
	case "available":
		return fmt.Sprintf("This machine runs AgentBox itself, with %d project(s) (%s) and %d agent(s): agentbox vm migrate moves them into the VM.",
			len(st.Projects), strings.Join(st.Projects, ", "), len(st.Agents))
	case "started":
		return "A move into the VM was started and isn't finished: agentbox vm migrate carries on with it."
	case "verified":
		if len(st.OldMachines) == 0 {
			return "This machine's AgentBox was moved into the VM, and checked there."
		}
		return fmt.Sprintf("This machine's AgentBox was moved into the VM, and checked there. Its old machines are still in this machine's Incus (%s): agentbox vm migrate --remove-old removes them.", strings.Join(st.OldMachines, ", "))
	}
	return "This machine's AgentBox was moved into the VM, and its old machines removed."
}

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Freeze the VM: its agents stop using CPU, and keep their memory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := chvVM()
			if err != nil {
				return err
			}
			return chv.Pause(cmd.Context(), vm.CHV.Layout, vm.Paths)
		},
	}
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Carry on running a paused VM, and wait for its daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := chvVM()
			if err != nil {
				return err
			}
			if err := chv.Resume(cmd.Context(), vm.CHV.Layout, vm.Paths); err != nil {
				return err
			}
			return vm.daemonUp(cmd.Context())
		},
	}
}

// newRunCmd is the VM's supervisor, which chv.Start runs in the background.
func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run the VM in the foreground (agentbox vm start runs this in the background)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := chvVM()
			if err != nil {
				return err
			}
			return chv.Supervise(cmd.Context(), vm.CHV.Config, vm.CHV.Layout, vm.Paths)
		},
	}
}

// newProxyCmd is ssh's ProxyCommand into the VM, over vsock.
func newProxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "proxy PORT",
		Short:  "Connect stdin and stdout to one of the VM's vsock ports",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return fmt.Errorf("%q isn't a port", args[0])
			}
			vm, err := chvVM()
			if err != nil {
				return err
			}
			return chv.Proxy(cmd.Context(), vm.CHV.Layout, uint32(port), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

func newCHVResizeCmd() *cobra.Command {
	var cpus int
	var memoryCap, memoryAlias string
	var restart bool
	// A vz VM boots with its whole cap and can't hotplug CPUs: a bigger one
	// needs a restart, which on a Mac resize does, as Lima's does.
	vz := runtime.GOOS == "darwin"
	cmd := &cobra.Command{
		Use:   "resize",
		Short: "Change the VM's CPUs and memory cap, while it runs when it can",
		Long: `Gives the VM another number of CPUs, or another memory cap (the most memory it takes
as its agents need it), or both: 1 CPU to every core this machine has, and a cap from
the memory the VM boots with to all of this machine's.

A running VM changes at once, and its agents keep running: it boots with room for
every core and all of this machine's memory, and only uses what its size says. The
daemon in it restarts to see the new size. A VM started by an older agentbox has no
such room: it gets the new size when it next starts, or now with --restart, which
restarts the VM and stops every agent. The disk stays the size it was made with.`,
		Example: "  agentbox vm resize --cpus 6 --memory-cap 24GiB",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			if vz && f.Changed("memory") {
				if f.Changed("memory-cap") {
					return errors.New("--memory is --memory-cap: give one of them")
				}
				if err := f.Set("memory-cap", memoryAlias); err != nil {
					return err
				}
			}
			if !f.Changed("cpus") && !f.Changed("memory-cap") {
				return errors.New("say what to change: --cpus, --memory-cap, or both")
			}
			vm, err := chvVM()
			if err != nil {
				return err
			}
			cpus, bytes, err := resizeArgs(f.Changed("cpus"), cpus, f.Changed("memory-cap"), memoryCap, chvLimits(vm.CHV.Config, numCPU(), hostMemory()))
			if err != nil {
				return err
			}
			return vm.CHV.resize(cmd.Context(), vm, cpus, bytes, restart)
		},
	}
	cmd.Flags().IntVar(&cpus, "cpus", 0, "CPUs for the VM")
	cmd.Flags().StringVar(&memoryCap, "memory-cap", "", "the most memory the VM is given, like 24GiB")
	cmd.Flags().BoolVar(&restart, "restart", vz, "restart the VM, stopping every agent, when the new size can't be given while it runs")
	if vz {
		// What the Mac's app and Lima's resize say.
		cmd.Flags().StringVar(&memoryAlias, "memory", "", "the same as --memory-cap")
		_ = cmd.Flags().MarkHidden("memory")
		cmd.Long = `Gives the VM another number of CPUs, or another memory cap, or both. The vz driver's VM
boots with its whole cap, and a balloon keeps it to what it needs; a lower cap changes
while it runs, and more CPUs or a higher cap restart it, which stops every agent (as
Lima's VM does). The disk stays the size it was made with.`
	}
	return cmd
}

func newInitCmd() *cobra.Command {
	size := DefaultSize()
	driver := api.VMDriverLima
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Make the VM, set AgentBox up in it and start its daemon (safe to run again)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			switch {
			case driver != api.VMDriverLima && driver != api.VMDriverVZ:
				return fmt.Errorf("--driver %s: it's lima or vz", driver)
			case runtime.GOOS == "darwin" && vzMade() && f.Changed("driver") && driver == api.VMDriverLima:
				return errors.New("AgentBox's VM here is the vz driver's: agentbox vm delete --yes removes it, and every agent in it, before Lima's is made")
			case runtime.GOOS == "darwin" && (driver == api.VMDriverVZ || vzMade()):
				sizes := f.Changed("cpus") || f.Changed("memory") || f.Changed("disk")
				if err := initVZ(cmd.Context(), size, sizes, cmd.ErrOrStderr()); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), `AgentBox's VM is ready (the vz driver, experimental). Next:
  agentbox image build          the machine every agent is copied from (the app's Setup page does this too)
  agentbox auth claude          a Claude Code login for your agents`)
				return nil
			}
			vm, err := New()
			if err != nil {
				return err
			}
			st, err := vm.State(cmd.Context())
			if err != nil {
				return err
			}
			if !st.Exists {
				if err := vm.createVM(cmd.Context(), size); err != nil {
					return err
				}
			}
			if err := vm.Up(cmd.Context()); err != nil {
				return err
			}
			if err := vm.Setup(cmd.Context()); err != nil {
				return err
			}
			if st, err := vm.State(cmd.Context()); err == nil {
				vm.checkReporting(cmd.Context(), st)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), `AgentBox's VM is ready. Next:
  agentbox image build          the machine every agent is copied from (the app's Setup page does this too)
  agentbox auth claude          a Claude Code login for your agents`)
			return nil
		},
	}
	cmd.Flags().IntVar(&size.CPUs, "cpus", size.CPUs, "CPUs for the VM")
	cmd.Flags().StringVar(&size.Memory, "memory", size.Memory, "memory for the VM, like 8GiB")
	cmd.Flags().StringVar(&size.Disk, "disk", size.Disk, "the VM's disk, like 100GiB (allocated as it's used)")
	if runtime.GOOS == "darwin" {
		cmd.Flags().StringVar(&driver, "driver", driver, "what runs the VM: lima, or vz (experimental: Apple's Virtualization framework, run by AgentBox itself, without Lima)")
	}
	return cmd
}

// initVZ is `agentbox vm init --driver vz` on a Mac: package chv's VM, run
// by Apple's Virtualization framework, with size's CPUs and disk and size's
// memory as its cap, which it boots with. Like Lima's init, it is safe to run
// again.
func initVZ(ctx context.Context, size Size, sizesSet bool, log io.Writer) error {
	c, err := currentConfig(env("AGENTBOX_VM", DefaultName))
	if err != nil {
		return err
	}
	c.Driver = chv.DriverVZ
	c.CPUs = size.CPUs
	if c.MemoryCap, err = ParseMemory(size.Memory); err != nil {
		return fmt.Errorf("--memory: %w", err)
	}
	c.MemoryMin = min(chv.DefaultMemoryMin, c.MemoryCap)
	if c.Disk, err = ParseMemory(size.Disk); err != nil {
		return fmt.Errorf("--disk: %w", err)
	}
	if err := checkConfig(c, numCPU(), hostMemory()); err != nil {
		return err
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	return initCHV(ctx, p, c, sizesSet, log)
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the VM, and bring its agentbox up to date (on Linux, and its daemon)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			if err := vm.Ready(cmd.Context()); err != nil {
				return err
			}
			if vm.CHV != nil {
				// The memory cap may have changed since (vm resize).
				if err := vm.writeProfile(cmd.Context()); err != nil {
					return err
				}
				// The app's Start returns when there's a daemon to talk to.
				return vm.daemonUp(cmd.Context())
			}
			return nil
		},
	}
}

func newStopCmd() *cobra.Command {
	var agents bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the VM, and with it the daemon and every agent",
		Long: `Stops the VM, and with it the daemon and every agent, and gives back all of its
memory. The agents that ran start again with the VM, unless --agents stopped them
first (on a Mac, where the VM is Lima's, --agents does nothing more).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			return vm.Stop(cmd.Context(), agents)
		},
	}
	cmd.Flags().BoolVar(&agents, "agents", false, "stop every running agent first, so they stay stopped when the VM starts again")
	return cmd
}

// Status is what `agentbox vm status --json` prints on a Mac, for the app.
type Status struct {
	// Driver is api.VMDriverVZ for the vz driver's VM, which Lima doesn't
	// run; missing for Lima's, whose Status is as it always was.
	Driver  string `json:"driver,omitempty"`
	Lima    string `json:"lima"`              // the limactl in use, "" without one
	Problem string `json:"problem,omitempty"` // why there's no VM to use, and what to run
	Name    string `json:"name"`
	State
	// Limits are the sizes `agentbox vm resize` takes on this machine.
	Limits Limits `json:"limits"`
	// Krunkit is whether `vm init` would make the VM with krunkit, which gives
	// memory back to the Mac: set before there's a VM, on a Mac with Apple
	// Silicon.
	Krunkit *KrunkitCheck `json:"krunkit,omitempty"`
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Say whether the VM exists and runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS == "darwin" && !useLima() {
				return vzStatus(cmd, asJSON)
			}
			if !useLima() {
				return linuxStatus(cmd, asJSON)
			}
			// On Lima this is Status, which the Mac's app parses as it is
			// (desktop/src/main/hostsetup.ts); an api.VMStatus is made from
			// it there. Don't change its shape without changing the app.
			vm, err := New()
			if vm == nil {
				return err
			}
			st := Status{Name: vm.Name, Lima: vm.Limactl, Limits: HostLimits()}
			if err == nil || vm.Limactl != "" {
				// A missing Linux binary doesn't keep a VM from being described.
				s, lerr := vm.State(cmd.Context())
				st.State = s
				err = errors.Join(err, lerr)
			}
			if err != nil {
				st.Problem = err.Error()
			} else if !st.Exists {
				st.Problem = ErrNotCreated.Error()
				st.Krunkit = vm.krunkitCheck(cmd.Context())
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(st)
			}
			switch {
			case st.Problem != "":
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), st.Problem)
			default:
				made := ""
				if st.VMType != "" {
					made = ", made with " + st.VMType
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s, %d CPUs, %s of memory, %s disk%s (%s)\n",
					st.Name, st.Status, st.CPUs, gib(st.Memory), gib(st.Disk), made, st.Dir)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print it as JSON")
	return cmd
}

// vzStatus is `agentbox vm status` on a Mac whose VM is the vz driver's: with
// --json the Status the Mac's app reads for Lima's (desktop/src/main/hostsetup.ts),
// made from the supervisor's api.VMStatus, so the app needs nothing else to
// show it. Its status is Lima's words for the state: Running, or Stopped.
func vzStatus(cmd *cobra.Command, asJSON bool) error {
	vm, err := New()
	if err != nil && !errors.Is(err, ErrNotCreated) {
		// No Linux binary beside this one: there's still a VM to describe.
		p, perr := paths.Default()
		c, cerr := chv.Load(p, env("AGENTBOX_VM", DefaultName))
		if perr != nil || cerr != nil {
			return err
		}
		vm = &VM{Name: c.Name, Home: c.Home, Paths: p, Log: os.Stderr, CHV: &CHV{Config: c, Layout: chv.NewLayout(p, c.Name)}}
	}
	if vm == nil || vm.CHV == nil {
		return err
	}
	st := chvStatus(cmd.Context(), vm.CHV.Config, vm.CHV.Layout, vm.Paths)
	if !asJSON {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), describe(st)+" (the vz driver, experimental)")
		return err
	}
	out := Status{Driver: api.VMDriverVZ, Name: st.Name, Problem: st.Problem, Limits: chvLimits(vm.CHV.Config, numCPU(), hostMemory())}
	if err != nil {
		out.Problem = err.Error()
	}
	out.State = State{Exists: st.State != api.VMMissing, Status: "Stopped", Dir: vm.CHV.Layout.Dir(), CPUs: st.CPUs, Memory: st.Memory.Cap, Disk: st.Disk.Size, Arch: runtime.GOARCH}
	switch st.State {
	case api.VMRunning, api.VMStarting, api.VMPaused, api.VMStopping:
		out.Status = "Running"
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

// linuxStatus is `agentbox vm status` on Linux: an api.VMStatus, which says
// ModeHost when this machine runs AgentBox itself.
func linuxStatus(cmd *cobra.Command, asJSON bool) error {
	vm, err := New()
	st := hostModeStatus()
	switch {
	case err == nil && vm.CHV != nil:
		st = chvStatus(cmd.Context(), vm.CHV.Config, vm.CHV.Layout, vm.Paths)
	case err != nil && !errors.Is(err, ErrNotCreated):
		return err
	}
	if asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(st)
	}
	if vm == nil || vm.CHV == nil {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "This machine runs AgentBox itself, not in a VM: agentbox vm init switches it to one.")
		return nil
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), describe(st))
	return nil
}

// Power is `agentbox vm power --json`: the VM's state and memory, which the
// app's top bar polls (desktop/src/main/vmpower.ts, its VMPower). State is
// one of off, starting, running, pausing, paused, resuming and stopping;
// sizes are bytes. On a machine with no VM it prints {"mode":"host"} instead.
type Power struct {
	State         string `json:"state"`
	MemoryUsed    int64  `json:"memoryUsed"`
	MemoryGranted int64  `json:"memoryGranted"`
	MemoryCap     int64  `json:"memoryCap"`
	CPUs          int    `json:"cpus"`
	Error         string `json:"error,omitempty"` // why it can't be used, and what to run
}

// powerFrom is a Cloud Hypervisor VM's Power. A VM vm init hasn't finished
// making is off, with why.
func powerFrom(st api.VMStatus) Power {
	p := Power{State: st.State, MemoryUsed: st.Memory.Used, MemoryGranted: st.Memory.Granted, MemoryCap: st.Memory.Cap, CPUs: st.CPUs, Error: st.Problem}
	if st.State == api.VMMissing {
		p.State = api.VMOff
		if p.Error == "" {
			p.Error = ErrNotCreated.Error()
		}
	}
	return p
}

// limaPower is a Lima VM's Power, whose memory is all granted while it runs.
func limaPower(st State, err error) Power {
	switch {
	case err != nil:
		return Power{State: api.VMOff, Error: err.Error()}
	case !st.Exists:
		return Power{State: api.VMOff, Error: ErrNotCreated.Error()}
	}
	p := Power{State: api.VMOff, MemoryCap: st.Memory, CPUs: st.CPUs}
	switch st.Status {
	case "Running":
		p.State, p.MemoryGranted = api.VMRunning, st.Memory
	case "Broken":
		p.Error = "AgentBox's VM is broken, Lima says: see limactl list"
	}
	return p
}

func newPowerCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:    "power",
		Short:  "Say whether the VM runs, and how much memory it holds (for the app)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			var p Power
			switch {
			case !useLima() && errors.Is(err, ErrNotCreated):
				if asJSON {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), `{"mode":"host"}`)
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "This machine runs AgentBox itself, not in a VM.")
				}
				return nil
			case err != nil && (vm == nil || vm.Limactl == ""):
				if vm == nil {
					return err
				}
				p = limaPower(State{}, err)
			case vm.CHV != nil:
				p = powerFrom(chvStatus(cmd.Context(), vm.CHV.Config, vm.CHV.Layout, vm.Paths))
			default:
				p = limaPower(vm.State(cmd.Context()))
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s, %d CPUs, %s of memory held (%s in use) of at most %s\n",
				p.State, p.CPUs, sizeWords(p.MemoryGranted), sizeWords(p.MemoryUsed), sizeWords(p.MemoryCap))
			if p.Error != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), p.Error)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print it as JSON")
	return cmd
}

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell [-- command...]",
		Short: "Open a shell in the VM (not in an agent: agentbox shell <project/agent> is that)",
		RunE: func(cmd *cobra.Command, args []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			if err := vm.Up(cmd.Context()); err != nil {
				return err
			}
			if vm.CHV != nil {
				// No command is a login shell (chv.SSHArgs).
				return execve(vm.command(vm.workdir(), stdinTerminal(), args))
			}
			argv := append([]string{vm.Limactl, "shell", "--workdir", vm.workdir(), vm.Name}, args...)
			return syscall.Exec(argv[0], argv, os.Environ())
		},
	}
}

func newResizeCmd() *cobra.Command {
	var cpus int
	var memory string
	cmd := &cobra.Command{
		Use:   "resize",
		Short: "Change the VM's CPUs and memory, restarting it and every agent in it",
		Long: `Gives the VM another number of CPUs, or another amount of memory, or both. Lima
only changes a stopped VM, so a running one is stopped, changed and started again,
and its daemon restarted: every agent stops with it. A stopped VM is changed and
stays stopped. The disk stays the size it was made with.`,
		Example: "  agentbox vm resize --cpus 6 --memory 12GiB",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cpus, bytes, err := resizeArgs(cmd.Flags().Changed("cpus"), cpus, cmd.Flags().Changed("memory"), memory, HostLimits())
			if err != nil {
				return err
			}
			vm, err := New()
			if err != nil {
				return err
			}
			return vm.Resize(cmd.Context(), cpus, bytes)
		},
	}
	cmd.Flags().IntVar(&cpus, "cpus", 0, "CPUs for the VM")
	cmd.Flags().StringVar(&memory, "memory", "", "memory for the VM, like 12GiB")
	return cmd
}

// resizeArgs checks resize's flags against this machine's limits, before
// anything is stopped: the CPUs and bytes of memory to give the VM, zero for
// one that stays.
func resizeArgs(cpusSet bool, cpus int, memorySet bool, memory string, limits Limits) (int, int64, error) {
	if !cpusSet && !memorySet {
		return 0, 0, errors.New("say what to change: --cpus, --memory, or both")
	}
	if cpusSet && cpus <= 0 {
		return 0, 0, fmt.Errorf("%d CPUs: the VM needs at least %d", cpus, limits.MinCPUs)
	}
	var bytes int64
	if memorySet {
		var err error
		if bytes, err = ParseMemory(memory); err != nil {
			return 0, 0, err
		}
	}
	return cpus, bytes, limits.Check(cpus, bytes)
}

func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Install this agentbox in the VM, and restart its daemon on it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			if err := vm.Up(cmd.Context()); err != nil {
				return err
			}
			if err := vm.Install(cmd.Context()); err != nil {
				return err
			}
			if vm.CHV != nil {
				if err := vm.writeProfile(cmd.Context()); err != nil {
					return err
				}
			}
			return vm.restartDaemon(cmd.Context())
		},
	}
}

func newDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove the VM, and every agent and base image in it",
		Long: `Removes the VM, and with it everything AgentBox keeps in it: agents, their machines,
the base image and AgentBox's state. Your projects and the agents' worktrees are on
your own disk, in your home directory, and stay. On Linux this machine then runs
AgentBox itself again, as it did before agentbox vm init.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return errors.New("this removes every agent's machine: run it with --yes if you mean it")
			}
			vm, err := New()
			if !useLima() && vm != nil && vm.CHV == nil {
				return errors.New("this machine has no VM of AgentBox's to remove")
			}
			if vm == nil || (vm.CHV == nil && vm.Limactl == "") {
				return err
			}
			return vm.Delete(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "really remove it")
	return cmd
}

func gib(bytes int64) string {
	return fmt.Sprintf("%.0f GiB", float64(bytes)/(1<<30))
}

package hostvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// Main is the agentbox command on a host that runs AgentBox in a VM: the `vm`
// commands here, and every other command in the VM.
func Main(args []string, version string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Printf("agentbox version %s\n", version)
		return 0
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
	root.AddCommand(newInitCmd(), newStartCmd(), newStopCmd(), newStatusCmd(), newShellCmd(), newResizeCmd(), newUpgradeCmd(), newDeleteCmd())
	return root
}

func newInitCmd() *cobra.Command {
	size := DefaultSize()
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Make the VM, set AgentBox up in it and start its daemon (safe to run again)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			st, err := vm.State(cmd.Context())
			if err != nil {
				return err
			}
			if !st.Exists {
				if err := vm.Create(cmd.Context(), size); err != nil {
					return err
				}
			}
			if err := vm.Up(cmd.Context()); err != nil {
				return err
			}
			if err := vm.Setup(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), `AgentBox's VM is ready. Next:
  agentbox image build          the machine every agent is copied from (the app's Setup page does this too)
  agentbox auth claude          a Claude Code login for your agents`)
			return nil
		},
	}
	cmd.Flags().IntVar(&size.CPUs, "cpus", size.CPUs, "CPUs for the VM")
	cmd.Flags().StringVar(&size.Memory, "memory", size.Memory, "memory for the VM, like 8GiB")
	cmd.Flags().StringVar(&size.Disk, "disk", size.Disk, "the VM's disk, like 100GiB (allocated as it's used)")
	return cmd
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the VM, and bring its agentbox up to date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			return vm.Ready(cmd.Context())
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the VM, and with it the daemon and every agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			vm, err := New()
			if err != nil {
				return err
			}
			return vm.Stop(cmd.Context())
		},
	}
}

// Status is what `agentbox vm status --json` prints, for the app.
type Status struct {
	Lima    string `json:"lima"`              // the limactl in use, "" without one
	Problem string `json:"problem,omitempty"` // why there's no VM to use, and what to run
	Name    string `json:"name"`
	State
	// Limits are the sizes `agentbox vm resize` takes on this machine.
	Limits Limits `json:"limits"`
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Say whether the VM exists and runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(st)
			}
			switch {
			case st.Problem != "":
				fmt.Fprintln(cmd.OutOrStdout(), st.Problem)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s, %d CPUs, %s of memory, %s disk (%s)\n",
					st.Name, st.Status, st.CPUs, gib(st.Memory), gib(st.Disk), st.Dir)
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
your own disk, in your home directory, and stay.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return errors.New("this removes every agent's machine: run it with --yes if you mean it")
			}
			vm, err := New()
			if vm == nil || vm.Limactl == "" {
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

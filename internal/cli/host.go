package cli

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/hostsetup"
)

func newHostCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Set up this machine for AgentBox, and check what's missing",
		// AgentBox's own VM runs these, and so does a Linux machine that ran
		// AgentBox itself before it ran in the VM on Linux too, until it moves
		// (internal/hostvm's Front). Nobody else is offered them: on a Linux
		// host, agentbox vm init sets AgentBox up.
		Hidden: true,
	}
	cmd.AddCommand(newHostSetupCmd(), newHostBudgetCmd(), newHostCheckCmd(a))
	return cmd
}

func newHostSetupCmd() *cobra.Command {
	var print bool
	var forUser, bridgeSubnet string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install Incus to run agents on this computer itself (once, as root)",
		Long: `On Linux, running AgentBox in a VM of its own is the recommended way: agentbox vm init,
which needs no password and changes nothing on your system. This is the other way:
agents run directly on this computer's Incus, sharing its kernel, memory and disk,
so heavy agent work can freeze your desktop.

Installs Incus (Arch, Debian/Ubuntu or Fedora), gives your user access to it — in the
session running now, not only the next login — creates its storage pool and network,
lets agents map your user, and keeps ufw and Docker from blocking the Incus bridge.
It also does what host budget does: the cgroup the shared agent budget lives in, ready
for when you turn the budget on. Safe to run again. Run it with sudo:

  ` + hostsetup.Command + `

The app's Setup page runs the same thing through pkexec, which asks for your password
in the desktop's own dialog. sudo says who ran it in SUDO_USER and pkexec in
PKEXEC_UID; --user names that user when neither of those does. --bridge-subnet gives
the Incus bridge that subnet when it's made, instead of letting Incus pick one; the
Mac's VM needs it, since Incus can't find a free subnet on Lima's network.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if print {
				_, err := cmd.OutOrStdout().Write(hostsetup.Script)
				return err
			}
			// Who this is for is worked out before the root check, so a run
			// from a terminal reports the wrong user before the missing sudo.
			target, err := hostsetup.TargetUser(os.Getenv("SUDO_USER"), os.Getenv("PKEXEC_UID"), forUser, hostsetup.System())
			if err != nil {
				return err
			}
			args := []string{"--user", target}
			if bridgeSubnet != "" {
				if _, err := netip.ParsePrefix(bridgeSubnet); err != nil {
					return fmt.Errorf("--bridge-subnet wants an address and prefix, like 10.87.0.1/24: %w", err)
				}
				args = append(args, "--bridge-subnet", bridgeSubnet)
			}
			if os.Geteuid() != 0 {
				return errors.New("host setup is for " + target + ", but it has to run as root: " + hostsetup.Command)
			}
			// The shared budget's cgroup first, so the script's own result is
			// what the output ends on. It only makes the budget possible:
			// turning it on is the user's. Here in the Mac's VM and WSL as
			// well, where the budget can't be turned on: the unit costs
			// nothing and is ready if it ever can.
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, "==> The shared agent budget's cgroup")
			u, err := hostsetup.System().ByName(target)
			if err == nil {
				err = hostsetup.SetUpBudget(target, u.Uid, func(line string) { _, _ = fmt.Fprintln(out, line) })
			}
			if err != nil {
				_, _ = fmt.Fprintf(out, "Couldn't make it, so agents have no shared budget yet (%v): Settings says what's missing, and %s tries again.\n", err, hostsetup.BudgetCommand)
			}
			f, err := os.CreateTemp("", "agentbox-host-setup-*.sh")
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(f.Name()) }()
			if _, err := f.Write(hostsetup.Script); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			run := exec.CommandContext(cmd.Context(), "bash", append([]string{f.Name()}, args...)...)
			run.Stdin, run.Stdout, run.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
			return run.Run()
		},
	}
	cmd.Flags().BoolVar(&print, "print", false, "only print the script")
	cmd.Flags().StringVar(&forUser, "user", "", "the user to set this machine up for, when sudo and pkexec don't say")
	cmd.Flags().StringVar(&bridgeSubnet, "bridge-subnet", "", "the Incus bridge's address and subnet, like 10.87.0.1/24, instead of one Incus picks")
	return cmd
}

func newHostBudgetCmd() *cobra.Command {
	var forUser string
	var remove bool
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Make the cgroup the shared agent budget needs (once, as root)",
		Long: `Installs ` + hostsetup.BudgetUnitName + `, a oneshot systemd unit that makes
` + hostsetup.BudgetCgroupDir + ` at every boot, enables the memory and CPU controllers for it,
and gives your user its budget files (` + strings.Join(hostsetup.BudgetFiles, ", ") + `) and the
memory.low of ` + strings.Join(hostsetup.ReserveSlices, " and ") + `, so the daemon can put every agent under
one shared budget, change it while they run, and reserve the rest of the memory
for your own apps. It doesn't turn the budget on: that's yours to do, in Settings
or with agentbox limits --shared-budget true. Run it with sudo:

  ` + hostsetup.BudgetCommand + `

Host setup does this too; it's here on its own for hosts set up before it did, and
Settings runs it through pkexec. --remove takes the unit away again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := func(line string) { _, _ = fmt.Fprintln(cmd.OutOrStdout(), line) }
			if remove {
				if os.Geteuid() != 0 {
					return errors.New("removing the shared budget's unit has to run as root: " + hostsetup.BudgetCommand + " --remove")
				}
				return hostsetup.RemoveBudget(log)
			}
			if os.Geteuid() != 0 {
				return errors.New("making the shared budget's cgroup has to run as root: " + hostsetup.BudgetCommand)
			}
			target, err := hostsetup.TargetUser(os.Getenv("SUDO_USER"), os.Getenv("PKEXEC_UID"), forUser, hostsetup.System())
			if err != nil {
				return err
			}
			u, err := hostsetup.System().ByName(target)
			if err != nil {
				return err
			}
			return hostsetup.InstallBudget(target, u.Uid, log)
		},
	}
	cmd.Flags().StringVar(&forUser, "user", "", "the user the budget is for, when sudo and pkexec don't say")
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the unit instead")
	return cmd
}

func newHostCheckCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check that this machine has everything AgentBox needs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			status, err := c.Setup(cmd.Context())
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, check := range status.Checks {
				mark := map[string]string{api.SetupOK: "✓", api.SetupMissing: "✗", api.SetupOutdated: "!", api.SetupOptional: "–", api.SetupWarn: "!", api.SetupUpdating: "↻"}[check.Status]
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", mark, check.Title, check.Detail)
				if check.Status != api.SetupOK && check.Status != api.SetupUpdating && check.Fix != "" {
					_, _ = fmt.Fprintf(w, "\t\tfix: %s\n", check.Fix)
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if !status.Ready {
				return exitCodeError(1)
			}
			return nil
		},
	}
}

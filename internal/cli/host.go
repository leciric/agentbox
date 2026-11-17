package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/bridgecheck"
	"agentbox/internal/hostsetup"
	"agentbox/internal/incus"
	"agentbox/internal/kvm"
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
	cmd.AddCommand(newHostSetupCmd(), newHostKVMCmd(), newHostCheckCmd(a), newProbeBridgeCmd())
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
It also removes the unit earlier releases installed for a shared agent budget.
Safe to run again. Run it with sudo:

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
			// The unit an earlier release installed for the shared agent
			// budget goes first, so the script's own result is what the output
			// ends on. Failing to remove it doesn't fail the setup.
			out := cmd.OutOrStdout()
			if err := hostsetup.RemoveBudget(func(line string) { _, _ = fmt.Fprintln(out, line) }); err != nil {
				_, _ = fmt.Fprintf(out, "Couldn't remove the shared agent budget's unit: %v\n", err)
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
			if err := run.Run(); err != nil {
				return err
			}
			// Containers on the bridge may not resolve, or reach out over IPv6
			// or with full-size packets, while this machine does (WSL
			// especially): said only when something's off.
			res, err := bridgecheck.Check(cmd.Context(), bridgecheck.System(incus.Client{}))
			if err != nil {
				_, _ = fmt.Fprintf(out, "\n==> %s\nCouldn't check it: %v\n", bridgecheck.Bridge, err)
			} else if res.Message != "" {
				_, _ = fmt.Fprintf(out, "\n==> %s\n%s\n", bridgecheck.Bridge, res.Message)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&print, "print", false, "only print the script")
	cmd.Flags().StringVar(&forUser, "user", "", "the user to set this machine up for, when sudo and pkexec don't say")
	cmd.Flags().StringVar(&bridgeSubnet, "bridge-subnet", "", "the Incus bridge's address and subnet, like 10.87.0.1/24, instead of one Incus picks")
	return cmd
}

// newHostKVMCmd gives the user who runs AgentBox /dev/kvm, which its VM
// needs, as root and without a new login (internal/kvm): what the app's Setup
// runs through pkexec when the VM can't be made for want of it.
func newHostKVMCmd() *cobra.Command {
	var forUser string
	cmd := &cobra.Command{
		Use:   "kvm",
		Short: "Give your user /dev/kvm for AgentBox's VM, with no new login (once, as root)",
		Long: `AgentBox's VM needs /dev/kvm. Debian and Ubuntu give it only to the kvm group,
which counts from your next login. This gives it to your user at once: an ACL on
/dev/kvm for the processes you run now, and the kvm group for later logins (or,
with no kvm group, a udev rule giving it to whoever is at the screen). When there's
no /dev/kvm and the CPU can virtualize, it loads KVM's module, now and at every boot.
Safe to run again. Run it with sudo:

  ` + kvm.Command + `

The app's Setup runs the same thing through pkexec. --user names the user when
neither sudo nor pkexec says who ran it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := hostsetup.TargetUser(os.Getenv("SUDO_USER"), os.Getenv("PKEXEC_UID"), forUser, hostsetup.System())
			if err != nil {
				return err
			}
			u, err := user.Lookup(target)
			if err != nil {
				return err
			}
			return kvm.Grant(cmd.Context(), u, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&forUser, "user", "", "the user to give /dev/kvm, when sudo and pkexec don't say")
	return cmd
}

// newProbeBridgeCmd is what bridgecheck runs as root to check the Incus
// bridge from a network namespace on it: once to make the namespace, and
// again, with --inside, in it. It prints a bridgecheck.Report.
func newProbeBridgeCmd() *cobra.Command {
	var inside bool
	cmd := &cobra.Command{
		Use:    "probe-bridge <spec>",
		Short:  "Check the Incus bridge from a network namespace on it (as root)",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var spec bridgecheck.Spec
			if err := json.Unmarshal([]byte(args[0]), &spec); err != nil {
				return err
			}
			var report bridgecheck.Report
			if inside {
				report = bridgecheck.Inside(cmd.Context(), spec)
			} else {
				if os.Geteuid() != 0 {
					return errors.New("probe-bridge makes a network namespace, which takes root")
				}
				self, err := bridgecheck.Self()
				if err != nil {
					return err
				}
				if report, err = bridgecheck.RunProbe(cmd.Context(), spec, self, nil); err != nil {
					return err
				}
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		},
	}
	cmd.Flags().BoolVar(&inside, "inside", false, "run the checks from this namespace")
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

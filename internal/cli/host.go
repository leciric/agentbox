package cli

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/hostsetup"
)

func newHostCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Set up this machine for AgentBox, and check what's missing",
	}
	cmd.AddCommand(newHostSetupCmd(), newHostCheckCmd(a))
	return cmd
}

func newHostSetupCmd() *cobra.Command {
	var print bool
	var forUser, bridgeSubnet string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install and configure Incus for AgentBox (once, as root)",
		Long: `Installs Incus (Arch, Debian/Ubuntu or Fedora), gives your user access to it — in the
session running now, not only the next login — creates its storage pool and network,
lets agents map your user, and keeps ufw and Docker from blocking the Incus bridge.
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
			f, err := os.CreateTemp("", "agentbox-host-setup-*.sh")
			if err != nil {
				return err
			}
			defer os.Remove(f.Name())
			if _, err := f.Write(hostsetup.Script); err != nil {
				f.Close()
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
				mark := map[string]string{api.SetupOK: "✓", api.SetupMissing: "✗", api.SetupOutdated: "!", api.SetupOptional: "–"}[check.Status]
				fmt.Fprintf(w, "%s\t%s\t%s\n", mark, check.Title, check.Detail)
				if check.Status != api.SetupOK && check.Fix != "" {
					fmt.Fprintf(w, "\t\tfix: %s\n", check.Fix)
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

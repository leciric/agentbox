package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// `agentbox run` was an agent's run phase (#799): running the app, docker
// compose, Playwright, the desktop or a recording took a run slot, and waited
// in line while none was free. Run slots are gone (24 Nov 2026, the user's
// decision): nothing waits for one, and the kernel's limits are what protect
// the VM. The command stays, hidden, for an agent whose brief still said to
// use it as AgentBox updated under it: `run up` returns at once, `run up --
// <command>` runs the command as if typed (through the shims in PATH, so a
// build still gets its share of the cores), and `run down` and `run status`
// say there is nothing to hold or give back.

// runGone is what each of them says.
const runGone = "Run slots are gone: start the app, docker compose, Playwright, the desktop or a recording whenever you need it, and stop it when you're done."

func newRunCmd(_ *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "run",
		Short:  "Nothing: run slots are gone (kept for agents that still call it)",
		Hidden: true,
	}
	up := &cobra.Command{
		Use:   "up [-- <command> [args...]]",
		Short: "Run the command, if any: nothing waits for a slot any more",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), runGone)
				return nil
			}
			path, err := exec.LookPath(args[0])
			if err != nil {
				return err
			}
			return execReal(path, args, os.Environ())
		},
	}
	up.Flags().SetInterspersed(false)
	nothing := func(use string) *cobra.Command {
		return &cobra.Command{
			Use:  use,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), runGone)
				return nil
			},
		}
	}
	cmd.AddCommand(up, nothing("status"), nothing("down"))
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), runGone)
		return nil
	}
	return cmd
}

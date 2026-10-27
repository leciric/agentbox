package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"agentbox/internal/agentd"
)

// newAgentdCmd is the command an agent's pod runs as its only process: it opens
// the pod's session to the master and serves the operations the master asks for
// over it. It is hidden because it is the pod's command, not the user's, and it
// takes no flags: everything it needs is in the environment the master made the
// pod with, and the token is a file the pod mounts rather than an argument that
// would show in a process listing.
func newAgentdCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:    "agentd [health]",
		Short:  "Serve the master's operations from inside an agent's pod",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				// The pod's startup probe runs this. The probe asks whether the
				// process is alive; a second process that answers is that.
				if args[0] != "health" {
					return fmt.Errorf("agentd takes no argument but health, not %q", args[0])
				}
				return nil
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cfg, err := agentd.FromEnv()
			if err != nil {
				return err
			}
			return agentd.Run(ctx, cfg)
		},
	}
}

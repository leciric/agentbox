package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newVersionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print AgentBox's version, and whether a newer one is out",
		Long: "Print AgentBox's version. When the daemon is running and its daily update check has found a\n" +
			"newer release, say so and link to it. This doesn't start the daemon, and asks nothing itself:\n" +
			"what it knows is what the daemon's last check found (see the README's \"Update check\").",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "agentbox %s\n", version)
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			status, err := api.NewClient(a.paths.Socket()).Update(ctx)
			if err != nil {
				return nil // no daemon, or one older than the check: nothing to add
			}
			printUpdate(out, status)
			return nil
		},
	}
}

func printUpdate(out io.Writer, status api.UpdateStatus) {
	if status.Current != version {
		_, _ = fmt.Fprintf(out, "The daemon running is %s: restart it with agentbox daemon stop\n", status.Current)
	}
	if u := status.Available; u != nil {
		_, _ = fmt.Fprintf(out, "Update available: AgentBox %s, %s\n", u.Version, u.URL)
	}
}

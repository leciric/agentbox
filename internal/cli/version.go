package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/update"
)

func newVersionCmd(a *app) *cobra.Command {
	var channel string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print AgentBox's version, and whether a newer one is out",
		Long: "Print AgentBox's version. When the daemon is running and its daily update check has found a\n" +
			"newer release, say so and link to it. This doesn't start the daemon, and asks nothing itself:\n" +
			"what it knows is what the daemon's last check found (see the README's \"Update check\").\n\n" +
			"--channel picks what the check offers: stable, only releases, or nightly, the nightly builds\n" +
			"as well. Going back to stable from a nightly offers the latest stable release, though its\n" +
			"version is lower. This one starts the daemon when it isn't running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if channel != "" {
				if channel != api.UpdateChannelStable && channel != api.UpdateChannelNightly {
					return fmt.Errorf("--channel is %s or %s", api.UpdateChannelStable, api.UpdateChannelNightly)
				}
				c, err := a.client(cmd)
				if err != nil {
					return err
				}
				if _, err := c.UpdateSettings(cmd.Context(), api.UpdateSettingsRequest{UpdateChannel: &channel}); err != nil {
					return err
				}
			}
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
	cmd.Flags().StringVar(&channel, "channel", "", "switch the update check to stable or nightly")
	return cmd
}

func printUpdate(out io.Writer, status api.UpdateStatus) {
	if status.Current != version {
		_, _ = fmt.Fprintf(out, "The daemon running is %s: restart it with agentbox daemon stop\n", status.Current)
	}
	// Said only when it isn't what a stable build on the stable channel, which
	// is nearly everybody, would take for granted.
	if status.Channel == api.UpdateChannelNightly || status.Nightly {
		_, _ = fmt.Fprintf(out, "Update channel: %s\n", status.Channel)
	}
	if u := status.Available; u != nil {
		if update.Newer(u.Version, status.Current) {
			_, _ = fmt.Fprintf(out, "Update available: AgentBox %s, %s\n", u.Version, u.URL)
		} else {
			_, _ = fmt.Fprintf(out, "Latest stable release: AgentBox %s, %s\n", u.Version, u.URL)
		}
	}
}

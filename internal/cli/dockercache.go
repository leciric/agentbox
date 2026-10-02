package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// newDockerCacheCmd shows or changes the image cache agents' Docker shares.
func newDockerCacheCmd(a *app) *cobra.Command {
	var maxSize string
	var clear bool
	cmd := &cobra.Command{
		Use:   "docker-cache [on|off] [--max 20GiB] [--clear]",
		Short: "Show or change the Docker image cache agents share",
		Long: `Shows or changes the shared image cache: every agent's Docker pulls Docker Hub's
images through one cache in AgentBox's VM, so an image is downloaded and stored
once rather than once per agent. Images from other registries (ghcr.io, quay.io…)
are pulled directly. When the cache can't answer, Docker pulls from Docker Hub
itself. It's on unless you turn it off.

  agentbox docker-cache                 say whether it's on, and how much it holds
  agentbox docker-cache off             point agents' Docker back at Docker Hub alone
  agentbox docker-cache --max 40GiB     the most it may hold; 20GiB by default, 0 for that
  agentbox docker-cache --clear         empty it

Inside an agent, docker info | grep -A2 Mirrors shows the cache as
` + agent.ImageCacheMirror + ` while it's on.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			var req api.UpdateSettingsRequest
			if len(args) == 1 {
				on, err := parseOnOff(args[0])
				if err != nil {
					return err
				}
				req.ImageCache = &on
			}
			if cmd.Flags().Changed("max") {
				var n int64
				if maxSize != "0" {
					if n, err = agent.ParseBytes(maxSize); err != nil {
						return fmt.Errorf("--max: %w", err)
					}
				}
				req.ImageCacheMaxBytes = &n
			}
			req.ClearImageCache = clear
			var settings api.Settings
			if req.ImageCache == nil && req.ImageCacheMaxBytes == nil && !clear {
				settings, err = c.Settings(cmd.Context())
			} else {
				settings, err = c.UpdateSettings(cmd.Context(), req)
			}
			if err != nil {
				return err
			}
			state := "off: agents pull from Docker Hub directly"
			if settings.ImageCache {
				state = "on"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "shared Docker image cache: %s\nholds %s of at most %s\n",
				state, agent.HumanBytes(settings.ImageCacheBytes), agent.HumanBytes(settings.ImageCacheMaxBytes))
			return err
		},
	}
	cmd.Flags().StringVar(&maxSize, "max", "", "the most the cache may hold, like 20GiB; 0 for the default")
	cmd.Flags().BoolVar(&clear, "clear", false, "empty the cache")
	return cmd
}

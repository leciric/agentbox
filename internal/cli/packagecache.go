package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
	"agentbox/internal/api"
)

// newPackageCacheCmd shows or changes the package caches agents share.
func newPackageCacheCmd(a *app) *cobra.Command {
	var maxSize string
	var clear bool
	cmd := &cobra.Command{
		Use:   "package-cache [on|off] [--max 20GiB] [--clear]",
		Short: "Show or change the package caches agents share",
		Long: `Shows or changes the shared package caches: every agent's pnpm, npm, Yarn (2 and
later), Go, pip, uv, Corepack and Playwright download into one directory in
AgentBox's VM, so a dependency, a module or a browser is downloaded once rather
than once per agent, and is still there for the next agent. Logins, tokens and
.env files stay in each agent. It's on unless you turn it off.

  agentbox package-cache                 say whether it's on, and how much it holds
  agentbox package-cache off             agents keep caches of their own again
  agentbox package-cache --max 40GiB     the most it may hold; 20GiB by default, 0 for that
  agentbox package-cache --clear         empty it

Inside an agent the caches are at ` + agent.PackageCachePath + `, and
pnpm store path or go env GOMODCACHE point there while it's on. Agents that
are running get it at once; a shell already open keeps what it had.`,
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
				req.PackageCache = &on
			}
			if cmd.Flags().Changed("max") {
				var n int64
				if maxSize != "0" {
					if n, err = agent.ParseBytes(maxSize); err != nil {
						return fmt.Errorf("--max: %w", err)
					}
				}
				req.PackageCacheMaxBytes = &n
			}
			req.ClearPackageCache = clear
			var settings api.Settings
			if req.PackageCache == nil && req.PackageCacheMaxBytes == nil && !clear {
				settings, err = c.Settings(cmd.Context())
			} else {
				settings, err = c.UpdateSettings(cmd.Context(), req)
			}
			if err != nil {
				return err
			}
			if req.PackageCacheMaxBytes != nil && *req.PackageCacheMaxBytes > settings.PackageCacheMaxBytes {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is more than the package caches' disk can hold above the space AgentBox keeps free: capped at %s\n",
					agent.HumanBytes(*req.PackageCacheMaxBytes), agent.HumanBytes(settings.PackageCacheMaxBytes))
			}
			state := "off: each agent downloads into caches of its own"
			if settings.PackageCache {
				state = "on"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "shared package caches: %s\nholds %s of at most %s\n",
				state, agent.HumanBytes(settings.PackageCacheBytes), agent.HumanBytes(settings.PackageCacheMaxBytes))
			return err
		},
	}
	cmd.Flags().StringVar(&maxSize, "max", "", "the most the caches may hold, like 20GiB; 0 for the default")
	cmd.Flags().BoolVar(&clear, "clear", false, "empty the caches")
	return cmd
}

package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newAutoStopCmd shows or changes "auto-stop idle agents".
func newAutoStopCmd(a *app) *cobra.Command {
	var idleTime string
	cmd := &cobra.Command{
		Use:   "auto-stop [on|off] [--idle-time 2h]",
		Short: "Show or change whether idle agents are stopped on their own",
		Long: `Shows or changes "auto-stop idle agents": the daemon stops a running or paused
agent once it has gone --idle-time with nothing happening on it — no chat turn,
job, question, terminal input or recording — keeping its worktree and branch,
like stopping it by hand. It's off unless you turn it on.

  agentbox auto-stop                    say whether it's on, and after how long
  agentbox auto-stop on                 turn it on
  agentbox auto-stop off                turn it off
  agentbox auto-stop --idle-time 90m    how long an agent may go idle first; 2h by default`,
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
				req.AutoStopIdle = &on
			}
			if cmd.Flags().Changed("idle-time") {
				d, err := time.ParseDuration(idleTime)
				if err != nil || d < 60*time.Second {
					return fmt.Errorf("--idle-time is a duration of at least 60s, like 2h; %q isn't", idleTime)
				}
				secs := int(d / time.Second)
				req.IdleTimeSeconds = &secs
			}
			var settings api.Settings
			if req.AutoStopIdle == nil && req.IdleTimeSeconds == nil {
				settings, err = c.Settings(cmd.Context())
			} else {
				settings, err = c.UpdateSettings(cmd.Context(), req)
			}
			if err != nil {
				return err
			}
			idle := time.Duration(settings.IdleTimeSeconds) * time.Second
			state := fmt.Sprintf("off (an agent would be stopped after %s idle)", idle)
			if settings.AutoStopIdle {
				state = fmt.Sprintf("on, stopping an agent idle for %s", idle)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "auto-stop idle agents: %s\n", state)
			return err
		},
	}
	cmd.Flags().StringVar(&idleTime, "idle-time", "", "how long an agent may go idle before it is stopped, like 2h")
	return cmd
}

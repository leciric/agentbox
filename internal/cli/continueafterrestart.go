package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newContinueAfterRestartCmd shows or changes "Continue agents after restarts".
func newContinueAfterRestartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "continue-after-restart [on|off]",
		Short: "Show or change whether agents carry on with a turn AgentBox's restart cut short",
		Long: `Shows or changes "Continue agents after restarts": when AgentBox, its VM or
this machine stops while agents or project chats are in the middle of a turn
(an update, a crash, a quit, a reboot), the next start starts their machines
again and tells each to continue where it left off. Agents you stopped or
paused stay as they are. It's on unless you turn it off.

  agentbox continue-after-restart        say whether it's on
  agentbox continue-after-restart off    turn it off
  agentbox continue-after-restart on     turn it on`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			var settings api.Settings
			if len(args) == 1 {
				on, err := parseOnOff(args[0])
				if err != nil {
					return err
				}
				settings, err = c.UpdateSettings(cmd.Context(), api.UpdateSettingsRequest{ContinueAfterRestart: &on})
				if err != nil {
					return err
				}
			} else if settings, err = c.Settings(cmd.Context()); err != nil {
				return err
			}
			state := "off"
			if settings.ContinueAfterRestart {
				state = "on"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "continue agents after restarts: %s\n", state)
			return err
		},
	}
}

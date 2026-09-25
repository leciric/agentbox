package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/desktop"
	"agentbox/internal/mcp"
)

// newDesktopCmd gives an agent's AI tool the agent's own display: the mouse,
// the keyboard, the windows and screenshots of the whole screen. Like
// `agentbox mcp` it speaks the Model Context Protocol on stdin and stdout, and
// is started by the AI tool from the configuration AgentBox writes into the
// agent, not by hand (D64).
func newDesktopCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "desktop",
		Short: "The agent's virtual desktop: mouse, keyboard and screenshots of the whole display",
	}
	cmd.AddCommand(newDesktopMCPCmd(a))
	return cmd
}

func newDesktopMCPCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:    "mcp",
		Short:  "Serve the display's mouse, keyboard and screenshots over the Model Context Protocol",
		Hidden: true, // started by the agent's AI tool, not by people
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The display is the agent's own, on this machine. On the host
			// there is no :99 to drive and no agent this would mean, so it
			// says so here rather than failing tool by tool.
			if _, err := os.Stat(api.InAgentSocket); err != nil {
				return errors.New("agentbox desktop mcp runs inside an agent, on that agent's own display; " +
					"from here, watch an agent's display in AgentBox's Desktop tab")
			}
			srv := &mcp.Server{Name: "agentbox-desktop", Version: version, Tools: desktop.Tools(cmd.Context())}
			return srv.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

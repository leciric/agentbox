package cli

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

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
	cmd.AddCommand(newDesktopMCPCmd(a), newDesktopInputLogCmd(), newDesktopOverlayCmd())
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

// newDesktopInputLogCmd and newDesktopOverlayCmd are the two halves of the
// overlay on a recording made with --input desktop: the recording script runs
// input-log beside ffmpeg while it records, and at the end draws what it
// logged onto the video with overlay's subtitles.
func newDesktopInputLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "input-log <file>",
		Short:  "Log the keys and buttons pressed on the display, one JSON object a line, until stopped",
		Hidden: true, // run by the recording script
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.OpenFile(args[0], os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
			if err != nil {
				return err
			}
			defer f.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return desktop.LogInput(ctx, f)
		},
	}
}

func newDesktopOverlayCmd() *cobra.Command {
	var opts desktop.OverlayOptions
	var events string
	cmd := &cobra.Command{
		Use:    "overlay",
		Short:  "Write the subtitles that draw logged input onto a recording, as ASS",
		Hidden: true, // run by the recording script
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := os.Open(events)
			if err != nil {
				return err
			}
			defer f.Close()
			evs, err := desktop.ReadInputLog(f)
			if err != nil {
				return err
			}
			_, err = io.WriteString(cmd.OutOrStdout(), desktop.Overlay(evs, opts))
			return err
		},
	}
	cmd.Flags().StringVar(&events, "events", "", "the file input-log wrote")
	cmd.Flags().Float64Var(&opts.Start, "start", 0, "when the video's first frame was taken, in seconds since the Unix epoch")
	cmd.Flags().IntVar(&opts.Width, "width", 1440, "the video's width")
	cmd.Flags().IntVar(&opts.Height, "height", 900, "the video's height")
	cmd.Flags().IntVar(&opts.Bottom, "bottom", 0, "how far above the bottom edge the key captions' centre sits")
	_ = cmd.MarkFlagRequired("events")
	_ = cmd.MarkFlagRequired("start")
	return cmd
}

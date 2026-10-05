package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

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
	cmd.AddCommand(newDesktopMCPCmd(a), newDesktopRecordCmd())
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
			socket := inAgentSocket()
			if _, err := os.Stat(socket); err != nil {
				if insideAgent() {
					return fmt.Errorf("the in-agent API socket %s is missing", socket)
				}
				return errors.New("agentbox desktop mcp runs inside an agent, on that agent's own display; " +
					"from here, watch an agent's display in AgentBox's Desktop tab")
			}
			srv := &mcp.Server{Name: "agentbox-desktop", Version: version, Tools: desktop.Tools(cmd.Context())}
			return srv.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// newDesktopRecordCmd is the recorder a recording runs in the agent: one
// process that captures, draws the input on and encodes, until it is
// interrupted or reaches its limit. It writes what it records, display or
// browser, to --ready once the file has its first frames, which is what the
// recording script waits for.
func newDesktopRecordCmd() *cobra.Command {
	opts := desktop.RecordOptions{Target: desktop.RecordAuto}
	var ready string
	cmd := &cobra.Command{
		Use:    "record <file>",
		Short:  "Record the display or the browser's page to an MP4, until interrupted",
		Hidden: true, // run by the recording script
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Out = args[0]
			opts.Log = cmd.ErrOrStderr()
			if ready != "" {
				opts.Started = func(target string) { _ = os.WriteFile(ready, []byte(target+"\n"), 0o644) }
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// The first signal has ffmpeg finish the file; a second one, from
			// a caller done waiting, ends the recorder as usual.
			context.AfterFunc(ctx, stop)
			return desktop.Record(ctx, opts)
		},
	}
	cmd.Flags().StringVar(&opts.Target, "target", desktop.RecordAuto, "auto, display or browser")
	cmd.Flags().StringVar(&opts.Input, "input", "playwright", "playwright, or desktop to draw the keys, clicks and cursor")
	cmd.Flags().DurationVar(&opts.Limit, "limit", 10*time.Minute, "stop after this long")
	cmd.Flags().IntVar(&opts.Bottom, "bottom", 0, "how far above the display's bottom edge the key caption's centre sits")
	cmd.Flags().StringVar(&ready, "ready", "", "a file to write what is recorded to, once it is")
	return cmd
}

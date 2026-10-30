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
	cmd.AddCommand(newDesktopMCPCmd(a), newDesktopGuardCmd(), newDesktopRecordCmd(), newDesktopEditCmd())
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

// newDesktopGuardCmd runs another MCP server, the Playwright one, with a
// deadline on each of its tool calls (mcp.Guard): the AI tool starts it from
// the configuration AgentBox writes into the agent.
func newDesktopGuardCmd() *cobra.Command {
	var timeout time.Duration
	var name string
	cmd := &cobra.Command{
		Use:    "guard -- <command> [args...]",
		Short:  "Run an MCP server with a deadline on each tool call, restarting it when one is missed",
		Hidden: true, // started by the agent's AI tool, not by people
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g := &mcp.Guard{Command: args, Timeout: timeout, Name: name, Stderr: cmd.ErrOrStderr()}
			return g.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "how long one tool call may take")
	cmd.Flags().StringVar(&name, "name", "MCP", "the server's name, for the message a missed deadline gets")
	return cmd
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
	cmd.Flags().BoolVar(&opts.NoTrim, "no-trim", false, "keep every frame, the screen standing still included")
	cmd.Flags().BoolVar(&opts.WholeDisplay, "whole-display", false, "film the display as it is, without making the browser fullscreen first")
	cmd.Flags().StringVar(&opts.Cuts, "cuts", "", "a file to write the stretches cut out to, as JSON, once the recording is finished")
	cmd.Flags().StringVar(&opts.Events, "events", "", "with --input desktop, a file to write the clicks and keys to, one JSON object a line")
	return cmd
}

// newDesktopEditCmd edits a recording: the camera zooms in on where the input
// is, and the notes are captions on it. StopRecording runs it in the agent,
// on what the recorder left beside the video.
func newDesktopEditCmd() *cobra.Command {
	var events, cuts, notes, title, subtitle string
	var start float64
	cmd := &cobra.Command{
		Use:    "edit <in> <out>",
		Short:  "Edit a recording: zoom in on the clicks and caption the chapters",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := desktop.ReadEdit(events, cuts, notes, start)
			if err != nil {
				return err
			}
			e.Title, e.Subtitle = title, subtitle
			return e.Render(cmd.Context(), args[0], args[1], os.Stderr)
		},
	}
	cmd.Flags().StringVar(&events, "events", "", "the recorder's --events file")
	cmd.Flags().StringVar(&cuts, "cuts", "", "the recorder's --cuts file")
	cmd.Flags().StringVar(&notes, "notes", "", `the chapters, one {"t", "text"} a line`)
	cmd.Flags().Float64Var(&start, "start", 0, "when the recording started, in Unix seconds, for one with no --cuts")
	cmd.Flags().StringVar(&title, "title", "", "a title card to open on")
	cmd.Flags().StringVar(&subtitle, "subtitle", "", "a line under the title")
	return cmd
}

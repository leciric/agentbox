package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/mcp"

	"github.com/spf13/cobra"
)

// An agent's run phase (internal/daemon/runslots.go): the app, docker
// compose, Playwright, its desktop, browser or emulator and its recordings
// take a run slot, and wait their turn for one; its code phase — editing,
// research, builds and targeted tests — never waits. `agentbox run up` waits
// for a slot (in the background, while the agent keeps coding), `agentbox
// run up -- <cmd>` runs a command in the run phase, and `agentbox run down`
// stops what the run phase started and gives the slot back. The shims do
// the same for a dev server, Playwright or `docker compose up`
// (isRunPhase), and `agentbox heavy` for whatever it runs.

// runClient is the part of the in-agent API the run phase goes through.
type runClient interface {
	RunUp(ctx context.Context, req api.RunUpRequest) (api.RunPhase, error)
	RunStatus(ctx context.Context) (api.RunPhase, error)
	RunJoin(ctx context.Context, pid int) error
	RunDown(ctx context.Context) (api.RunPhase, error)
}

// runWaitSeconds is how long each ask for a slot waits before asking again,
// which keeps its place in the queue.
const runWaitSeconds = 25

// runBeat is how often a run-phase test run renews the slot while it runs.
const runBeat = 30 * time.Second

func newRunCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Your run phase: a run slot for the app, docker compose, Playwright, the desktop and recordings",
		Long: `Inside an agent, running the app, docker compose, Playwright, the desktop,
the browser, the Android emulator or a recording is your run phase, and takes
one of the VM's run slots: as many as its memory fits at once. Editing,
builds and targeted tests are your code phase, which never waits.

  agentbox run up              wait for a run slot (run it in the background and
                               keep coding until it returns)
  agentbox run up -- <cmd>     wait for one, then run <cmd> in your run phase
  agentbox run status          where you stand: holding one, or how many are ahead
  agentbox run down            stop what your run phase started, and give it back

A slot lasts ten minutes after you last used your run phase; then what it
started is stopped.`,
	}
	cmd.AddCommand(newRunUpCmd(a), newRunStatusCmd(a), newRunDownCmd(a))
	return cmd
}

// inAgentRunClient is the in-agent API, or an error outside an agent.
func inAgentRunClient() (*api.Client, error) {
	socket := inAgentSocket()
	if !fileExists(socket) {
		if insideAgent() {
			return nil, fmt.Errorf("the in-agent API socket %s is missing", socket)
		}
		return nil, errors.New("run slots are an agent's: run this inside one")
	}
	return api.NewClient(socket), nil
}

func newRunUpCmd(_ *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up [-- <command> [args...]]",
		Short: "Wait for a run slot; with a command, run it in your run phase",
		Example: `  agentbox run up
  agentbox run up -- npm run dev
  agentbox run up -- docker compose up`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				var c heavyClient
				if socket := inAgentSocket(); fileExists(socket) {
					c = api.NewClient(socket)
				}
				if args[0] == "--" {
					args = args[1:]
				}
				if len(args) == 0 {
					return errors.New("say which command to run")
				}
				code, err := runHeavyCommand(cmd.Context(), c, heavyCommandRun{path: args[0], argv: args, command: strings.Join(args, " "), run: true, beat: isRunTest(strings.Join(args, " "))}, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				if code != 0 {
					return exitCodeError(code)
				}
				return nil
			}
			c, err := inAgentRunClient()
			if err != nil {
				return err
			}
			ph, err := waitRunSlot(cmd.Context(), c, "", cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), runHeld(ph))
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newRunStatusCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Say whether you hold a run slot, or how many are ahead of you",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := inAgentRunClient()
			if err != nil {
				return err
			}
			ph, err := c.RunStatus(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch ph.State {
			case api.RunSlotHolding:
				_, _ = fmt.Fprintln(out, runHeld(ph))
			case api.RunSlotWaiting:
				_, _ = fmt.Fprintf(out, "Waiting for a run slot: %d ahead of you.\n", ph.Ahead)
			default:
				_, _ = fmt.Fprintf(out, "Code phase: you hold no run slot (%d of %d free). `agentbox run up` waits for one.\n", ph.Free, ph.Total)
			}
			return nil
		},
	}
}

func newRunDownCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop what your run phase started, and give its run slot back",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := inAgentRunClient()
			if err != nil {
				return err
			}
			ph, err := c.RunDown(cmd.Context())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), runEnded(ph))
			return nil
		},
	}
}

// runHeld says the agent holds a run slot.
func runHeld(ph api.RunPhase) string {
	until := ""
	if ph.Expires != nil {
		until = fmt.Sprintf(", until %s unless you use it", ph.Expires.Local().Format("15:04"))
	}
	return fmt.Sprintf("You hold a run slot%s. Start the app with `agentbox run up -- <command>`, and run `agentbox run down` when you are done.", until)
}

// runEnded says what ending the run phase did.
func runEnded(ph api.RunPhase) string {
	var b strings.Builder
	b.WriteString("Run phase ended")
	if len(ph.Stopped) > 0 {
		b.WriteString(": stopped " + strings.Join(ph.Stopped, ", ") + ", and what ran through it")
	}
	if ph.Peak > 0 {
		fmt.Fprintf(&b, " (your machine peaked at %s)", agent.HumanBytes(ph.Peak))
	}
	b.WriteString(".")
	return b.String()
}

// waitRunSlot asks for a run slot until the agent holds one, saying on
// stderr where it waits as that changes. command is what it is for.
func waitRunSlot(ctx context.Context, c runClient, command string, stderr io.Writer) (api.RunPhase, error) {
	last := -1
	for {
		ph, err := c.RunUp(ctx, api.RunUpRequest{WaitSeconds: runWaitSeconds, Command: command})
		if err != nil {
			return ph, err
		}
		switch ph.State {
		case api.RunSlotHolding:
			if last >= 0 {
				_, _ = fmt.Fprintln(stderr, "agentbox: you have a run slot now")
			}
			return ph, nil
		case api.RunSlotWaiting:
			if ph.Ahead != last {
				_, _ = fmt.Fprintf(stderr, "agentbox: waiting for a run slot (%d ahead); every run slot is in use, so keep coding meanwhile\n", ph.Ahead)
				last = ph.Ahead
			}
		}
		if err := ctx.Err(); err != nil {
			return ph, err
		}
	}
}

// inRunPhase runs, inside an agent, before a start of the agent's own that
// is its run phase (the browser, the emulator, a recording): it waits for
// a run slot first, as `agentbox run up` does. On a daemon without run
// slots, it goes on.
func inRunPhase(ctx context.Context, ref string, stderr io.Writer) error {
	if ref != "" {
		return nil // the user's, from the host
	}
	socket := inAgentSocket()
	if !fileExists(socket) {
		return nil
	}
	_, err := waitRunSlot(ctx, api.NewClient(socket), "", stderr)
	if err != nil && !noRunSlots(err) {
		return err
	}
	return nil
}

// noRunSlots reports whether err is a daemon from before run slots refusing
// the ask.
func noRunSlots(err error) bool {
	var se *api.StatusError
	return errors.As(err, &se) && (se.Code == 404 || se.Code == 403 || se.Code == 405)
}

// runPhaseCommand matches a command that is a run phase's: the app, a dev
// server, Playwright's and e2e tests, docker compose up and docker run.
// Builds and unit tests aren't. Only a command's own words count, where a
// command starts, as in heavyCommand.
var runPhaseCommand = regexp.MustCompile(`(?m)(?:^|[;&|(\x60]|\$\(|-c\s+["']|\b(?:then|do|else)\s)\s*` +
	`(?:(?:[A-Za-z_][A-Za-z0-9_]*=\S*|sudo|time|nice|nohup|env|exec|command|timeout\s+\S+)\s+)*` +
	`(?:\S*/)?(?:` +
	`(?:npm|pnpm|yarn|bun)\s+(?:--prefix[\s=]\S+\s+|-C\s+\S+\s+|--filter[\s=]\S+\s+)*(?:run\s+)?(?:dev|start|preview|serve|e2e|storybook)\b|` +
	`(?:(?:npx|pnpx|bunx)\s+)?(?:playwright\s+test|cypress\s+(?:run|open)|next\s+(?:dev|start)|vp\s+(?:dev|preview))\b|` +
	`(?:(?:npx|pnpx|bunx)\s+)?electron(?:\s|$)|` +
	`(?:(?:npx|pnpx|bunx)\s+)?vite(?:\s+(?:dev|serve|preview|--host|--port)\b|\s*(?:$|[;&|)]))|` +
	`docker\s+(?:run|start|compose\s+(?:\S+\s+)*?(?:up|start|run))\b|docker-compose\s+(?:up|start|run)\b` +
	`)`)

// isRunPhase reports whether command is a run phase's.
func isRunPhase(command string) bool {
	return runPhaseCommand.MatchString(withoutHeredocs(command))
}

// runTest matches a run phase's command that ends by itself: a test run,
// which renews the slot while it goes. A dev server doesn't: one left
// running doesn't keep the slot.
var runTest = regexp.MustCompile(`\b(?:playwright\s+test|cypress\s+run|e2e)\b`)

func isRunTest(command string) bool { return runTest.MatchString(command) }

// runSlotGate has the desktop's tools need a run slot: the desktop is the
// run phase. A slot held is renewed, at most every runBeat; one free is
// taken; with none, the call is refused with what to do instead.
type runSlotGate struct {
	c    runClient
	mu   sync.Mutex
	last time.Time
	now  func() time.Time
}

func (g *runSlotGate) check(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if !g.last.IsZero() && now.Sub(g.last) < runBeat {
		return nil
	}
	ph, err := g.c.RunUp(ctx, api.RunUpRequest{Try: true, Browser: true, Command: "the desktop"})
	if err != nil {
		// No daemon, or one before run slots: the tool runs.
		return nil
	}
	switch ph.State {
	case api.RunSlotHolding:
		g.last = now
		return nil
	case api.RunSlotWaiting:
		return fmt.Errorf("you are waiting for a run slot (%d ahead): the desktop is your run phase, and it needs one. "+
			"Keep coding until `agentbox run up` returns, then use the desktop", ph.Ahead)
	}
	return fmt.Errorf("no run slot is free (%d %s waiting): the desktop is your run phase, and it needs one. "+
		"Run `agentbox run up` in the background, keep coding until it returns, then use the desktop; `agentbox run down` when you are done",
		ph.Ahead, map[bool]string{true: "agent is", false: "agents are"}[ph.Ahead == 1])
}

// needingRunSlot has each of tools check g before it runs.
func needingRunSlot(tools []mcp.Tool, g *runSlotGate) []mcp.Tool {
	out := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		if run := t.Run; run != nil {
			t.Run = func(args json.RawMessage) (string, error) {
				if err := g.check(context.Background()); err != nil {
					return "", err
				}
				return run(args)
			}
		}
		if run := t.RunContent; run != nil {
			t.RunContent = func(args json.RawMessage) ([]mcp.Content, error) {
				if err := g.check(context.Background()); err != nil {
					return nil, err
				}
				return run(args)
			}
		}
		if wait := t.Wait; wait != nil {
			t.Wait = func(ctx context.Context, args json.RawMessage) (string, error) {
				if err := g.check(ctx); err != nil {
					return "", err
				}
				return wait(ctx, args)
			}
		}
		out[i] = t
	}
	return out
}

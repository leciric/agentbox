package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
)

func newChatCmd(a *app) *cobra.Command {
	var (
		clear bool
		stop  bool
		set   []string
	)
	cmd := &cobra.Command{
		Use:   "chat <project>|<project/agent> [message]",
		Short: "Talk to a project, or to one of its agents",
		Long: `Talk to a project or to one of its agents through the chat the app shows.

Name a project, like pawly, for the project's own chat. It directs the project's
agents and does none of the work itself: it has no machine, costs nothing until
you write to it, and cannot run or change anything.

Name an agent, like pawly/agent-01, for that agent's AI tool inside its machine.

Without a message, chat prints the conversation. With one, it sends the message
and follows the reply until the turn ends. Ctrl-C stops following while the turn
keeps running; stop the turn with --stop. When the tool asks for permission,
chat asks you here if this is a terminal; otherwise, answer in the app.`,
		Example: `  agentbox chat pawly "What does the reminders page do?"
  agentbox chat pawly/agent-01 "Add a page that lists reminders"
  agentbox chat pawly/agent-01 --set model=haiku --set mode=bypassPermissions
  agentbox chat pawly/agent-01 --new "Start over: fix the login test"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, message := args[0], strings.TrimSpace(strings.Join(args[1:], " "))
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if stop {
				if _, err := c.CancelChat(ctx, ref); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Stopped the running turn")
				return nil
			}
			if clear {
				if err := c.ClearChat(ctx, ref); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Started a new conversation")
			}
			if len(set) > 0 {
				if err := setChatOptions(cmd, c, ref, set); err != nil {
					return err
				}
			}
			switch {
			case message != "":
				return followTurn(cmd, c, ref, message)
			case clear || len(set) > 0:
				return nil
			}
			thread, err := c.Chat(ctx, ref)
			if err != nil {
				return err
			}
			if len(thread.Items) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No messages yet. Send one with: agentbox chat %s \"<message>\"\n", ref)
				return nil
			}
			p := newChatPrinter(cmd.OutOrStdout())
			for _, it := range thread.Items {
				p.item(it)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&clear, "new", false, "start a new conversation first; the AI tool forgets this one")
	f.BoolVar(&stop, "stop", false, "stop the running turn")
	f.StringArrayVar(&set, "set", nil, "change a setting of the session, like model=haiku (repeat for more)")
	return cmd
}

func newInterfaceCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "interface <project/agent> [chat|cli]",
		Short: "Show or switch how you use an agent's AI tool: the chat, or its command line in the terminal",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			var ag api.Agent
			if len(args) == 1 {
				ag, err = c.Agent(cmd.Context(), args[0])
			} else {
				ag, err = c.UpdateAgent(cmd.Context(), args[0], api.UpdateAgentRequest{Interface: &args[1]})
			}
			if err != nil {
				return err
			}
			switch {
			case ag.AI == "none":
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s has no AI tool, only a shell\n", ag.Ref)
			case ag.Interface == "chat":
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s uses the chat: the app's Chat tab, or agentbox chat %s\n", ag.Ref, ag.Ref)
			default:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s uses %s's command line: agentbox shell %s\n", ag.Ref, describeAI(ag.AI, false), ag.Ref)
			}
			return nil
		},
	}
}

// setChatOptions starts the agent's AI tool, whose session says what it offers,
// and changes each name=value setting.
func setChatOptions(cmd *cobra.Command, c *api.Client, ref string, settings []string) error {
	ctx := cmd.Context()
	session, err := c.StartChat(ctx, ref)
	if err != nil {
		return err
	}
	for deadline := time.Now().Add(3 * time.Minute); session.State == api.ChatStarting; {
		if time.Now().After(deadline) {
			return errors.New("the AI tool didn't start within 3 minutes")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		thread, err := c.Chat(ctx, ref)
		if err != nil {
			return err
		}
		session = thread.Session
	}
	if session.State == api.ChatError {
		return errors.New(session.Error)
	}
	for _, setting := range settings {
		id, value, ok := strings.Cut(setting, "=")
		if !ok {
			return fmt.Errorf("invalid setting %q: use name=value, like model=haiku", setting)
		}
		i := slices.IndexFunc(session.Options, func(o api.ChatOption) bool { return o.ID == id })
		if i < 0 {
			var ids []string
			for _, o := range session.Options {
				ids = append(ids, o.ID)
			}
			return fmt.Errorf("no setting %q: the settings are %s", id, strings.Join(ids, ", "))
		}
		option := session.Options[i]
		if session, err = c.SetChatOption(ctx, ref, id, value); err != nil {
			var choices []string
			for _, ch := range option.Choices {
				choices = append(choices, ch.Value)
			}
			if option.Type == "boolean" {
				choices = []string{"true", "false"}
			}
			return fmt.Errorf("%w (choices: %s)", err, strings.Join(choices, ", "))
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", option.Name, value)
	}
	return nil
}

// followTurn sends a message and prints the turn it starts as it happens.
func followTurn(cmd *cobra.Command, c *api.Client, ref, message string) error {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	stderr := cmd.ErrOrStderr()

	// Events only say that something changed; the thread is fetched again, at
	// most every 150 ms, and every 2 s in case the stream misses something.
	changed := make(chan struct{}, 1)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- c.Events(ctx, func(ev api.Event) error {
			var chat api.ChatEvent
			if ev.Type == api.EventChat && json.Unmarshal(ev.Data, &chat) == nil && chat.Agent == ref {
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			return nil
		})
	}()

	user, err := c.SendChat(ctx, ref, message)
	if err != nil {
		return err
	}
	var in *bufio.Reader
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		in = bufio.NewReader(f)
	}
	p := newChatPrinter(cmd.OutOrStdout())
	p.shown[user.ID] = true // you know what you sent, wherever it landed
	asked := map[string]bool{}
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		thread, err := c.Chat(ctx, ref)
		if ctx.Err() != nil {
			return detachFromTurn(stderr, ref)
		}
		if err != nil {
			return err
		}
		// The turn to follow is the one the message is in. A message sent while
		// the tool was working joined the turn that was already running, so
		// that's the one whose answer and ending are ours to wait for — and if
		// it couldn't join, it heads a turn of its own instead. Both are read
		// off the message itself, which the daemon updates either way.
		turn := user.ID
		for _, it := range thread.Items {
			if it.ID == user.ID && it.Kind == "aside" && it.Turn != "" {
				turn = it.Turn
			}
		}
		var result *api.ChatTurnResult
		for _, it := range thread.Items {
			if it.Turn != turn {
				continue
			}
			if it.ID == turn {
				result = it.Result
				continue
			}
			p.item(it)
			if it.Kind != "permission" || it.Permission.Outcome != "" || asked[it.ID] {
				continue
			}
			asked[it.ID] = true
			if in == nil {
				_, _ = fmt.Fprintf(stderr, "  ? %s: answer in the app\n", it.Permission.Title)
			} else if err := askPermission(ctx, c, ref, it, in, stderr); err != nil {
				return err
			}
		}
		if result != nil {
			switch result.State {
			case "completed":
				return nil
			case "cancelled":
				return errors.New("the turn was stopped")
			}
			return errors.New("the turn failed")
		}
		wait := time.After(150 * time.Millisecond)
		select {
		case <-changed:
		case <-poll.C:
		case err := <-streamErr:
			if ctx.Err() != nil {
				return detachFromTurn(stderr, ref)
			}
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "(the event stream ended: %v; checking every 2 s)\n", err)
			}
			streamErr = nil
		case <-ctx.Done():
			return detachFromTurn(stderr, ref)
		}
		<-wait
	}
}

func detachFromTurn(w io.Writer, ref string) error {
	_, _ = fmt.Fprintf(w, "\nStopped following: the turn keeps running. Stop it with: agentbox chat %s --stop\n", ref)
	return exitCodeError(130)
}

func askPermission(ctx context.Context, c *api.Client, ref string, it api.ChatItem, in *bufio.Reader, w io.Writer) error {
	perm := it.Permission
	_, _ = fmt.Fprintf(w, "\n  ? %s\n", perm.Title)
	for i, o := range perm.Options {
		_, _ = fmt.Fprintf(w, "    %d. %s\n", i+1, o.Name)
	}
	for {
		_, _ = fmt.Fprintf(w, "  Choose 1-%d: ", len(perm.Options))
		line, err := in.ReadString('\n')
		if err != nil {
			return err
		}
		if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && n >= 1 && n <= len(perm.Options) {
			if _, err := c.AnswerChat(ctx, ref, it.ID, perm.Options[n-1].ID); err != nil {
				_, _ = fmt.Fprintf(w, "  %v\n", err) // answered in the app meanwhile, or the turn ended
			}
			_, _ = fmt.Fprintln(w)
			return nil
		}
	}
}

// chatPrinter writes a conversation as text, and picks up where it left off as
// its items change.
type chatPrinter struct {
	out     io.Writer
	printed map[string]int // how much of each message's text is out
	shown   map[string]bool
}

func newChatPrinter(out io.Writer) *chatPrinter {
	return &chatPrinter{out: out, printed: map[string]int{}, shown: map[string]bool{}}
}

func (p *chatPrinter) item(it api.ChatItem) {
	// What a subagent did is shown under it, indented, and its words are left
	// out: they are its report to the agent, which the agent's own answer
	// carries on from (D86).
	if it.Parent != "" {
		if it.Kind == "tool" {
			p.tool(it, "      ")
		}
		return
	}
	switch it.Kind {
	case "subagent":
		sa := it.Subagent
		if sa == nil {
			return
		}
		p.once(it.ID, fmt.Sprintf("  ⧉ subagent %s: %s\n", sa.Name, oneLine(sa.Task)))
		if sa.State != "running" {
			p.once(it.ID+":end", fmt.Sprintf("  ⧉ %s %s\n", sa.Name, sa.State))
		}
	case "user":
		p.once(it.ID, "› "+it.Text+"\n\n")
	case "aside":
		// Sent while the tool was already working, so it reads in the middle of
		// the turn, where it landed.
		p.once(it.ID, "\n› "+it.Text+deliveryNote(it.Delivery)+"\n\n")
	case "assistant":
		if n := p.printed[it.ID]; n < len(it.Text) {
			_, _ = fmt.Fprint(p.out, it.Text[n:])
			p.printed[it.ID] = len(it.Text)
		}
		if !it.Streaming {
			p.once(it.ID, "\n\n")
		}
	case "tool":
		p.tool(it, "  ")
	case "permission":
		if outcome := it.Permission.Outcome; outcome != "" {
			for _, o := range it.Permission.Options {
				if o.ID == outcome {
					outcome = o.Name
				}
			}
			p.once(it.ID, "  ? "+it.Permission.Title+": "+outcome+"\n")
		}
	case "notice", "error":
		p.once(it.ID, "! "+it.Text+"\n")
	}
}

// deliveryNote says what became of a message sent mid-turn, when that isn't
// simply "the tool has it".
func deliveryNote(delivery string) string {
	switch delivery {
	case api.ChatAsideDeferred:
		return "  (waiting for this turn to end)"
	case api.ChatAsideLost:
		return "  (never reached the AI tool)"
	}
	return ""
}

func (p *chatPrinter) once(key, text string) {
	if !p.shown[key] {
		p.shown[key] = true
		_, _ = fmt.Fprint(p.out, text)
	}
}

// tool prints a tool call once it has ended, at the indent given.
func (p *chatPrinter) tool(it api.ChatItem, indent string) {
	t := it.Tool
	if t == nil {
		return
	}
	line := t.Title
	if t.Kind == "execute" && t.Command != "" {
		line = t.Command
	}
	switch t.Status {
	case "completed":
		p.once(it.ID, indent+"✓ "+line+"\n")
	case "failed":
		p.once(it.ID, indent+"✗ "+line+"\n")
	case "stopped":
		p.once(it.ID, indent+"◼ "+line+" (stopped)\n")
	}
}

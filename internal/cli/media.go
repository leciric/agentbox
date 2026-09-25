package cli

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

var (
	refPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)
	// projectPattern is a bare project name, which media list also accepts.
	projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// splitRef takes an agent off the front of args when there are more than the
// command's own n arguments: the host names the agent first, an agent leaves it out.
func splitRef(args []string, n int) (string, []string) {
	if len(args) > n && refPattern.MatchString(args[0]) {
		return args[0], args[1:]
	}
	return "", args
}

func newMediaCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "media",
		Short: "Screenshots, recordings, reports, logs and notes that show an agent's work (the app's Media tab)",
		Long: `Media is what shows an agent's work: screenshots, recordings, test reports, logs and notes.
It appears in the app's Media tab. Inside an agent, leave out the agent: the commands work on its own media.`,
	}
	record := &cobra.Command{Use: "record", Short: "Record the agent's display as a video"}
	record.AddCommand(newRecordStartCmd(a), newRecordStopCmd(a), newRecordStatusCmd(a))
	cmd.AddCommand(
		newMediaListCmd(a),
		newMediaScreenshotCmd(a),
		record,
		newMediaAddCmd(a),
		newMediaNoteCmd(a),
		newMediaLogsCmd(a),
		newMediaOpenCmd(a),
		newMediaExportCmd(a),
		newMediaRmCmd(a),
		newMediaDeleteCmd(a),
		newMediaRetentionCmd(a),
	)
	return cmd
}

// newMediaRetentionCmd shows or sets how long a removed agent's media is
// kept, for the whole installation, mirroring newAutonomyCmd's show-or-set
// shape.
func newMediaRetentionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "retention [immediately|1d|7d|30d|forever]",
		Short: "How long a removed agent's media is kept (1d by default)",
		Long: `Shows or sets how long media is kept once its agent has been destroyed,
before the daemon purges it. It applies to every project. "immediately"
deletes an agent's media with it, and "forever" never purges it. Media of an
agent that still exists never expires, however old.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			var settings api.Settings
			if len(args) == 0 {
				settings, err = c.Settings(cmd.Context())
			} else {
				settings, err = c.SetMediaRetention(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), settings.MediaRetention)
			return nil
		},
	}
}

func printSaved(cmd *cobra.Command, item api.MediaItem) {
	what := item.Kind
	if item.Size > 0 {
		what += ", " + humanBytes(item.Size)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Saved %q (%s) as %s\n", item.Name, what, item.ID)
}

func newMediaListCmd(a *app) *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:     "list [agent|project]",
		Aliases: []string{"ls"},
		Short:   "List media, newest first: an agent's, or a whole project's",
		Long: `Lists media newest first.

Name an agent (pawly/agent-01) for that agent's media, or a project (pawly) for
every agent's, with the agent each item came from. Inside an agent, name
nothing: it lists its own.`,
		Example: `  agentbox media list pawly              # the whole project, labelled by agent
  agentbox media list pawly --kind screenshot
  agentbox media list pawly/agent-01`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// An agent (pawly/agent-01), or a whole project (pawly).
			ref, project := "", ""
			if len(args) == 1 {
				switch {
				case refPattern.MatchString(args[0]):
					ref = args[0]
				case projectPattern.MatchString(args[0]):
					project = args[0]
				default:
					return fmt.Errorf("%q is neither a project nor an agent: use <project> or <project>/<agent>", args[0])
				}
			}
			whole := project != ""
			// A whole project is a host question: inside an agent there is only
			// that agent's own media, and asking for a project's is refused.
			var (
				c     *api.Client
				err   error
				items []api.MediaItem
			)
			if whole {
				if c, err = a.client(cmd); err != nil {
					return err
				}
				items, err = c.ProjectMedia(cmd.Context(), project, "", kind)
			} else {
				if c, err = a.scopedClient(cmd, ref); err != nil {
					return err
				}
				items, err = c.Media(cmd.Context(), ref)
			}
			if err != nil {
				return err
			}
			if kind != "" && !whole {
				items = slices.DeleteFunc(items, func(it api.MediaItem) bool { return it.Kind != kind })
			}
			out := cmd.OutOrStdout()
			if len(items) == 0 {
				fmt.Fprintln(out, "No media yet")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			if whole {
				fmt.Fprintln(w, "ID\tAGENT\tKIND\tNAME\tFROM\tSIZE\tCREATED")
			} else {
				fmt.Fprintln(w, "ID\tKIND\tNAME\tFROM\tSIZE\tCREATED")
			}
			for _, item := range items {
				from, size := "agent", "-"
				if item.Source == "user" {
					from = "you"
				}
				if item.Size > 0 {
					size = humanBytes(item.Size)
				}
				if whole {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.ID, mediaAgent(item), item.Kind, item.Name, from, size, ago(item.CreatedAt))
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", item.ID, item.Kind, item.Name, from, size, ago(item.CreatedAt))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "only this kind: screenshot, recording, report, log, note or file")
	return cmd
}

// mediaAgent labels an item with the agent it came from, and what that agent is
// for, so a project's media says what each item is about at a glance. A gone
// agent is called out: the item was kept past a destroy, not deleted with it.
func mediaAgent(item api.MediaItem) string {
	name := item.AgentName
	if name == "" {
		_, name, _ = strings.Cut(item.Agent, "/")
	}
	if item.AgentTitle != "" {
		name += " · " + item.AgentTitle
	}
	if item.AgentGone {
		name += " (removed)"
	}
	return name
}

func newMediaScreenshotCmd(a *app) *cobra.Command {
	var req api.ScreenshotRequest
	var display bool
	cmd := &cobra.Command{
		Use:   "screenshot [agent]",
		Short: "Take a screenshot of the browser's current page, or of the whole display",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			if display {
				req.Target = "display"
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.Screenshot(cmd.Context(), ref, req)
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Name, "name", "", `a name, like "empty-state"`)
	cmd.Flags().BoolVar(&display, "display", false, "the whole display instead of the browser's page")
	cmd.Flags().BoolVar(&req.FullPage, "full-page", false, "the whole page, not just the visible part")
	return cmd
}

func newRecordStartCmd(a *app) *cobra.Command {
	var name, input string
	var limit time.Duration
	cmd := &cobra.Command{
		Use:   "start [agent]",
		Short: "Start recording the agent's display",
		Long: `Start recording the agent's display.

With --input desktop the recording also shows how it was driven: the mouse
cursor, a ripple where it clicks, and the keys pressed as a caption over the
dock, drawn onto the video when it stops. Use it for a flow driven through the
desktop, with xdotool; Playwright's input is synthesized inside Chromium, where
neither the cursor nor the overlay sees it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.StartRecording(cmd.Context(), ref, api.RecordRequest{Input: input, Name: name, LimitSeconds: int(limit.Seconds())})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recording %q%s. Run agentbox media record stop when you're done (it stops by itself after %s)\n",
				status.Name, recordInput(status.Input), time.Duration(status.LimitSeconds)*time.Second)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", `a name, like "checkout-flow"`)
	cmd.Flags().StringVar(&input, "input", "playwright", "playwright, or desktop to show the mouse cursor and the keys pressed")
	cmd.Flags().DurationVar(&limit, "limit", 10*time.Minute, "stop by itself after this long (at most 1h)")
	return cmd
}

// recordInput names the mode in a sentence, and says nothing for the default.
func recordInput(input string) string {
	if input == "desktop" {
		return " with the keys and mouse"
	}
	return ""
}

func newRecordStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [agent]",
		Short: "Stop the recording and keep it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.StopRecording(cmd.Context(), ref)
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
}

func newRecordStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status [agent]",
		Short: "Show whether a recording is running",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.Recording(cmd.Context(), ref)
			if err != nil {
				return err
			}
			if !status.Recording {
				fmt.Fprintln(cmd.OutOrStdout(), "Not recording")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recording %q%s since %s\n", status.Name, recordInput(status.Input), ago(*status.StartedAt))
			return nil
		},
	}
}

func newMediaAddCmd(a *app) *cobra.Command {
	var req api.AddMediaRequest
	cmd := &cobra.Command{
		Use:   "add [agent] <path>",
		Short: "Keep a file from the agent: a test report, a log, an image",
		Long: `Keep a single file from the agent's machine: JUnit XML shows its pass and fail counts. A
directory is refused unless it has an index.html (like playwright-report/), which becomes a report
you can open; zip other directories first, or point at the one file that matters. On the host,
relative paths are relative to the agent's worktree.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) != 1 {
				return errors.New("usage: agentbox media add [agent] <path>")
			}
			req.Path = rest[0]
			if ref == "" && !filepath.IsAbs(req.Path) {
				abs, err := filepath.Abs(req.Path)
				if err != nil {
					return err
				}
				req.Path = abs
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.AddMedia(cmd.Context(), ref, req)
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Kind, "kind", "", "screenshot, recording, report, log or file (default: guessed)")
	cmd.Flags().StringVar(&req.Name, "name", "", "a name (default: the file name)")
	return cmd
}

func newMediaNoteCmd(a *app) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "note [agent] <text>",
		Short: "Leave a short note, like a summary of what was done",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) != 1 {
				return errors.New(`usage: agentbox media note [agent] "<text>"`)
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.AddNote(cmd.Context(), ref, api.NoteRequest{Text: rest[0], Name: name})
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "a title (default: the note's first line)")
	return cmd
}

func newMediaLogsCmd(a *app) *cobra.Command {
	var req api.LogsRequest
	cmd := &cobra.Command{
		Use:   "logs [agent] (--service <name> | --terminal)",
		Short: "Keep a Docker Compose service's logs, or the terminal's scrollback",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.AddLogs(cmd.Context(), ref, req)
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Service, "service", "", "a Docker Compose service in the worktree")
	f.StringVar(&req.Since, "since", "30m", "for a service: how far back")
	f.BoolVar(&req.Terminal, "terminal", false, "the tmux session's scrollback")
	f.StringVar(&req.Window, "window", "", "for the terminal: the tmux window (default: the AI tool's)")
	f.StringVar(&req.Name, "name", "", "a name")
	return cmd
}

func newMediaOpenCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "open <project/agent> <id>",
		Short: "Open an item with your desktop's default app (on the host)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := hostMediaItem(cmd, a, args[0], args[1])
			if err != nil {
				return err
			}
			if item.Kind == "note" {
				fmt.Fprintln(cmd.OutOrStdout(), item.Text)
				return nil
			}
			path := item.Path
			if item.Meta.Entry != "" {
				path = filepath.Join(path, item.Meta.Entry)
			}
			if err := exec.Command("xdg-open", path).Start(); err != nil {
				return fmt.Errorf("opening %s: %w", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Opened %s\n", path)
			return nil
		},
	}
}

func newMediaRmCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <project/agent> <id>",
		Short: "Delete an item (on the host; agents can't delete media)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			item, err := hostMediaItem(cmd, a, args[0], args[1])
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.DeleteMedia(cmd.Context(), item.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %q\n", item.Name)
			return nil
		},
	}
}

// newMediaDeleteCmd is the bulk form of media rm: several items at once, or
// everything a filter matches, which is how you free the space media takes.
func newMediaDeleteCmd(a *app) *cobra.Command {
	var all, yes bool
	var agent, kind string
	cmd := &cobra.Command{
		Use:   "delete <project|project/agent> [id...]",
		Short: "Delete several items at once, or everything a filter matches",
		Long: `Deletes media in bulk, on the host: agents can't delete media.

Name a project and the IDs to delete, or pass --all for everything, narrowed by
--agent and --kind the same way media list is. Naming an agent
(pawly/agent-01) is the same as --agent agent-01.

It asks first; --yes answers for you, for scripts.`,
		Example: `  agentbox media delete pawly 20260101T120000-ab12cd 20260101T120500-34ef56
  agentbox media delete pawly --all --kind screenshot
  agentbox media delete pawly/agent-01 --all --yes`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := args[0]
			if refPattern.MatchString(project) {
				var named string
				project, named, _ = strings.Cut(project, "/")
				if agent != "" && agent != named {
					return fmt.Errorf("%s names agent %s, but --agent says %s", args[0], named, agent)
				}
				agent = named
			} else if !projectPattern.MatchString(project) {
				return fmt.Errorf("%q is neither a project nor an agent: use <project> or <project>/<agent>", args[0])
			}
			ids := args[1:]
			switch {
			case all && len(ids) > 0:
				return errors.New("name the items to delete, or pass --all, not both")
			case !all && len(ids) == 0:
				return errors.New("name the items to delete, or pass --all")
			case !all && kind != "":
				return errors.New("--kind narrows --all; naming items already says which ones")
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			question := fmt.Sprintf("Delete %d item(s)?", len(ids))
			if all {
				// What --all covers, so the question can name it and its size,
				// and so an empty filter stops here instead of at the daemon.
				items, err := c.ProjectMedia(cmd.Context(), project, agent, kind)
				if err != nil {
					return err
				}
				if len(items) == 0 {
					fmt.Fprintln(out, "No media matches")
					return nil
				}
				var bytes int64
				for _, item := range items {
					bytes += item.Size
				}
				question = fmt.Sprintf("Delete %s?", describeAllMedia(len(items), kind, agent))
				if bytes > 0 {
					question = fmt.Sprintf("Delete %s, freeing %s?", describeAllMedia(len(items), kind, agent), humanBytes(bytes))
				}
			}
			if !yes {
				ok, err := confirmPrompt(cmd, question)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(out, "Nothing deleted")
					return nil
				}
			}
			req := api.DeleteMediaRequest{IDs: ids, All: all, Agent: agent, Kind: kind}
			var result api.DeleteMediaResult
			if !all && agent != "" {
				// The agent's own route, so an ID from another agent is refused
				// rather than deleted because it shares the project. --all goes
				// through the project instead, so an agent that's been
				// destroyed, whose media is kept, can still be emptied by name.
				result, err = c.DeleteAgentMedia(cmd.Context(), project+"/"+agent, req)
			} else {
				result, err = c.DeleteProjectMedia(cmd.Context(), project, req)
			}
			if err != nil {
				return err
			}
			freed := ""
			if result.Bytes > 0 {
				freed = ", freed " + humanBytes(result.Bytes)
			}
			fmt.Fprintf(out, "Deleted %d item(s)%s\n", result.Deleted, freed)
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&all, "all", false, "delete everything the filters match, not named items")
	f.StringVar(&agent, "agent", "", "with --all: only this agent's items")
	f.StringVar(&kind, "kind", "", "with --all: only this kind: screenshot, recording, report, log, note or file")
	f.BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}

// describeAllMedia names what --all covers, so the question says which filter
// it obeys: "all 42 screenshots of agent-01".
func describeAllMedia(n int, kind, agent string) string {
	what := cmp.Or(kind, "item")
	if n != 1 {
		what += "s"
	}
	if agent != "" {
		return fmt.Sprintf("all %d %s of %s", n, what, agent)
	}
	return fmt.Sprintf("all %d %s", n, what)
}

// confirmPrompt asks before something destructive happens, and takes silence,
// or anything but yes, for no.
func confirmPrompt(cmd *cobra.Command, question string) (bool, error) {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func newMediaExportCmd(a *app) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "export <project/agent>",
		Short: "Copy an agent's media into a folder with a README.md, to attach to a pull request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if dir != "" {
				if dir, err = filepath.Abs(dir); err != nil {
					return err
				}
			}
			result, err := c.ExportMedia(cmd.Context(), args[0], api.ExportRequest{Dir: dir})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Exported %d item(s) to %s\n", result.Items, result.Dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "out", "", "the folder to export into (default: ~/AgentBox/exports)")
	return cmd
}

// hostMediaItem fetches an item on the host and checks it belongs to ref.
func hostMediaItem(cmd *cobra.Command, a *app, ref, id string) (api.MediaItem, error) {
	c, err := a.client(cmd)
	if err != nil {
		return api.MediaItem{}, err
	}
	item, err := c.MediaItem(cmd.Context(), id)
	if err != nil {
		return api.MediaItem{}, err
	}
	if item.Agent != ref {
		return api.MediaItem{}, fmt.Errorf("%s belongs to %s, not %s", id, item.Agent, ref)
	}
	if item.Kind != "note" && strings.TrimSpace(item.Path) == "" {
		return api.MediaItem{}, fmt.Errorf("%s has no file", id)
	}
	return item, nil
}

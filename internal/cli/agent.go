package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/image"
	"agentbox/internal/incus"
)

func newImageCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage the base image agents are created from",
	}

	var local, android, codex, withOpenCode, devCaches, withIncus bool
	build := &cobra.Command{
		Use:   "build",
		Short: "Build the base image (replaces an existing one; existing agents are unaffected)",
		Long: `Makes the base image here, from Debian and the tools every agent gets, which takes a
few minutes. Nothing publishes a ready-made image: it holds software AgentBox may
not redistribute, so every machine builds its own.

Five components are optional and off until you ask, because most agents use
none of them: --android adds scrcpy, which mirrors an Android emulator's screen,
--codex adds the Codex CLI and the adapter the app's chat drives it with,
--opencode adds the OpenCode CLI, which is its own adapter, --dev-caches
fills the Go, npm and Electron caches from AgentBox's own repository, for a
machine whose agents work on AgentBox itself, and --incus adds Incus itself, for
a project whose agents need to run a real Incus daemon of their own (its
"nesting" setting). What you choose is remembered, so a later rebuild keeps it;
turn one off again with --android=false, --codex=false, --opencode=false,
--dev-caches=false or --incus=false.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			var req api.BuildImageRequest
			f := cmd.Flags()
			if f.Changed("android") {
				req.Android = &android
			}
			if f.Changed("codex") {
				req.Codex = &codex
			}
			if f.Changed("opencode") {
				req.OpenCode = &withOpenCode
			}
			if f.Changed("dev-caches") {
				req.DevCaches = &devCaches
			}
			if f.Changed("incus") {
				req.Incus = &withIncus
			}
			out := cmd.OutOrStdout()
			components, err := buildComponents(cmd.Context(), c, req)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Making the base image here, with %s. It downloads:\n\n%s\n%s.\n\n",
				components.Summary(), image.DownloadTable(image.DownloadsFor(components)), image.DownloadsHint)
			start := time.Now()
			j, err := c.BuildImage(cmd.Context(), req)
			if err != nil {
				return err
			}
			if _, err := waitJob(cmd, c, j); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "\nBase image %s ready in %s\n", image.SnapshotRef(), time.Since(start).Round(time.Second))
			return nil
		},
	}
	// --local chose the local build when there was a published image to
	// download instead. Every build is local now; the flag stays so scripts
	// that pass it keep working.
	build.Flags().BoolVar(&local, "local", false, "")
	_ = build.Flags().MarkDeprecated("local", "every build is local now")
	build.Flags().BoolVar(&android, "android", false, "build in scrcpy, for watching an agent's Android emulator")
	build.Flags().BoolVar(&codex, "codex", false, "build in the Codex CLI and its chat adapter")
	build.Flags().BoolVar(&withOpenCode, "opencode", false, "build in the OpenCode CLI, which is its own chat adapter")
	build.Flags().BoolVar(&devCaches, "dev-caches", false, "fill the Go, npm and Electron caches from AgentBox's own repository, for agents that work on AgentBox")
	build.Flags().BoolVar(&withIncus, "incus", false, "build in Incus, for a project whose agents run a real Incus daemon of their own")
	cmd.AddCommand(build)

	version := &cobra.Command{
		Use:   "version",
		Short: "Print the version of the base image this AgentBox builds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), image.Version)
			return nil
		},
	}
	cmd.AddCommand(version)
	return cmd
}

// buildComponents works out which optional components the build will include,
// so the command can say so before it starts: the ones the flags name, and
// otherwise the ones this installation already chose. It is the same rule the
// daemon applies, and only what's printed depends on it.
func buildComponents(ctx context.Context, c *api.Client, req api.BuildImageRequest) (image.Components, error) {
	status, err := c.Setup(ctx)
	if err != nil {
		return image.Components{}, err
	}
	components := image.Components{
		Android:   status.Image.Components.Android,
		Codex:     status.Image.Components.Codex,
		OpenCode:  status.Image.Components.OpenCode,
		DevCaches: status.Image.Components.DevCaches,
		Incus:     status.Image.Components.Incus,
	}
	if req.Android != nil {
		components.Android = *req.Android
	}
	if req.Codex != nil {
		components.Codex = *req.Codex
	}
	if req.OpenCode != nil {
		components.OpenCode = *req.OpenCode
	}
	if req.DevCaches != nil {
		components.DevCaches = *req.DevCaches
	}
	if req.Incus != nil {
		components.Incus = *req.Incus
	}
	return components, nil
}

func newCreateCmd(a *app) *cobra.Command {
	var req api.CreateAgentRequest
	// Three settings a new agent can be given, or not. A flag left off has to
	// be told apart from one set to its zero value: "" is not "the model new
	// agents start on", and --autonomous=false is a real choice, so each is
	// read from the flag only when it was actually typed.
	var (
		autonomous, queue     bool
		model, effort, window string
	)
	cmd := &cobra.Command{
		Use:   "create <project>",
		Short: "Create an agent: an isolated machine with its own worktree and branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Project = args[0]
			f := cmd.Flags()
			req.Autonomous = &autonomous
			if f.Changed("model") {
				req.Model = &model
			}
			if f.Changed("effort") {
				req.Effort = &effort
			}
			if f.Changed("context-window") {
				req.ContextWindow = &window
			}
			// Left off, the project's "always queue new agents" decides.
			if f.Changed("queue") {
				req.Queue = &queue
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			start := time.Now()
			j, err := c.CreateAgent(cmd.Context(), req)
			if err != nil {
				return err
			}
			return finishAgentJob(cmd, c, j, start)
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Name, "name", "", "agent name (default: the next free agent-NN)")
	f.StringVar(&req.Title, "title", "", `a title shown next to the agent's name, like "Medication reminders"`)
	f.StringVar(&req.Branch, "branch", "", "its branch, after the project's prefix, in lowercase kebab-case like fix-login-redirect (default: made from --title, then the agent's name; a taken branch gets -2, -3…)")
	f.StringVar(&req.AI, "ai", "claude", "AI tool to start: claude, codex, opencode or none")
	f.StringVar(&req.Interface, "interface", "", "how you use the AI tool: chat (the default: the app's Chat tab, or agentbox chat) or cli (its command line, in the terminal)")
	f.BoolVar(&autonomous, "autonomous", true, "start the AI tool without permission prompts (the agent's machine is the sandbox); --autonomous=false asks first")
	f.StringVar(&model, "model", "", "the Claude Code model this agent runs on, as Claude Code names it, like opus, sonnet or haiku (default: the project's, then the model new agents start on in Settings, then opus)")
	f.StringVar(&window, "context-window", "", "this agent's context window, where its chat compacts, like 200k or 1m, if its model has that window (default: the window new agents start with in Settings, then the installation's compact window)")
	f.StringVar(&effort, "effort", "", "how hard this agent thinks, as Claude Code names it, like xhigh or low (default: the effort new agents start on, then high)")
	f.StringVar(&req.From, "from", "", "branch or commit to start from (default: the branch checked out in the project)")
	f.StringVar(&req.ClaudeAccount, "claude-account", "", "a stored Claude Code account for this agent (default: the project's, then this machine's default)")
	f.StringVar(&req.GitHubAccount, "github-account", "", "a stored GitHub account for this agent (default: the project's, then this machine's default, then none)")
	f.BoolVar(&queue, "queue", false, "queue it: it gets its name, branch and task now, and its machine when one of the project's slots is free (agentbox queue); --queue=false makes it now even in a project that always queues (default: the project's setting)")
	f.StringVar(&req.Size, "size", "", "how much of the VM's memory it reserves in its heavy phases (tests, builds, the browser, a recording), which wait for it: auto (the default, the project's), light (~2 GB), normal (what the project's agents were seen to need) or heavy (~8 GB, like a recording). A reservation, not a cap")
	f.StringVar(&req.Task, "task", "", "its first message, sent once it's ready: what to do")
	f.BoolVar(&req.NoEnv, "no-env", false, "don't copy gitignored env files from the project")
	f.BoolVar(&req.Clean, "clean", false, "start from the base image even if the project has a saved base")
	f.BoolVar(&req.CatchUp, "catch-up", false, "bring the machine up to the current base image's packages and agent tools before its task: how a project base is refreshed")
	return cmd
}

// finishAgentJob waits for a job that makes an agent, then describes the agent.
func finishAgentJob(cmd *cobra.Command, c *api.Client, j api.Job, start time.Time) error {
	j, err := waitJob(cmd, c, j)
	if err != nil {
		return err
	}
	var ag api.Agent
	if err := json.Unmarshal(j.Result, &ag); err != nil {
		return fmt.Errorf("reading the job result: %w", err)
	}
	printAgent(cmd, ag, time.Since(start))
	return nil
}

func printAgent(cmd *cobra.Command, ag api.Agent, took time.Duration) {
	out := cmd.OutOrStdout()
	if ag.State == "queued" {
		why := ag.Waiting
		if why == "" {
			why = fmt.Sprintf("queued: it starts when one of %s's slots is free", ag.Project)
		}
		_, _ = fmt.Fprintf(out, "\nAgent %s %s (#%d in line, agentbox queue %s)\n", ag.Ref, why, ag.QueuePosition, ag.Project)
		_, _ = fmt.Fprintf(out, "  branch  %s (made when it starts)\n", ag.Branch)
		return
	}
	_, _ = fmt.Fprintf(out, "\nAgent %s ready in %s\n\n", ag.Ref, took.Round(100*time.Millisecond))
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if ag.Title != "" {
		_, _ = fmt.Fprintf(w, "  title\t%s\n", ag.Title)
	}
	_, _ = fmt.Fprintf(w, "  ai\t%s\n", describeAI(ag.AI, ag.Autonomous))
	if ag.AI != "none" {
		_, _ = fmt.Fprintf(w, "  interface\t%s\n", ag.Interface)
	}
	if ag.ClaudeAccount != "" {
		_, _ = fmt.Fprintf(w, "  account\t%s (Claude Code)\n", ag.ClaudeAccount)
	}
	if ag.GitHubAccount != "" {
		_, _ = fmt.Fprintf(w, "  account\t%s (GitHub)\n", ag.GitHubAccount)
	}
	_, _ = fmt.Fprintf(w, "  machine\tcopy of %s\n", ag.Source)
	_, _ = fmt.Fprintf(w, "  branch\t%s (from %s)\n", ag.Branch, ag.BaseRef)
	_, _ = fmt.Fprintf(w, "  worktree\t%s\n", ag.Worktree)
	_, _ = fmt.Fprintf(w, "  ip\t%s\n", ag.IP)
	_ = w.Flush()
	if ag.Interface == "chat" {
		_, _ = fmt.Fprintf(out, "\nChat with it in the app, or: agentbox chat %s \"<message>\"", ag.Ref)
	}
	_, _ = fmt.Fprintf(out, "\nAttach with: agentbox shell %s\n", ag.Ref)
}

func newListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list [project]",
		Aliases: []string{"ls"},
		Short:   "List agents",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := ""
			if len(args) == 1 {
				project = args[0]
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			agents, err := c.Agents(cmd.Context(), project)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(agents) == 0 {
				_, _ = fmt.Fprintln(out, "No agents. Create one with: agentbox create <project>")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			// TITLE comes last: it can contain spaces, and scripts read the columns before it.
			_, _ = fmt.Fprintln(w, "AGENT\tAI\tSTATE\tBRANCH\tIP\tCREATED\tTITLE")
			for _, ag := range agents {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					ag.Ref, describeAI(ag.AI, ag.Autonomous), ag.State, ag.Branch, orDash(ag.IP), ago(ag.CreatedAt), orDash(ag.Title))
			}
			return w.Flush()
		},
	}
}

func newTitleCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "title <project/agent> <title>",
		Short: `Set the title shown next to an agent's name ("" clears it)`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ag, err := c.UpdateAgent(cmd.Context(), args[0], api.UpdateAgentRequest{Title: &args[1]})
			if err != nil {
				return err
			}
			if ag.Title == "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cleared the title of %s\n", ag.Ref)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is titled %q\n", ag.Ref, ag.Title)
			}
			return nil
		},
	}
}

func newShellCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "shell <project/agent>",
		Short: "Attach to the agent's terminal (tmux: window 0 is a shell, window 1 the AI tool's command line unless it uses the chat; detach with Ctrl-b d)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ag, err := c.AgentAction(cmd.Context(), args[0], "session")
			if err != nil {
				return err
			}
			u, err := hostUser()
			if err != nil {
				return err
			}
			argv := agent.ShellCommand(incus.Client{}.Path(), ag.Instance, u.Name, ag.Worktree)
			bin, err := exec.LookPath(argv[0])
			if err != nil {
				return err
			}
			return syscall.Exec(bin, argv, os.Environ())
		},
	}
}

func newExecCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <project/agent> -- <command>...",
		Short: "Run a command in the agent's worktree (login shell, as your user)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			command, err := commandLine(args[1:])
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ag, err := c.Agent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if ag.State != "running" {
				return notRunning(ag)
			}
			u, err := hostUser()
			if err != nil {
				return err
			}
			// The incus command, not the API: with a terminal on stdin it
			// gives the command one too, as ssh does.
			run := incus.Client{}.UserCommand(cmd.Context(), ag.Instance, u.Name, agent.ExecCommand(ag.Worktree, command))
			run.Stdin, run.Stdout, run.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			err = run.Run()
			if code, ok := incus.ExitCode(err); ok {
				return exitCodeError(code)
			}
			return err
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// commandLine joins the words after the agent into one shell command, as ssh
// does. Flag parsing stops at the agent, so a "--" separator arrives as a word.
func commandLine(words []string) (string, error) {
	if len(words) > 0 && words[0] == "--" {
		words = words[1:]
	}
	if len(words) == 0 {
		return "", errors.New("missing command: agentbox exec <project/agent> -- <command>")
	}
	return strings.Join(words, " "), nil
}

func notRunning(ag api.Agent) error {
	if ag.State == "paused" {
		return fmt.Errorf("%s is paused: run agentbox resume %s", ag.Ref, ag.Ref)
	}
	return fmt.Errorf("%s is %s: run agentbox start %s", ag.Ref, ag.State, ag.Ref)
}

// newActionCmd builds start, stop, pause and resume.
func newActionCmd(a *app, action, short, done string) *cobra.Command {
	var all, now bool
	cmd := &cobra.Command{
		Use:   action + " <project/agent>",
		Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if all {
				return stopAll(cmd, c)
			}
			if now {
				if ag, err := c.Agent(cmd.Context(), args[0]); err == nil && ag.State == "queued" {
					if err := c.StartQueued(cmd.Context(), args[0]); err != nil {
						return err
					}
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Starting "+ag.Ref+" now; it's left the queue")
					return nil
				}
			}
			ag, err := c.AgentAction(cmd.Context(), args[0], action)
			if err != nil {
				return err
			}
			msg := done + " " + ag.Ref
			if action == "start" {
				msg += " (ip " + ag.IP + ")"
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	if action == "stop" {
		cmd.Use = "stop <project/agent> | --all"
		cmd.Flags().BoolVar(&all, "all", false, "stop every running or paused agent of every project")
	}
	if action == "start" {
		cmd.Flags().BoolVar(&now, "now", false, "start a queued agent now, even if the VM's memory or its project's slots say it should wait")
	}
	return cmd
}

// stopAll is `agentbox stop --all`: the app's "Free resources", followed as
// a job, then what it freed.
func stopAll(cmd *cobra.Command, c *api.Client) error {
	j, err := c.StopAgents(cmd.Context(), api.StopAgentsRequest{})
	if err != nil {
		return err
	}
	final, err := waitJob(cmd, c, j)
	if err != nil {
		return err
	}
	var result api.StopAgentsResult
	if err := json.Unmarshal(final.Result, &result); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(result.Stopped) == 0 && len(result.Failed) == 0 {
		_, _ = fmt.Fprintln(out, "No agent was running.")
		return nil
	}
	_, _ = fmt.Fprintf(out, "Stopped %d, freeing %s of memory and %.1f cores\n", len(result.Stopped), humanBytes(result.FreedMemory), result.FreedCPU/100)
	for _, f := range result.Failed {
		_, _ = fmt.Fprintf(out, "Couldn't stop %s: %s\n", f.Ref, f.Error)
	}
	if len(result.Failed) > 0 {
		return exitCodeError(1)
	}
	return nil
}

func newDestroyCmd(a *app) *cobra.Command {
	var force, deleteBranch, deleteMedia bool
	cmd := &cobra.Command{
		Use: "destroy <project/agent>",
		Short: "Delete an agent's machine, snapshots and worktree (its branch stays if it has commits " +
			"that aren't merged or pushed, and its media for the media retention, unless --delete-branch or --delete-media)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ag, err := c.Agent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.DestroyAgent(cmd.Context(), args[0], force, deleteBranch, deleteMedia); err != nil {
				return err
			}
			msg := "Destroyed " + ag.Ref
			var kept []string
			if !deleteBranch {
				kept = append(kept, "branch "+ag.Branch+" unless it was merged or pushed")
			}
			if !deleteMedia {
				kept = append(kept, "its media")
			}
			if len(kept) > 0 {
				msg += fmt.Sprintf(" (%s kept)", strings.Join(kept, ", "))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "discard uncommitted changes")
	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false, "also delete the agent's branch, even with commits that aren't merged or pushed")
	cmd.Flags().BoolVar(&deleteMedia, "delete-media", false, "also delete its media, instead of keeping it in the project's media view")
	return cmd
}

func newDiffCmd(a *app) *cobra.Command {
	var stat bool
	cmd := &cobra.Command{
		Use:   "diff <project/agent>",
		Short: "Show what the agent changed since it was created (commits, uncommitted and untracked files)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			diff, err := c.Diff(cmd.Context(), args[0], stat)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), diff)
			return nil
		},
	}
	cmd.Flags().BoolVar(&stat, "stat", false, "show a summary instead of the full diff")
	return cmd
}

func newPathCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "path <project/agent>",
		Short: "Print the agent's worktree path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ag, err := c.Agent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), ag.Worktree)
			return nil
		},
	}
}

func describeAI(ai string, autonomous bool) string {
	if autonomous {
		return ai + " (autonomous)"
	}
	return ai
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func ago(t time.Time) string {
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

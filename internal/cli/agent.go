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

	var local, android, codex, withOpenCode bool
	build := &cobra.Command{
		Use:   "build",
		Short: "Build the base image (replaces an existing one; existing agents are unaffected)",
		Long: `Makes the base image here, from Debian and the tools every agent gets, which takes a
few minutes. Nothing publishes a ready-made image: it holds software AgentBox may
not redistribute, so every machine builds its own.

Three components are optional and off until you ask, because most agents use
none of them: --android adds scrcpy, which mirrors an Android emulator's screen,
--codex adds the Codex CLI and the adapter the app's chat drives it with, and
--opencode adds the OpenCode CLI, which is its own adapter. What you choose is remembered, so a later rebuild keeps it; turn one off again with
--android=false, --codex=false or --opencode=false.`,
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
			out := cmd.OutOrStdout()
			components, err := buildComponents(cmd.Context(), c, req)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Making the base image here, with %s. It downloads:\n\n%s\n%s.\n\n",
				components.Summary(), image.DownloadTable(image.DownloadsFor(components)), image.DownloadsHint)
			start := time.Now()
			j, err := c.BuildImage(cmd.Context(), req)
			if err != nil {
				return err
			}
			if _, err := waitJob(cmd, c, j); err != nil {
				return err
			}
			fmt.Fprintf(out, "\nBase image %s ready in %s\n", image.SnapshotRef(), time.Since(start).Round(time.Second))
			return nil
		},
	}
	// --local chose the local build when there was a published image to
	// download instead. Every build is local now; the flag stays so scripts
	// that pass it keep working.
	build.Flags().BoolVar(&local, "local", false, "")
	build.Flags().MarkDeprecated("local", "every build is local now")
	build.Flags().BoolVar(&android, "android", false, "build in scrcpy, for watching an agent's Android emulator")
	build.Flags().BoolVar(&codex, "codex", false, "build in the Codex CLI and its chat adapter")
	build.Flags().BoolVar(&withOpenCode, "opencode", false, "build in the OpenCode CLI, which is its own chat adapter")
	cmd.AddCommand(build)

	version := &cobra.Command{
		Use:   "version",
		Short: "Print the version of the base image this AgentBox builds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), image.Version)
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
		Android:  status.Image.Components.Android,
		Codex:    status.Image.Components.Codex,
		OpenCode: status.Image.Components.OpenCode,
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
	return components, nil
}

func newCreateCmd(a *app) *cobra.Command {
	var req api.CreateAgentRequest
	// Three settings a new agent can be given, or not. A flag left off has to
	// be told apart from one set to its zero value: "" is not "the model new
	// agents start on", and --autonomous=false is a real choice, so each is
	// read from the flag only when it was actually typed.
	var (
		autonomous                bool
		model, effort, window     string
		cpu, memory, cpuAllowance string
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
			// The limits are read the same way, and for a sharper reason:
			// --cpu "" is a real choice (this agent gets every core), so an
			// untyped flag can't be sent as "" or every agent would come out
			// uncapped.
			if f.Changed("cpu") {
				req.CPU = &cpu
			}
			if f.Changed("memory") {
				req.Memory = &memory
			}
			if f.Changed("cpu-allowance") {
				req.CPUAllowance = &cpuAllowance
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
	f.StringVar(&req.AI, "ai", "claude", "AI tool to start: claude, codex, opencode or none")
	f.StringVar(&req.Interface, "interface", "", "how you use the AI tool: chat (the default: the app's Chat tab, or agentbox chat) or cli (its command line, in the terminal)")
	f.BoolVar(&autonomous, "autonomous", true, "start the AI tool without permission prompts (the agent's machine is the sandbox); --autonomous=false asks first")
	f.StringVar(&model, "model", "", "the Claude Code model this agent runs on, as Claude Code names it, like opus[1m] or haiku (default: the model new agents start on, then Opus 5 at 1M context)")
	f.StringVar(&window, "context-window", "", "where this agent's chat compacts, like 200k or 1m, if its model has that window (default: the installation's compact window)")
	f.StringVar(&effort, "effort", "", "how hard this agent thinks, as Claude Code names it, like xhigh or low (default: the effort new agents start on, then high)")
	f.StringVar(&req.From, "from", "", "branch or commit to start from (default: the branch checked out in the project)")
	f.StringVar(&req.ClaudeAccount, "claude-account", "", "a stored Claude Code account for this agent (default: the project's, then this machine's default)")
	f.StringVar(&req.GitHubAccount, "github-account", "", "a stored GitHub account for this agent (default: the project's, then this machine's default, then none)")
	f.BoolVar(&req.NoEnv, "no-env", false, "don't copy gitignored env files from the project")
	f.BoolVar(&req.Clean, "clean", false, "start from the base image even if the project has a saved base")
	f.StringVar(&cpu, "cpu", "", `how many cores this agent gets, like 4; "" gives it every core (default: what new agents get)`)
	f.StringVar(&memory, "memory", "", `how much memory this agent gets, like 8GiB; "" gives it all of it (default: what new agents get)`)
	f.StringVar(&cpuAllowance, "cpu-allowance", "", `this agent's share of the CPUs: a percentage like 50%, which only counts when the host is busy, or a hard ceiling like 25ms/100ms (default: what new agents get)`)
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
	fmt.Fprintf(out, "\nAgent %s ready in %s\n\n", ag.Ref, took.Round(100*time.Millisecond))
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if ag.Title != "" {
		fmt.Fprintf(w, "  title\t%s\n", ag.Title)
	}
	fmt.Fprintf(w, "  ai\t%s\n", describeAI(ag.AI, ag.Autonomous))
	if ag.AI != "none" {
		fmt.Fprintf(w, "  interface\t%s\n", ag.Interface)
	}
	if ag.ClaudeAccount != "" {
		fmt.Fprintf(w, "  account\t%s (Claude Code)\n", ag.ClaudeAccount)
	}
	if ag.GitHubAccount != "" {
		fmt.Fprintf(w, "  account\t%s (GitHub)\n", ag.GitHubAccount)
	}
	fmt.Fprintf(w, "  machine\tcopy of %s\n", ag.Source)
	fmt.Fprintf(w, "  limits\t%s\n", limitWords(ag.Limits))
	fmt.Fprintf(w, "  branch\t%s (from %s)\n", ag.Branch, ag.BaseRef)
	fmt.Fprintf(w, "  worktree\t%s\n", ag.Worktree)
	fmt.Fprintf(w, "  ip\t%s\n", ag.IP)
	w.Flush()
	if ag.Interface == "chat" {
		fmt.Fprintf(out, "\nChat with it in the app, or: agentbox chat %s \"<message>\"", ag.Ref)
	}
	fmt.Fprintf(out, "\nAttach with: agentbox shell %s\n", ag.Ref)
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
				fmt.Fprintln(out, "No agents. Create one with: agentbox create <project>")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			// TITLE comes last: it can contain spaces, and scripts read the columns before it.
			fmt.Fprintln(w, "AGENT\tAI\tSTATE\tBRANCH\tIP\tCREATED\tTITLE")
			for _, ag := range agents {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
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
				fmt.Fprintf(cmd.OutOrStdout(), "Cleared the title of %s\n", ag.Ref)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is titled %q\n", ag.Ref, ag.Title)
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
			err = incus.Client{}.UserExec(cmd.Context(), ag.Instance, u.Name, agent.ExecCommand(ag.Worktree, command),
				cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return exitCodeError(exitErr.ExitCode())
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
	return &cobra.Command{
		Use:   action + " <project/agent>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			ag, err := c.AgentAction(cmd.Context(), args[0], action)
			if err != nil {
				return err
			}
			msg := done + " " + ag.Ref
			if action == "start" {
				msg += " (ip " + ag.IP + ")"
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
}

func newDestroyCmd(a *app) *cobra.Command {
	var force, deleteBranch, deleteMedia bool
	cmd := &cobra.Command{
		Use: "destroy <project/agent>",
		Short: "Delete an agent's machine, snapshots and worktree (its branch and media stay " +
			"unless --delete-branch or --delete-media)",
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
				kept = append(kept, "branch "+ag.Branch)
			}
			if !deleteMedia {
				kept = append(kept, "its media")
			}
			if len(kept) > 0 {
				msg += fmt.Sprintf(" (%s kept)", strings.Join(kept, ", "))
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "discard uncommitted changes")
	cmd.Flags().BoolVar(&deleteBranch, "delete-branch", false, "also delete the agent's branch")
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
			fmt.Fprint(cmd.OutOrStdout(), diff)
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
			fmt.Fprintln(cmd.OutOrStdout(), ag.Worktree)
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

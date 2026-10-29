package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newAddCmd(a *app) *cobra.Command {
	var name, claudeAccount, githubAccount, codexAccount, cursorAccount string
	var openCodeAccounts map[string]string
	var copyToLinux, create, commitFiles bool
	var cloneURL string
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Register a git repository as a project",
		Long: `Register a git repository as a project.

With --new, <path> is a repository to start: AgentBox makes the folder, runs
git init on main and makes an initial commit, so agents have a commit to branch
from, then registers it. A folder that is already a repository with commits is
added as it is; one with files in it and no commits is only committed with
--commit-files.

With --clone, AgentBox clones the repository at that link (https:// or
git@host:owner/repo, on GitHub or any other host) into <path>, or into the
clone folder (~/src unless Settings changed it) when there is no <path>, and
registers it. A github.com repository is cloned with the project's GitHub
account (--github-account, or this machine's default), so a private one needs
no other login; anything else, or one that account can't see, with this
machine's own git credentials.`,
		Example: `  agentbox add ~/code/my-app
  agentbox add --new ~/code/my-new-app
  agentbox add --clone https://github.com/acme/my-app
  agentbox add --clone git@gitlab.com:acme/my-app.git ~/code/my-app`,
		Args: func(cmd *cobra.Command, args []string) error {
			if cloneURL != "" {
				return cobra.MaximumNArgs(1)(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) > 0 {
				var err error
				if path, err = filepath.Abs(args[0]); err != nil {
					return err
				}
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			req := api.AddProjectRequest{
				Path: path, Name: name, ClaudeAccount: claudeAccount, GitHubAccount: githubAccount, CopyToLinux: copyToLinux,
				CodexAccount: codexAccount, OpenCodeAccounts: openCodeAccounts, CursorAccount: cursorAccount,
				Create: create, CommitFiles: commitFiles,
			}
			var p api.Project
			if cloneURL != "" {
				p, err = cloneProject(cmd, c, api.CloneProjectRequest{URL: cloneURL, AddProjectRequest: req})
			} else {
				p, err = c.AddProject(cmd.Context(), req)
			}
			if api.HasReason(err, api.ErrorFolderNotEmpty) {
				return fmt.Errorf("%w: add --commit-files to make them its initial commit", err)
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Added project %s\n\n", p.Called())
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			if p.Name != p.Called() {
				_, _ = fmt.Fprintf(w, "  id\t%s (in refs like %s/agent-01; either name works in commands)\n", p.Name, p.Name)
			}
			_, _ = fmt.Fprintf(w, "  root\t%s\n", p.Root)
			_, _ = fmt.Fprintf(w, "  branch\t%s (new agents start here)\n", p.Branch)
			_, _ = fmt.Fprintf(w, "  env files\t%s\n", describeEnvFiles(p.EnvFiles))
			_, _ = fmt.Fprintf(w, "  claude account\t%s\n", cmpOrDefault("claude", p.ClaudeAccount))
			_, _ = fmt.Fprintf(w, "  github account\t%s\n", cmpOrDefault("github", p.GitHubAccount))
			if p.Android {
				_, _ = fmt.Fprintf(w, "  android\tdetected: its agents can run their own emulator (agentbox android start)\n")
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&name, "name", "", `what the project is called, anything you like, such as "Organic Web App" (default: the directory's name)`)
	cmd.Flags().StringVar(&claudeAccount, "claude-account", "", "Claude Code account its agents use (default: this machine's default account)")
	cmd.Flags().StringVar(&githubAccount, "github-account", "", "GitHub account its agents use (default: this machine's default account)")
	cmd.Flags().StringVar(&codexAccount, "codex-account", "", "Codex account its agents and chat use (default: this machine's default account)")
	cmd.Flags().StringVar(&cursorAccount, "cursor-account", "", "Cursor account its agents and chat use (default: this machine's default account)")
	cmd.Flags().StringToStringVar(&openCodeAccounts, "opencode-account", nil, "OpenCode account its agents use for a provider, as provider=account; repeat it for several (default: each provider's default)")
	cmd.Flags().BoolVar(&create, "new", false, "make <path> a new repository, on main with an initial commit, and add that")
	cmd.Flags().BoolVar(&commitFiles, "commit-files", false, "with --new, commit the files already in <path> as the initial commit")
	cmd.Flags().StringVar(&cloneURL, "clone", "", "clone the repository at this link into <path> (default: the clone folder, ~/src) and add that")
	cmd.Flags().BoolVar(&copyToLinux, "copy", false, "in WSL, add a clone in ~/src/<name> of a repository on a Windows drive, rather than refusing it")
	return cmd
}

// cloneProject runs the job that clones a repository and adds it, its
// progress on stderr, and answers the project it added.
func cloneProject(cmd *cobra.Command, c *api.Client, req api.CloneProjectRequest) (api.Project, error) {
	j, err := c.CloneProject(cmd.Context(), req)
	if api.HasReason(err, api.ErrorFolderExists) {
		return api.Project{}, fmt.Errorf("%w (agentbox add <folder> adds it as it is)", err)
	}
	if err != nil {
		return api.Project{}, err
	}
	if j, err = waitJob(cmd, c, j); err != nil {
		return api.Project{}, err
	}
	var p api.Project
	if err := json.Unmarshal(j.Result, &p); err != nil {
		return api.Project{}, fmt.Errorf("reading the cloned project: %w", err)
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr())
	return p, nil
}

// cmpOrDefault describes a project's Claude Code or GitHub account, which is
// empty until the project picks one.
func cmpOrDefault(tool, account string) string {
	if account == "" {
		return fmt.Sprintf("this machine's default (agentbox %s-account <project> <account> to change)", tool)
	}
	return account
}

func describeEnvFiles(files []string) string {
	if len(files) == 0 {
		return "none"
	}
	return strings.Join(files, ", ") + " (copied into new agents)"
}

// newProjectCmd groups the settings that belong to one project, as opposed to
// the installation-wide ones the app's overview holds.
func newProjectCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Settings of one project",
		Long:  `Settings that belong to one project, rather than to this installation.`,
	}
	cmd.AddCommand(newAddCmd(a), newProjectModelCmd(a), newProjectToolCmd(a), newProjectBranchPrefixCmd(a), newProjectRenameCmd(a), newProjectGitCmd(a), newProjectClusterCmd(a))
	return cmd
}

// newProjectClusterCmd shows or sets where a project's agents run when a
// request names no cluster.
func newProjectClusterCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "cluster <project> [<cluster>|auto|local]",
		Short: "Where this project's new agents run: a cluster, auto, or this machine",
		Long: `Shows or sets where this project's new agents run when an agent doesn't name
a cluster of its own.

  local       this machine, an Incus instance for each agent (this is how a
              project starts)
  <cluster>   that Kubernetes cluster, by name
  auto        the daemon picks the cluster with the most room for each agent,
              which is this project's own default cluster when that is one of
              them

Agents that already exist stay where they are: this is where the next one
runs.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), projectClusterWords(p.Cluster))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			// "local" is how you ask for this machine on a command line, where
			// an empty argument can't be typed.
			cluster := strings.TrimSpace(args[1])
			if cluster == "local" {
				cluster = ""
			}
			p, err := c.SetProjectCluster(cmd.Context(), args[0], cluster)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), projectClusterWords(p.Cluster))
			return nil
		},
	}
}

func projectClusterWords(cluster string) string {
	switch cluster {
	case "":
		return "local — an Incus instance on this machine for each new agent"
	case "auto":
		return "auto — whichever cluster has the most room for each new agent"
	default:
		return cluster + " — every new agent of this project runs there"
	}
}

// newProjectRenameCmd changes what a project is called. Its id, the slug in
// its refs, containers, folders and branches, stays as it is.
func newProjectRenameCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <project> <name>",
		Short: "Change what a project is called",
		Long: `Change what a project is called: any name, if no other project has it.

Only the name changes. The project's id, which its agents' refs, machines,
folders and branches are named with, stays as it is.`,
		Example: `  agentbox project rename organic-web-app "Organic Web App"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			p, err := c.RenameProject(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Renamed %s to %s\n", p.Name, p.Called())
			return nil
		},
	}
}

// newProjectModelCmd shows or sets the model this project's agents are created
// on, in newAutonomyCmd's show-or-set shape.
func newProjectModelCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "model <project> [<model>|auto|default]",
		Short: "The model this project's agents are created on",
		Long: `Shows or sets the model this project's new agents are created on.

  default   follow the model new agents start on everywhere, chosen on the
            app's overview (this is how a project starts)
  <model>   every agent of this project is created on it — a name Claude Code
            itself uses, like sonnet, unless one is named for a single agent
  auto      the project's chat chooses a model for each agent it creates, from
            how hard the task is, and says which it chose

Agents that already exist keep the model they have: this is the starting point
for the next one.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), agentModelWords(p.AgentModel))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			// "default" is how you ask for the installation's setting on a
			// command line, where an empty argument can't be typed. It is
			// also the model menu's own word for "no model of my own", which
			// is never stored as a model id.
			model := strings.TrimSpace(args[1])
			if model == "default" {
				model = ""
			}
			p, err := c.SetAgentModel(cmd.Context(), args[0], model)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), agentModelWords(p.AgentModel))
			return nil
		},
	}
}

// newProjectToolCmd shows or sets the AI tool this project's agents run,
// in newProjectModelCmd's show-or-set shape.
func newProjectToolCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "tool <project> [inherit|claude|codex|opencode|cursor]",
		Short: "The AI tool this project's agents run",
		Long: `Shows or sets the AI tool this project's new agents run when the agent doesn't
name one, or names no account of its own.

  inherit   follow the tool this project's chat runs (this is how a
            project starts)
  claude    Claude Code, on the project's Claude Code account
  codex     Codex, on the project's Codex account
  opencode  OpenCode, on the project's OpenCode accounts
  cursor    Cursor, on the project's Cursor account

An account or a tool named for one agent still wins over this. Agents that
already exist keep the tool they have: this is the starting point for the next
one.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), agentToolWords(p.AgentTool))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			tool := strings.TrimSpace(args[1])
			if tool == "inherit" {
				tool = ""
			}
			p, err := c.SetAgentTool(cmd.Context(), args[0], tool)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), agentToolWords(p.AgentTool))
			return nil
		},
	}
}

func agentToolWords(tool string) string {
	switch tool {
	case "":
		return "inherit — the tool this project's chat runs"
	case "claude":
		return "Claude Code, on this project's account"
	case "codex":
		return "Codex, on this project's account"
	case "opencode":
		return "OpenCode, on this project's accounts"
	case "cursor":
		return "Cursor, on this project's account"
	}
	return tool
}

func agentModelWords(model string) string {
	switch model {
	case "":
		return "default — the model new agents start on, chosen on the app's overview"
	case "auto":
		return "auto — its chat chooses a model for each agent it creates"
	default:
		return model + " — every new agent of this project starts on it"
	}
}

// newProjectBranchPrefixCmd shows or sets what this project's agents' branches
// are named with, before the agent's name.
func newProjectBranchPrefixCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "branch-prefix <project> [<prefix>]",
		Short: "What this project's agents' branches start with",
		Long: `Shows or sets what this project's new agents' branches start with, before the
slug named after its work. A project starts on agentbox/, which puts an agent
titled "Fix login" on the branch agentbox/fix-login.

In a repository you share with others, a prefix of your own keeps your agents'
branches out of theirs:

  agentbox project branch-prefix myapp thiago/agentbox/

An empty prefix ('') names each branch after its agent alone. The prefix has to
make a valid branch name, by git check-ref-format's rules. Agents that already
exist keep the branch they were made on.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), branchPrefixWords(p.BranchPrefix))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			p, err := c.SetBranchPrefix(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), branchPrefixWords(p.BranchPrefix))
			return nil
		},
	}
}

func branchPrefixWords(prefix string) string {
	if prefix == "" {
		return "no prefix — a new agent's branch is its name, like agent-01"
	}
	return prefix + " — a new agent's branch is like " + prefix + "agent-01"
}

func newProjectsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "List projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			projects, err := c.Projects(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(projects) == 0 {
				_, _ = fmt.Fprintln(out, "No projects yet. Add one with: agentbox add <path>")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "PROJECT\tID\tCLAUDE ACCOUNT\tGITHUB ACCOUNT\tROOT")
			for _, p := range projects {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.Called(), p.Name, orDash(p.ClaudeAccount), orDash(p.GitHubAccount), p.Root)
			}
			return w.Flush()
		},
	}
}

func newRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <project>",
		Short: "Unregister a project (the repository itself is not touched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if err := c.RemoveProject(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed project %s\n", args[0])
			return nil
		},
	}
}

// newFinishNoticesCmd sets what happens when one of a project's agents
// finishes, in newAutonomyCmd's show-or-set shape.
func newFinishNoticesCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "finish-notices <project> [chat|off|lead]",
		Short: "What happens when one of a project's agents finishes",
		Long: `Shows or sets what happens when one of a project's agents finishes.

  lead  the agent that finished chose, with create_agent's "notify"; one that
        chose nothing is treated as chat (the default for a new project)
  chat  the project's chat is told, and takes a turn to decide what happens
        next, whatever the agent chose
  off   the finish is only recorded in the chat's history: it reads it the next
        time you write, and nothing is spent until then, whatever the agent chose

Questions are not affected either way: an agent that runs "agentbox ask" is
blocked until it gets an answer, so its question always wakes the chat.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), finishNoticeWords(p.FinishNotices))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			p, err := c.SetFinishNotices(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), finishNoticeWords(p.FinishNotices))
			return nil
		},
	}
}

// newNestingCmd shows or sets whether a project's agents run their own Incus
// daemon, in newFinishNoticesCmd's show-or-set shape.
func newNestingCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "nesting <project> [on|off]",
		Short: "Whether a project's agents run a real Incus daemon of their own",
		Long: `Shows or sets whether a project's agents run their own Incus, inside their own
container, so agents working on AgentBox can test features that touch agent
machines (image builds, devices, networking) for real. Off by default: it costs
isolation, and needs the base image built with agentbox image build --incus.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), nestingWords(p.Nesting))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			on, err := parseOnOff(args[1])
			if err != nil {
				return err
			}
			p, err := c.SetNesting(cmd.Context(), args[0], on)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), nestingWords(p.Nesting))
			return nil
		},
	}
}

func nestingWords(on bool) string {
	if on {
		return "on — its agents run their own Incus"
	}
	return "off"
}

// newAgentPRsCmd shows or sets whether a project's agents push their own
// branch and open their own pull request, in newNestingCmd's shape.
func newAgentPRsCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "agent-prs <project> [on|off]",
		Short: "Whether a project's agents push their branch and open a PR themselves",
		Long: `Shows or sets whether a project's agents push their own branch and open their
own pull request when they finish, rather than leaving both to you or the
project's chat, which then only retires an agent once its PR is open. They
still never push to the base branch, merge, close, or force-push someone else's
branch. Off by default: a push publishes, with your GitHub token.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), agentPRsWords(p.AgentPRs))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			on, err := parseOnOff(args[1])
			if err != nil {
				return err
			}
			p, err := c.SetAgentPRs(cmd.Context(), args[0], on)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), agentPRsWords(p.AgentPRs))
			return nil
		},
	}
}

func agentPRsWords(on bool) string {
	if on {
		return "on — its agents push their branch and open a pull request"
	}
	return "off — its agents don't push"
}

func parseOnOff(s string) (bool, error) {
	switch s {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("%q: use on or off", s)
	}
}

func finishNoticeWords(notices string) string {
	switch notices {
	case "off":
		return "off — only recorded, no chat turn and no tokens"
	case "lead":
		return "lead — the agent that finished chose, with create_agent's \"notify\""
	default:
		return "chat — its chat is told, and decides what happens next"
	}
}

// newRolloverCmd shows or sets how full a project chat's context gets before
// the conversation is compacted, in newFinishNoticesCmd's show-or-set shape.
// With "now" it compacts the chat there and then, whatever the threshold is.
func newRolloverCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rollover <project> [percent|off|now]",
		Short: "How full a project chat's context gets before it is compacted",
		Long: `Shows or sets when a project's chat is compacted.

A project's chat is one conversation that never ends, and a model's context is
finite. Past this percentage of it, AgentBox consolidates the conversation into
the project's memory, starts a fresh session and gives it a recap of where the
chat had got to. The chat you see carries on, with a notice where the session
changed.

  <percent>  compact at this much of the context window, between 10 and 95
  off        never compact; the AI tool's own compaction, if it has one, is
             then the only thing standing between the chat and a full context
  now        compact the chat immediately, whatever the threshold says

The default is 80.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			show := func(p api.Project) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), rolloverWords(p.RolloverThreshold))
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						show(p)
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			if args[1] == "now" {
				if err := c.RolloverChat(cmd.Context(), args[0]); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: the chat was consolidated into the project's memory and carried on in a fresh session\n", args[0])
				return nil
			}
			percent := 0
			if args[1] != "off" {
				if percent, err = strconv.Atoi(args[1]); err != nil {
					return fmt.Errorf("invalid threshold %q: use a percentage, off or now", args[1])
				}
			}
			p, err := c.SetRolloverThreshold(cmd.Context(), args[0], percent)
			if err != nil {
				return err
			}
			show(p)
			return nil
		},
	}
}

// newContextBudgetCmd shows or sets how many tokens one context built from a
// project's memory may cost, in newFinishNoticesCmd's show-or-set shape.
func newContextBudgetCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "context-budget <project> [tokens]",
		Short: "How much of a model's context this project's memory may fill",
		Long: `Shows or sets how much AgentBox spends telling a model what this project knows.

Every brief AgentBox writes carries a slice of the project's memory: the chat
gets what it is doing, the story so far, what is open and what memory says
about it, and every agent gets a smaller slice picked out for its own task.
Both are built to this budget, in tokens, and what doesn't fit is given up —
artifacts first, then reports, then past events, then knowledge. What the
project is doing and where it stands is never given up.

The count is an estimate, not a tokenizer: four bytes to a token. It is close
for English prose and low for code, paths and JSON.

The default is 4,000 tokens, and a worker agent gets a quarter of it.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			show := func(p api.Project) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %d tokens a context, %d for one agent\n",
					p.Called(), p.ContextBudget, p.ContextBudget/4)
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						show(p)
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			tokens, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid budget %q: use a number of tokens", args[1])
			}
			p, err := c.SetContextBudget(cmd.Context(), args[0], tokens)
			if err != nil {
				return err
			}
			show(p)
			return nil
		},
	}
}

func rolloverWords(percent int) string {
	if percent <= 0 {
		return "off — the conversation is never compacted by AgentBox"
	}
	return fmt.Sprintf("%d%% — compacted into the project's memory at %d%% of the context window", percent, percent)
}

// newConsolidationCmd shows or sets how many new events a project gathers
// before its chat is asked to turn them into memories, in newRolloverCmd's
// show-or-set shape. With "now" it runs a consolidation there and then.
func newConsolidationCmd(a *app) *cobra.Command {
	var distil bool
	cmd := &cobra.Command{
		Use:   "consolidation <project> [events|off|now]",
		Short: "How much history a project gathers before it is distilled into memories",
		Long: `Shows or sets when a project's raw history is turned into memories.

AgentBox records what happens on a project — agents created and finished,
questions asked, pull requests merged — and that history only grows. A
consolidation is what turns a stretch of it into the handful of things worth
remembering: new memories, memories that replace ones that went stale, and
issues that are simply over.

  <events>  ask the project's chat to distil, once this many new events have
            been recorded since the last pass; between 20 and 2000
  off       never consolidate: no distillation, and no mechanical pass either,
            so the project's memories are only ever added to
  now       consolidate immediately, whatever the setting says

"now" runs the mechanical pass — near-duplicates, importance decay — which
calls no model and costs nothing. Add --distil to also ask the project's chat
to read the events since the last pass, which spends the project's tokens.

The default is 200.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			show := func(p api.Project) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), consolidationWords(p.Consolidation))
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						show(p)
						state, err := c.ProjectMemory(p.Name).Consolidation(cmd.Context())
						if err == nil {
							_, _ = fmt.Fprint(cmd.OutOrStdout(), describeConsolidation(state))
						}
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			if args[1] == "now" {
				passes, err := c.ProjectMemory(args[0]).Consolidate(cmd.Context(), distil)
				if err != nil {
					return err
				}
				for _, p := range passes {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", args[0], passWords(p))
				}
				return nil
			}
			every := 0
			if args[1] != "off" {
				if every, err = strconv.Atoi(args[1]); err != nil {
					return fmt.Errorf("invalid consolidation %q: use a number of events, off or now", args[1])
				}
			}
			p, err := c.SetConsolidation(cmd.Context(), args[0], every)
			if err != nil {
				return err
			}
			show(p)
			return nil
		},
	}
	cmd.Flags().BoolVar(&distil, "distil", false, "with \"now\", also ask the project's chat to read the events since the last pass")
	return cmd
}

// newConsolidationModelCmd shows or sets which model distils a project's
// events into memories, in newConsolidationCmd's show-or-set shape.
func newConsolidationModelCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "consolidation-model <project> [cheap|<model>|chat]",
		Short: "Which model turns this project's history into memories",
		Long: `Shows or sets the model a consolidation reads with.

Distilling a stretch of history into a handful of memories is summarising, not
engineering, and it does not need the model you chat on — which is usually the
most capable and most expensive one your account has. It runs in a session of
its own, through the same AI tool and the same login as the project's chat, so
the conversation you are having is neither interrupted nor spent on it.

  cheap     the cheap model of whichever AI tool this project runs
  <model>   a model id, the same way you would name one in the composer
  chat      whatever the project's chat is running on, in the chat's own
            session — no second session, and the chat's own context spent on it

If the model can't be run — the account doesn't offer it, or its session won't
start — the pass falls back to the chat's own session rather than being lost,
and the pass table under the project's Settings → Memory says which model each pass really ran on.

The default is cheap.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			show := func(p api.Project) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Called(), consolidationModelWords(p.ConsolidationModel))
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Is(args[0]) {
						show(p)
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			model := args[1]
			if model == "chat" {
				model = api.ConsolidationModelChat
			}
			p, err := c.SetConsolidationModel(cmd.Context(), args[0], model)
			if err != nil {
				return err
			}
			show(p)
			return nil
		},
	}
}

func consolidationModelWords(model string) string {
	switch model {
	case api.ConsolidationModelChat:
		return "the chat's own model, in the chat's own session"
	case api.ConsolidationModelCheap:
		return "cheap — the cheap model of whichever AI tool this project runs"
	}
	return model + " — in a session of its own"
}

func consolidationWords(every int) string {
	if every <= 0 {
		return "off — its memories are only ever added to"
	}
	return fmt.Sprintf("every %d events — its chat is asked to distil them into memories", every)
}

// describeConsolidation is the compression this has bought, in one line, plus
// what is waiting. It is the whole point of consolidation made visible.
func describeConsolidation(c api.MemoryConsolidation) string {
	out := fmt.Sprintf("  %d events, %d live memories", c.Events, c.Memories)
	if c.Memories > 0 && c.Events > 0 {
		out += fmt.Sprintf(" (%.0f events per memory)", float64(c.Events)/float64(c.Memories))
	}
	out += "\n"
	if c.Pending > 0 {
		out += fmt.Sprintf("  %d events waiting to be distilled\n", c.Pending)
	}
	if c.Duplicates > 0 {
		out += fmt.Sprintf("  %d near-duplicate pairs flagged\n", c.Duplicates)
	}
	if c.Passes > 0 {
		out += fmt.Sprintf("  %d passes, %d memories written, %d replaced, %d closed\n",
			c.Passes, c.MemoriesWritten, c.MemoriesSuperseded, c.MemoriesResolved)
	}
	return out
}

func passWords(p api.ConsolidationPass) string {
	if p.Error != "" {
		return fmt.Sprintf("the %s pass didn't finish: %s", p.Kind, p.Error)
	}
	if p.Kind == api.ConsolidationDistill {
		return fmt.Sprintf("%d events became %d memories, %d replaced, %d closed",
			p.EventsRead, p.MemoriesWritten, p.MemoriesSuperseded, p.MemoriesResolved)
	}
	return fmt.Sprintf("merged %d memories, aged %d, flagged %d near-duplicate pairs",
		p.MemoriesSuperseded, p.MemoriesDecayed, p.DuplicatesFound)
}

func newBriefCmd(a *app) *cobra.Command {
	var agentName string
	cmd := &cobra.Command{
		Use:   "brief <project>",
		Short: "Show the instructions an agent receives about its environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			text, err := c.Brief(cmd.Context(), args[0], agentName)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), text)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "agent-01", "agent name to preview")
	return cmd
}

// newProjectGitCmd is "agentbox project git": the two remotes a project's
// cluster agents clone and push with, and the login each signs in with. A
// credential's value goes in once, with --from-file, and is never printed.
func newProjectGitCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "The git profiles a project's cluster agents clone and push with",
		Long: `Where a project's agents clone from, and how they sign in there.

An agent in a cluster has no host checkout and no host identity: its pod clones
one of its project's two git profiles. Private is where work in progress goes,
and is what agents use unless the project says otherwise; public is where a
finished change is published. A project added from a repository gets private
from its hub remote and public from its origin, without a login: set one before
its first cluster agent.

  agentbox project git set myapp private https://hub.example/o/myapp.git --from-file ~/.config/token
  agentbox project git use myapp public
  agentbox project git test myapp

A URL may be the alias this machine already uses (forge:h4ad/myapp.git): it is
resolved here, and the pod is given a real URL. A token profile clones over
https, which is what a token answers; --auth ssh-key keeps ssh and needs the
private key itself, also read with --from-file.`,
	}
	cmd.AddCommand(newProjectGitShowCmd(a), newProjectGitSetCmd(a), newProjectGitUseCmd(a),
		newProjectGitTestCmd(a), newProjectGitRemoveCmd(a))
	return cmd
}

func newProjectGitShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <project>",
		Short: "Show a project's git profiles",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			pg, err := c.ProjectGit(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s uses its %s profile for cluster agents.\n\n", args[0], pg.AgentRemote)
			return writeProjectGit(cmd.OutOrStdout(), pg)
		},
	}
}

func newProjectGitSetCmd(a *app) *cobra.Command {
	var auth, credential, fromFile string
	cmd := &cobra.Command{
		Use:   "set <project> <private|public> <url>",
		Short: "Set one of a project's git profiles, and its login",
		Long: `Set one of a project's two git profiles.

--auth says how the URL signs in: token (the default), ssh-key, or
github-account, which uses a login already stored with "agentbox auth github".
--credential names the stored login, and defaults to the profile's own name.

--from-file stores the secret too: a token, or the private key itself for
--auth ssh-key. For a token profile, a token is one line and answers over https,
which is what a token can answer, so an ssh URL becomes https on the same host.
The secret is read here, sent once, and never printed.

  op read op://work/forgejo/token | agentbox project git set myapp private https://hub.example/o/myapp.git --from-file -
  agentbox project git set myapp private git@github.com:o/myapp.git --auth ssh-key --from-file ~/.ssh/id_ed25519`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, profile, url := args[0], args[1], args[2]
			// Only what was passed is sent: an existing profile keeps its auth
			// and credential when just the URL changes.
			req := api.SetProjectGitProfileRequest{URL: url}
			if cmd.Flags().Changed("auth") {
				req.Auth = auth
			}
			if cmd.Flags().Changed("credential") {
				req.Credential = credential
			}
			if fromFile != "" {
				secret, err := readGitCredential(cmd, fromFile)
				if err != nil {
					return err
				}
				req.Secret = secret
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			pg, err := c.SetProjectGitProfile(cmd.Context(), project, profile, req)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Set %s's %s git profile.\n\n", project, profile)
			return writeProjectGit(cmd.OutOrStdout(), pg)
		},
	}
	cmd.Flags().StringVar(&auth, "auth", "token", "how the URL signs in: token, ssh-key or github-account (default: token for a new profile, else unchanged)")
	cmd.Flags().StringVar(&credential, "credential", "", "the stored login's name (default: the profile's name, for token and ssh-key)")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "read the token or private key from this file, or from stdin with -")
	return cmd
}

func newProjectGitUseCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "use <project> <private|public>",
		Short: "Choose the profile a project's cluster agents clone with",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			pg, err := c.UseProjectGitProfile(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s now uses its %s profile for cluster agents.\n", args[0], pg.AgentRemote)
			return nil
		},
	}
}

func newProjectGitTestCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "test <project> [private|public]",
		Short: "Ask a profile's remote for its refs with the stored login",
		Long: `Ask a profile's remote for its refs, with the login the profile stores.

The check runs where the login is kept, in a temporary directory only that
account can read, so the login is never an argument to a command. Without a
profile it tests the one the project's agents use. It says how many refs
answered, or a reason with the login taken out of it.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := ""
			if len(args) > 1 {
				profile = args[1]
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out, err := c.TestProjectGitProfile(cmd.Context(), args[0], profile)
			if err != nil {
				return err
			}
			if !out.OK {
				return fmt.Errorf("%s's %s git profile (%s) didn't answer: %s", args[0], out.Profile, out.URL, out.Detail)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s's %s git profile accepted the login: %d refs from %s\n", args[0], out.Profile, out.Refs, out.URL)
			return nil
		},
	}
}

func newProjectGitRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <project> <private|public>",
		Short: "Forget one of a project's git profiles",
		Long: `Forget one of a project's two git profiles.

Its login goes with it, unless the other profile names the same one. A project
whose agents were using it moves to the profile that is left.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			pg, err := c.RemoveProjectGitProfile(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s's %s git profile.\n\n", args[0], args[1])
			return writeProjectGit(cmd.OutOrStdout(), pg)
		},
	}
}

// writeProjectGit is a project's profiles as a table: which is in use, where it
// points, and which login it means. A credential's value is not in the API's
// answer, so there is nothing here to leave out.
func writeProjectGit(out io.Writer, pg api.ProjectGit) error {
	if len(pg.Profiles) == 0 {
		_, _ = fmt.Fprintln(out, "No git profiles yet. Set one with:")
		_, _ = fmt.Fprintln(out, "  agentbox project git set <project> private <url> --from-file <file>")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "PROFILE\tUSE\tURL\tAUTH\tLOGIN\tRESOLVED")
	for _, p := range pg.Profiles {
		use := ""
		if p.Profile == pg.AgentRemote {
			use = "yes"
		}
		login := p.Credential + " (not stored)"
		if p.HasCredential {
			login = p.Credential + " (stored)"
		}
		if p.Credential == "" {
			login = "none yet"
		}
		resolved := p.ResolvedURL
		if p.ResolveError != "" {
			resolved = "not resolvable: " + p.ResolveError
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Profile, use, p.URL, p.Auth, login, resolved)
	}
	return w.Flush()
}

// readGitCredential reads a token or a private key the user points at. "-" is
// stdin, which is how a password manager's output is piped in; the value is
// never echoed, printed or put on a command line.
func readGitCredential(cmd *cobra.Command, path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", err
		}
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("no credential in %s: it is empty", path)
	}
	return secret, nil
}

package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newAddCmd(a *app) *cobra.Command {
	var name, claudeAccount, githubAccount string
	var copyToLinux bool
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Register a git repository as a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			p, err := c.AddProject(cmd.Context(), api.AddProjectRequest{
				Path: path, Name: name, ClaudeAccount: claudeAccount, GitHubAccount: githubAccount, CopyToLinux: copyToLinux,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Added project %s\n\n", p.Name)
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "  root\t%s\n", p.Root)
			fmt.Fprintf(w, "  branch\t%s (new agents start here)\n", p.Branch)
			fmt.Fprintf(w, "  env files\t%s\n", describeEnvFiles(p.EnvFiles))
			fmt.Fprintf(w, "  claude account\t%s\n", cmpOrDefault("claude", p.ClaudeAccount))
			fmt.Fprintf(w, "  github account\t%s\n", cmpOrDefault("github", p.GitHubAccount))
			if p.Android {
				fmt.Fprintf(w, "  android\tdetected: its agents can run their own emulator (agentbox android start)\n")
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name (default: derived from the directory name)")
	cmd.Flags().StringVar(&claudeAccount, "claude-account", "", "Claude Code account its agents use (default: this machine's default account)")
	cmd.Flags().StringVar(&githubAccount, "github-account", "", "GitHub account its agents use (default: this machine's default account)")
	cmd.Flags().BoolVar(&copyToLinux, "copy", false, "in WSL, add a clone in ~/src/<name> of a repository on a Windows drive, rather than refusing it")
	return cmd
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
	cmd.AddCommand(newProjectModelCmd(a), newProjectBranchPrefixCmd(a))
	return cmd
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
					if p.Name == args[0] {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, agentModelWords(p.AgentModel))
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
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, agentModelWords(p.AgentModel))
			return nil
		},
	}
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
agent's name. A project starts on agentbox/, which puts agent-01 on the branch
agentbox/agent-01.

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
					if p.Name == args[0] {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, branchPrefixWords(p.BranchPrefix))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			p, err := c.SetBranchPrefix(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, branchPrefixWords(p.BranchPrefix))
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
				fmt.Fprintln(out, "No projects yet. Add one with: agentbox add <path>")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "PROJECT\tCLAUDE ACCOUNT\tGITHUB ACCOUNT\tROOT")
			for _, p := range projects {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, orDash(p.ClaudeAccount), orDash(p.GitHubAccount), p.Root)
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
			fmt.Fprintf(cmd.OutOrStdout(), "Removed project %s\n", args[0])
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
					if p.Name == args[0] {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, finishNoticeWords(p.FinishNotices))
						return nil
					}
				}
				return fmt.Errorf("no project named %q", args[0])
			}
			p, err := c.SetFinishNotices(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, finishNoticeWords(p.FinishNotices))
			return nil
		},
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
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, rolloverWords(p.RolloverThreshold))
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Name == args[0] {
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
				fmt.Fprintf(cmd.OutOrStdout(), "%s: the chat was consolidated into the project's memory and carried on in a fresh session\n", args[0])
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
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %d tokens a context, %d for one agent\n",
					p.Name, p.ContextBudget, p.ContextBudget/4)
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Name == args[0] {
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
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, consolidationWords(p.Consolidation))
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Name == args[0] {
						show(p)
						state, err := c.ProjectMemory(p.Name).Consolidation(cmd.Context())
						if err == nil {
							fmt.Fprint(cmd.OutOrStdout(), describeConsolidation(state))
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
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", args[0], passWords(p))
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
and the Memory tab's pass table says which model each pass really ran on.

The default is cheap.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			show := func(p api.Project) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, consolidationModelWords(p.ConsolidationModel))
			}
			if len(args) == 1 {
				projects, err := c.Projects(cmd.Context())
				if err != nil {
					return err
				}
				for _, p := range projects {
					if p.Name == args[0] {
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
			fmt.Fprint(cmd.OutOrStdout(), text)
			return nil
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "agent-01", "agent name to preview")
	return cmd
}

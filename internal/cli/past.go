package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// Past agents: agents whose machine was removed, kept with their chat until
// deleted (the daemon's archive.go).

func newPastCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "past",
		Short: "Past agents: ones whose machine was removed, kept with their branch, pull request and chat",
		Long: `Destroying or retiring an agent removes its machine and worktree and keeps the
agent as a past agent: its title, task, branch, pull request, chat, media and
token usage, until you delete it. Its name is never given to another agent.

Read a past agent's chat with: agentbox chat <project/agent>`,
	}
	cmd.AddCommand(newPastListCmd(a), newPastShowCmd(a), newPastDeleteCmd(a), newPastContinueCmd(a))
	return cmd
}

// pastFlags are the list's filters, shared with agentbox list --archived.
type pastFlags struct{ query, pr, since, until string }

func (f *pastFlags) add(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.query, "search", "", "only past agents whose name, title, task, branch or pull request holds this")
	fl.StringVar(&f.pr, "pr", "", "only those whose pull request is open, merged or closed, or none for those with none")
	fl.StringVar(&f.since, "since", "", "only those archived on or after this date, like 2026-10-01")
	fl.StringVar(&f.until, "until", "", "only those archived on or before this date")
}

func (f pastFlags) options(project string) api.ArchivedOptions {
	return api.ArchivedOptions{Project: project, Query: f.query, PR: f.pr, Since: f.since, Until: f.until}
}

func newPastListCmd(a *app) *cobra.Command {
	var f pastFlags
	cmd := &cobra.Command{
		Use:     "list [project]",
		Aliases: []string{"ls"},
		Short:   "List past agents, the most recently archived first",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return listPast(cmd, a, firstArg(args), f)
		},
	}
	f.add(cmd)
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func listPast(cmd *cobra.Command, a *app, project string, f pastFlags) error {
	c, err := a.client(cmd)
	if err != nil {
		return err
	}
	past, err := c.ArchivedAgents(cmd.Context(), f.options(project))
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(past) == 0 {
		_, _ = fmt.Fprintln(out, "No past agents.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	// TITLE comes last: it can contain spaces, and scripts read the columns before it.
	_, _ = fmt.Fprintln(w, "AGENT\tAI\tBRANCH\tPR\tARCHIVED\tTITLE")
	for _, p := range past {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Ref, p.AI, orDash(p.Branch), prCell(p.PR), ago(p.ArchivedAt), orDash(p.Title))
	}
	return w.Flush()
}

func prCell(pr *api.PullRequest) string {
	if pr == nil {
		return "-"
	}
	return fmt.Sprintf("#%d %s", pr.Number, pr.State)
}

func newPastShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <project/agent>",
		Short: "Show a past agent: its task, branch and pull request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			p, err := c.ArchivedAgent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "%s (past agent)\n", p.Ref)
			_, _ = fmt.Fprintf(w, "  title\t%s\n", orDash(p.Title))
			_, _ = fmt.Fprintf(w, "  ai\t%s\n", p.AI)
			_, _ = fmt.Fprintf(w, "  branch\t%s (from %s)\n", orDash(p.Branch), orDash(p.BaseRef))
			if p.PR != nil {
				_, _ = fmt.Fprintf(w, "  pr\t#%d %s, %s %s\n", p.PR.Number, p.PR.State, p.PR.Title, p.PR.URL)
			}
			_, _ = fmt.Fprintf(w, "  created\t%s\n", p.CreatedAt.Local().Format(time.DateTime))
			_, _ = fmt.Fprintf(w, "  archived\t%s\n", p.ArchivedAt.Local().Format(time.DateTime))
			if p.Tokens > 0 {
				_, _ = fmt.Fprintf(w, "  tokens\t%d ($%.2f)\n", p.Tokens, p.CostUSD)
			}
			_ = w.Flush()
			if p.Task != "" {
				_, _ = fmt.Fprintf(out, "\nTask:\n%s\n", p.Task)
			}
			_, _ = fmt.Fprintf(out, "\nRead its chat with: agentbox chat %s\nContinue it with: agentbox past continue %s\n", p.Ref, p.Ref)
			return nil
		},
	}
}

func newPastDeleteCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <project/agent>...",
		Short: "Delete past agents for good, with their chats (their branches and media stay)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			res, err := c.DeleteArchived(cmd.Context(), args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, ref := range res.Deleted {
				_, _ = fmt.Fprintf(out, "Deleted %s\n", ref)
			}
			if len(res.Failed) > 0 {
				var failed []string
				for _, ref := range args {
					if why, ok := res.Failed[ref]; ok {
						failed = append(failed, fmt.Sprintf("%s: %s", ref, why))
					}
				}
				return fmt.Errorf("couldn't delete %s", strings.Join(failed, "; "))
			}
			return nil
		},
	}
}

func newPastContinueCmd(a *app) *cobra.Command {
	var ai string
	cmd := &cobra.Command{
		Use:   "continue <project/agent> [message]",
		Short: "Start a new agent from a past one's branch, told a short summary of its chat and then the message",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start := time.Now()
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			j, err := c.ContinueArchived(cmd.Context(), args[0], api.ContinueArchivedRequest{
				Message: strings.TrimSpace(strings.Join(args[1:], " ")), AI: ai,
			})
			if err != nil {
				return err
			}
			return finishAgentJob(cmd, c, j, start)
		},
	}
	cmd.Flags().StringVar(&ai, "ai", "", "the new agent's AI tool (default: the past agent's)")
	return cmd
}

// archivedParams are list_agents' parameters for past agents, for a lead and
// for the Home chat.
var archivedParams = map[string]any{
	"archived": map[string]any{"type": "boolean", "description": "list past agents instead: ones whose machine was removed, kept with their chat"},
	"query":    str("with archived: only those whose name, title, task, branch or pull request holds this"),
	"pr":       choiceOf("with archived: only those whose pull request is in this state, or none", api.PRStateOpen, api.PRStateMerged, api.PRStateClosed, api.PRStateNone),
	"since":    str("with archived: only those archived on or after this date, like 2026-10-01"),
}

// withProjectParam is archivedParams with the Home chat's project parameter.
func withProjectParam(project map[string]any) map[string]any {
	out := map[string]any{"project": project}
	for k, v := range archivedParams {
		out[k] = v
	}
	return out
}

type archivedArgs struct {
	Archived         bool
	Query, PR, Since string
}

func (in archivedArgs) options(project string) api.ArchivedOptions {
	return api.ArchivedOptions{Project: project, Query: in.Query, PR: in.PR, Since: in.Since, Limit: 50}
}

// describeArchived is past agents for a chat to read, newest first.
func describeArchived(past []api.ArchivedAgent) string {
	if len(past) == 0 {
		return "No past agents."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d past agent(s), most recently archived first. read_agent reads one's chat.\n", len(past))
	for _, p := range past {
		fmt.Fprintf(&b, "\n- %s", p.Name)
		if p.Title != "" {
			fmt.Fprintf(&b, " %q", p.Title)
		}
		fmt.Fprintf(&b, ", archived %s, branch %s", p.ArchivedAt.Format(time.DateOnly), orDash(p.Branch))
		if p.PR != nil {
			fmt.Fprintf(&b, ", PR #%d (%s) %s", p.PR.Number, p.PR.State, p.PR.URL)
		}
		if p.Task != "" {
			task := strings.Join(strings.Fields(p.Task), " ")
			if r := []rune(task); len(r) > 200 {
				task = string(r[:200]) + "…"
			}
			fmt.Fprintf(&b, "\n  task: %s", task)
		}
	}
	return b.String()
}

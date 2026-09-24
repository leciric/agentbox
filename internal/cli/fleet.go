package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// newFleetCmd follows a project's agents: what each is, what it's doing now,
// what it has changed, and where its branch stands on GitHub.
func newFleetCmd(a *app) *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:   "fleet <project>",
		Short: "Follow a project's agents: what each is doing, changed and opened",
		Long: `Shows every agent of a project in one table: its state, what its chat is doing,
how much it has changed, how much it has shown, and its pull request when
AgentBox has a GitHub token (agentbox auth github).

Agents still being created are listed too, so you can watch them appear.`,
		Example: `  agentbox fleet pawly
  agentbox fleet pawly --watch`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			for {
				fleet, err := waitForPulls(cmd.Context(), c, args[0])
				if err != nil {
					return err
				}
				printFleet(cmd, fleet)
				if !watch {
					return nil
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(3 * time.Second):
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "keep showing it, refreshed every few seconds")
	return cmd
}

// waitForPulls reads the fleet, and waits out the first read of the
// repository's pull requests when one is still in flight. The daemon serves
// what it has at once, for an app that polls and redraws; a command that
// prints a table once and exits would rather wait a moment than print a fleet
// with the pull request column empty.
func waitForPulls(ctx context.Context, c *api.Client, project string) (api.Fleet, error) {
	deadline := time.Now().Add(8 * time.Second)
	for attempt := 0; ; attempt++ {
		fleet, err := c.Fleet(ctx, project)
		if err != nil || !fleet.PullsRefreshing || fleet.PullsFetchedAt != nil || time.Now().After(deadline) {
			return fleet, err
		}
		wait := min(100*time.Millisecond*time.Duration(attempt+1), 500*time.Millisecond)
		select {
		case <-ctx.Done():
			return fleet, nil
		case <-time.After(wait):
		}
	}
}

func printFleet(cmd *cobra.Command, fleet api.Fleet) {
	out := cmd.OutOrStdout()
	if len(fleet.Agents) == 0 && len(fleet.Creating) == 0 {
		fmt.Fprintf(out, "%s has no agents yet. Create one with: agentbox create %s\n", fleet.Project, fleet.Project)
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tTITLE\tSTATE\tDOING\tCHANGES\tMEDIA\tPULL REQUEST")
	for _, j := range fleet.Creating {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", "(creating)", j.Target, "creating", "being made", "-", "-", "-")
	}
	for _, f := range fleet.Agents {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			f.Name, dash(f.Title), f.State, doingOrIdle(f), changes(f.Changes), mediaCount(f.Media), pullRequest(f.PR))
	}
	w.Flush()
	if fleet.Idle > 0 {
		fmt.Fprintf(out, "\n%d agent(s) finished and are holding a machine. Free them with: agentbox retire %s\n", fleet.Idle, fleet.Project)
	}
	if fleet.GitHub != "" {
		// Which account these were read with: the same repository is a 404 for
		// an account that isn't in it, so "none" would be a half-truth.
		account := ""
		if fleet.GitHubAccount != "" {
			account = " as " + fleet.GitHubAccount
		}
		fmt.Fprintf(out, "\nGitHub: %s%s\n", fleet.GitHub, account)
	}
	if fleet.GitHubError != nil {
		fmt.Fprintf(out, "GitHub: %s\n", githubErrorLine(fleet.GitHubError))
	}
}

// githubErrorLine says why pull requests couldn't be read and what would fix
// it, naming the account they were read with: the same sentences the app
// shows, with commands instead of links.
func githubErrorLine(err *api.GitHubError) string {
	account := err.Account
	if err.Login != "" {
		account = fmt.Sprintf("%s (%s)", err.Account, err.Login)
	}
	switch err.Kind {
	case api.GitHubNoAccess:
		return fmt.Sprintf("the account %s can't see %s. Pick another one with agentbox github-account <project> <account>, or add one with agentbox auth github --account <name>",
			account, err.Repo)
	case api.GitHubBadToken:
		return fmt.Sprintf("GitHub refused the account %s. Save its token again with agentbox auth github --account %s", account, err.Account)
	case api.GitHubNoAccount:
		return fmt.Sprintf("%s. Add one with agentbox auth github --account <name>", err.Message)
	default:
		return err.Message
	}
}

// doingOrIdle says what the agent is doing, or that it has finished and is
// holding a machine for nothing.
func doingOrIdle(f api.FleetAgent) string {
	if f.Idle {
		return "idle, holding a machine"
	}
	return dash(chatDoing(f.Chat))
}

// chatDoing turns a chat's state into what the agent is doing right now.
func chatDoing(state string) string {
	switch state {
	case api.ChatRunning:
		return "working"
	case api.ChatWaiting:
		return "waiting for you"
	case api.ChatStarting:
		return "starting"
	case api.ChatReady:
		return "idle"
	case api.ChatError:
		return "its AI tool stopped"
	}
	return ""
}

func changes(c api.AgentChanges) string {
	if c.Files == 0 {
		return "-"
	}
	out := fmt.Sprintf("%d file", c.Files)
	if c.Files != 1 {
		out += "s"
	}
	if c.Insertions > 0 || c.Deletions > 0 {
		out += fmt.Sprintf(" +%d/-%d", c.Insertions, c.Deletions)
	}
	if c.Dirty {
		out += "*"
	}
	return out
}

func mediaCount(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

func pullRequest(pr *api.PullRequest) string {
	if pr == nil {
		return "-"
	}
	parts := []string{fmt.Sprintf("#%d %s", pr.Number, pr.State)}
	if pr.Draft {
		parts[0] += " (draft)"
	}
	if pr.Checks != "" {
		parts = append(parts, "checks "+pr.Checks)
	}
	if pr.Comments > 0 {
		parts = append(parts, fmt.Sprintf("%d comment(s)", pr.Comments))
	}
	return strings.Join(parts, ", ")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// newRetireCmd frees what a project's finished agents are holding. An agent is
// one task: its work is on its branch, which outlives it, so the way to move on
// is to retire it and make a new one rather than reuse it for something else.
func newRetireCmd(a *app) *cobra.Command {
	var req api.RetireRequest
	cmd := &cobra.Command{
		Use:   "retire <project> [agent...]",
		Short: "Free what a project's finished agents are holding",
		Long: `Frees the machines of agents that have finished, so they don't sit holding
memory and disk for nothing.

  --how stop      shut the machine down: memory freed, disk kept (the default)
  --how pause     freeze it: no CPU, memory kept, back in an instant
  --how destroy   remove the machine and the worktree

A branch is never deleted. An agent's work is its branch, and the branch stays
whichever way you retire it, so starting the next task is a new agent rather
than reusing this one: creating one takes about a second from the project's base.

Agents that are still working are left alone, and so are agents whose work isn't
committed, unless you name them with --force.`,
		Example: `  agentbox retire pawly --dry-run
  agentbox retire pawly --idle-for 30m
  agentbox retire pawly agent-03 --how destroy`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			req.Agents = args[1:]
			result, err := c.Retire(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			printRetired(cmd, result)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.How, "how", "", "pause, stop (the default) or destroy")
	f.StringVar(&req.IdleFor, "idle-for", "", "only agents idle at least this long, like 30m")
	f.BoolVar(&req.Force, "force", false, "retire an agent whose work isn't committed")
	f.BoolVar(&req.DryRun, "dry-run", false, "say what would happen, without doing it")
	return cmd
}

func printRetired(cmd *cobra.Command, r api.RetireResult) {
	out := cmd.OutOrStdout()
	did := map[string]string{api.RetirePause: "Paused", api.RetireStop: "Stopped", api.RetireDestroy: "Destroyed"}[r.How]
	if r.DryRun {
		did = "Would " + map[string]string{api.RetirePause: "pause", api.RetireStop: "stop", api.RetireDestroy: "destroy"}[r.How]
	}
	if len(r.Retired) == 0 {
		fmt.Fprintln(out, "Nothing to retire: no agent has finished and is holding a machine.")
	}
	for _, who := range r.Retired {
		fmt.Fprintf(out, "%s %s%s", did, who.Name, titleSuffix(who.Title))
		if who.Branch != "" {
			fmt.Fprintf(out, " — its work stays on %s", who.Branch)
		}
		fmt.Fprintln(out)
	}
	for _, who := range r.Skipped {
		fmt.Fprintf(out, "Left %s%s: %s\n", who.Name, titleSuffix(who.Title), who.Reason)
	}
	if len(r.Retired) > 0 && r.How == api.RetireDestroy && !r.DryRun {
		fmt.Fprintln(out, "\nTheir branches are still there. Start the next task with a new agent.")
	}
}

func titleSuffix(title string) string {
	if title == "" {
		return ""
	}
	return " (" + title + ")"
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

func newPsCmd(a *app) *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:   "ps [project | project/agent]",
		Short: "Show what agents still have running in their machines, as process trees, the biggest first",
		Long: `Show what each agent with a running machine still has running: every process in
its machine, grouped in trees (a dev server with what it started is one), the biggest
first, with memory, CPU, age and how long it has been idle. Heavy (1 GiB or more) and
long-idle (30 minutes or more) trees are flagged. The AI tool's own processes and the
machine's are shown too, protected: agentbox kill refuses them.

Name a project for its agents only, or <project>/<agent> for one. Kill a tree with
agentbox kill <project/agent> <pid>.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			var agents []api.AgentProcesses
			project, name := "", ""
			if len(args) == 1 {
				project, name, _ = projectOrAgent(args[0])
			}
			if name != "" {
				ap, err := c.AgentProcesses(cmd.Context(), project+"/"+name)
				if err != nil {
					return err
				}
				agents = []api.AgentProcesses{ap}
			} else {
				list, err := c.Processes(cmd.Context(), project)
				if err != nil {
					return err
				}
				agents = list.Agents
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(agents)
			}
			renderProcesses(out, agents, all, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the agents and their trees as JSON")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "list each tree's processes too")
	return cmd
}

// renderProcesses prints each agent's trees as a table under a line about
// the agent.
func renderProcesses(w io.Writer, agents []api.AgentProcesses, members bool, now time.Time) {
	if len(agents) == 0 {
		_, _ = fmt.Fprintln(w, "No agent has a running machine.")
		return
	}
	for i, ap := range agents {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		head := ap.Ref
		if ap.Title != "" {
			head += "  " + strconv.Quote(ap.Title)
		}
		switch {
		case ap.Error != "":
			_, _ = fmt.Fprintf(w, "%s  couldn't read its processes: %s\n", head, ap.Error)
			continue
		case !ap.Running:
			_, _ = fmt.Fprintf(w, "%s  machine not running\n", head)
			continue
		}
		_, _ = fmt.Fprintf(w, "%s  %s in %d processes, %s of it can be killed\n", head, humanBytes(ap.Memory), ap.Processes, humanBytes(ap.Killable))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  PID\tMEMORY\tCPU\tAGE\tIDLE\tPROCS\tCOMMAND")
		for _, t := range ap.Trees {
			idle := "-"
			if !t.Protected && t.IdleSeconds >= 60 {
				idle = spanWords(time.Duration(t.IdleSeconds) * time.Second)
			}
			age := "-"
			if !t.Started.IsZero() {
				age = spanWords(now.Sub(t.Started))
			}
			label := t.Label
			var flags []string
			if t.Protected {
				flags = append(flags, "protected")
			}
			if t.Heavy {
				flags = append(flags, "heavy")
			}
			if t.Idle {
				flags = append(flags, "idle")
			}
			if len(flags) > 0 {
				label += "  (" + strings.Join(flags, ", ") + ")"
			}
			_, _ = fmt.Fprintf(tw, "  %d\t%s\t%.0f%%\t%s\t%s\t%d\t%s\n", t.PID, humanBytes(t.Memory), t.CPU, age, idle, t.Processes, label)
			if members && len(t.Members) > 1 {
				for _, m := range t.Members {
					_, _ = fmt.Fprintf(tw, "  \t%s\t\t\t\t\t  %d %s\n", humanBytes(m.Memory), m.PID, clipWords(m.Command, 100))
				}
			}
		}
		_ = tw.Flush()
	}
	_, _ = fmt.Fprintln(w, "\nKill a tree with: agentbox kill <project/agent> <pid>")
}

// spanWords is a duration like "45s", "12m" or "3h10m".
func spanWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h := int(d.Hours())
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func clipWords(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func newKillCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "kill <project/agent> <pid>",
		Short: "Kill a process in an agent's machine with everything it started (SIGTERM, then SIGKILL), and tell the agent",
		Long: `Kill a process in an agent's machine, by the number agentbox ps shows, with every process
it started: SIGTERM first, then SIGKILL to whatever is left 5 seconds later. The agent is
told in its chat, so it doesn't start it again without a reason. The AI tool's own
processes and the machine's are refused.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := strconv.Atoi(args[1])
			if err != nil || pid <= 0 {
				return fmt.Errorf("%q isn't a process number: agentbox ps shows them", args[1])
			}
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			res, err := c.KillProcess(cmd.Context(), args[0], pid)
			if err != nil {
				return err
			}
			msg := fmt.Sprintf("Killed %s in %s: %d %s, %s.", strconv.Quote(res.Label), res.Ref, res.Processes, plural(res.Processes, "process", "processes"), humanBytes(res.Memory))
			if res.Forced > 0 {
				msg += fmt.Sprintf(" %d needed SIGKILL.", res.Forced)
			}
			if res.Left > 0 {
				msg += fmt.Sprintf(" %d still hadn't ended after it.", res.Left)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), msg+" Its agent is being told in its chat.")
			return nil
		},
	}
}

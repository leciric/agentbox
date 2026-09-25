package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// waitJob prints a job's log to stderr as it runs and returns the finished
// job. Ctrl-C only detaches: the job keeps running in the daemon.
func waitJob(cmd *cobra.Command, c *api.Client, j api.Job) (api.Job, error) {
	stderr := cmd.ErrOrStderr()
	err := c.FollowJobLog(cmd.Context(), j.ID, stderr)
	if cmd.Context().Err() != nil {
		_, _ = fmt.Fprintf(stderr, "\nDetached from job %s. It keeps running in the daemon:\n  follow it:  agentbox jobs %s\n  cancel it:  agentbox jobs cancel %s (rolls back)\n", j.ID, j.ID, j.ID)
		return j, exitCodeError(130)
	}
	if err != nil {
		return j, err
	}
	final, err := c.Job(context.WithoutCancel(cmd.Context()), j.ID)
	if err != nil {
		return j, err
	}
	if final.Status != api.JobSucceeded {
		return final, errors.New(final.Error)
	}
	return final, nil
}

func newJobsCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs [id]",
		Short: "List the daemon's jobs, or show one (following it while it runs)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				jobs, err := c.Jobs(cmd.Context())
				if err != nil {
					return err
				}
				if len(jobs) == 0 {
					_, _ = fmt.Fprintln(out, "No jobs yet.")
					return nil
				}
				w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
				_, _ = fmt.Fprintln(w, "JOB\tKIND\tTARGET\tSTATUS\tSTARTED\tTOOK")
				for _, j := range jobs {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, j.Kind, j.Target, j.Status, ago(j.CreatedAt), took(j))
				}
				return w.Flush()
			}

			j, err := c.Job(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if j.Done() {
				log, err := c.JobLog(cmd.Context(), j.ID)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprint(cmd.ErrOrStderr(), log)
			} else if j, err = waitJob(cmd, c, j); err != nil && j.Status == api.JobRunning {
				return err
			}
			_, _ = fmt.Fprintf(out, "\nJob %s (%s %s): %s after %s\n", j.ID, j.Kind, j.Target, j.Status, took(j))
			if j.Error != "" {
				_, _ = fmt.Fprintf(out, "  %s\n", j.Error)
			}
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a running job; what it already did is rolled back",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			j, err := c.CancelJob(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Job %s (%s %s): %s\n", j.ID, j.Kind, j.Target, j.Status)
			return nil
		},
	})
	return cmd
}

func took(j api.Job) string {
	end := time.Now()
	if j.FinishedAt != nil {
		end = *j.FinishedAt
	}
	return end.Sub(j.CreatedAt).Round(100 * time.Millisecond).String()
}

func newEventsCmd(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Stream the daemon's events: jobs and their logs, agent state changes and resource use",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			err = c.Events(cmd.Context(), func(ev api.Event) error {
				if asJSON {
					b, _ := json.Marshal(ev)
					_, err := fmt.Fprintln(out, string(b))
					return err
				}
				_, err := fmt.Fprintln(out, formatEvent(ev))
				return err
			})
			if cmd.Context().Err() != nil {
				return nil
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print each event as JSON")
	return cmd
}

func formatEvent(ev api.Event) string {
	ts := ev.Time.Local().Format(time.TimeOnly)
	switch ev.Type {
	case api.EventJob:
		var j api.Job
		_ = json.Unmarshal(ev.Data, &j)
		line := fmt.Sprintf("%s  job    %s %s %s [%s]", ts, j.Kind, j.Target, j.Status, j.ID)
		if j.Error != "" {
			line += ": " + j.Error
		}
		return line
	case api.EventJobLog:
		var l api.JobLogLine
		_ = json.Unmarshal(ev.Data, &l)
		return fmt.Sprintf("%s  log    [%s] %s", ts, l.Job, l.Line)
	case api.EventAgent:
		var ch api.AgentChange
		_ = json.Unmarshal(ev.Data, &ch)
		if ch.Removed {
			return fmt.Sprintf("%s  agent  %s removed", ts, ch.Ref)
		}
		return strings.TrimSpace(fmt.Sprintf("%s  agent  %s %s %s", ts, ch.Ref, ch.State, ch.IP))
	case api.EventUsage:
		var u api.Usage
		_ = json.Unmarshal(ev.Data, &u)
		parts := []string{fmt.Sprintf("host cpu %.0f%% mem %s", u.Host.CPU, humanBytes(u.Host.MemUsed))}
		for _, a := range u.Agents {
			parts = append(parts, fmt.Sprintf("%s cpu %.0f%% mem %s", a.Ref, a.CPU, humanBytes(a.Memory)))
		}
		return fmt.Sprintf("%s  usage  %s", ts, strings.Join(parts, " · "))
	}
	return fmt.Sprintf("%s  %s %s", ts, ev.Type, ev.Data)
}

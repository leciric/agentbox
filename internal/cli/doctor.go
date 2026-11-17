package cli

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/daemon"
	"agentbox/internal/doctor"
)

func newDoctorCmd(a *app) *cobra.Command {
	var fix, asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check what AgentBox needs, and fix what fixes itself",
		Long: "Check, in order, what AgentBox needs to run agents: the daemon and its version, Incus, disk\n" +
			"space, the network to where the base image and GitHub are, the base image, the Home chat's AI\n" +
			"tool and its login, and the GitHub account. Each check says ok, warn or fail, and what fixes it.\n\n" +
			"--fix first fixes what is safe to try again: it starts the daemon, restarts one older than this\n" +
			"command, downloads the base image and installs the chat's AI tool and the GitHub CLI. The rest\n" +
			"are commands to run, or in the app's Setup, something to ask the Home chat to fix.\n\n" +
			"It exits 1 when a check fails. On a Mac or a Linux machine running AgentBox in a VM, the command\n" +
			"there checks the VM and virtualization first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			failed, err := doctor.Run(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), func(ctx context.Context) ([]doctor.Local, *api.Client) {
				return a.doctorChecks(cmd, ctx)
			}, fix, asJSON)
			if err != nil {
				return err
			}
			if failed {
				return exitCodeError(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "fix what fixes itself first")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the checks as JSON")
	return cmd
}

// doctorChecks are the checks only this side of the socket can make: whether
// a daemon answers, and whether it's this command's version. Neither starts
// one: a doctor that started the daemon would never see it down.
func (a *app) doctorChecks(cmd *cobra.Command, ctx context.Context) ([]doctor.Local, *api.Client) {
	c := api.NewClient(a.paths.Socket())
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	info, err := c.Version(pctx)
	cancel()
	socket := doctor.Local{Check: api.DoctorCheck{ID: "socket", Title: "Daemon socket", Status: api.DoctorOK, Message: "the daemon answers on " + c.Socket()}}
	if err != nil {
		socket.Check = api.DoctorCheck{ID: "socket", Title: "Daemon socket", Status: api.DoctorFail,
			Message: fmt.Sprintf("no daemon answers on %s: %v", c.Socket(), err), Fix: "agentbox daemon start", AutoFix: true}
		socket.Fix = func(context.Context) error { return a.launchDaemon(cmd, c) }
		return []doctor.Local{socket}, nil
	}
	return []doctor.Local{socket, a.versionCheck(cmd, c, info.Version)}, c
}

// versionCheck compares the daemon's version with this command's. A daemon
// left from before an update is restarted, unless it's running jobs, which a
// restart would cancel.
func (a *app) versionCheck(cmd *cobra.Command, c *api.Client, running string) doctor.Local {
	l := doctor.Local{Check: doctor.VersionCheck(running, version, "agentbox daemon stop: the next command starts this one")}
	if l.Check.Status == api.DoctorOK {
		return l
	}
	l.Fix = func(ctx context.Context) error {
		if jobs, err := c.Jobs(ctx); err != nil || slices.ContainsFunc(jobs, func(j api.Job) bool { return !j.Done() }) {
			return fmt.Errorf("the daemon is running jobs: restart it once they finish, with agentbox daemon stop")
		}
		if err := c.Shutdown(ctx); err != nil {
			return err
		}
		daemon.WaitStopped(c.Socket(), daemon.StopWait)
		return a.launchDaemon(cmd, c)
	}
	return l
}

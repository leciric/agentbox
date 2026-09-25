// Package cli implements the agentbox command line. Most commands are clients
// of the daemon (package daemon), which the CLI starts on demand; `shell` and
// `exec` attach to the agent directly, and `auth` works on local files.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
)

var version = "dev"

// Version is this build's version: "dev", or the release's, set at link time
// with -X agentbox/internal/cli.version.
func Version() string { return version }

type app struct {
	paths paths.Paths
	env   string // an environment on a hub (--env or AGENTBOX_ENV); empty for this machine
}

func (a *app) credentials() credentials.Store {
	return credentials.Store{Dir: a.paths.Credentials()}
}

// client connects to the daemon, starting it in the background if it isn't
// running, and restarting it if it started without a group this command has.
// AGENTBOX_NO_AUTOSTART=1 turns both off.
func (a *app) client(cmd *cobra.Command) (*api.Client, error) {
	if a.env != "" {
		h, e, err := a.findEnvironment(cmd.Context(), a.env)
		if err != nil {
			return nil, err
		}
		return api.HubClient{URL: h.URL, Token: h.Token}.Environment(e.ID), nil
	}
	c := api.NewClient(a.paths.Socket())
	autostart := os.Getenv("AGENTBOX_NO_AUTOSTART") == ""
	if info, err := c.Version(cmd.Context()); err == nil {
		if autostart {
			a.restartIfStale(cmd, c, info)
		}
		return c, nil
	}
	if !autostart {
		return nil, fmt.Errorf("the AgentBox daemon isn't running on %s: start it with agentbox daemon", c.Socket())
	}
	if err := a.launchDaemon(cmd, c); err != nil {
		return nil, err
	}
	return c, nil
}

// neededGroups are the groups that give AgentBox access to Incus and KVM.
var neededGroups = []string{"incus-admin", "incus", "kvm"}

// restartIfStale restarts a daemon that can't do what this command's own
// process could. That happens after host setup: a daemon started before it
// keeps the groups it had, and may have started with no Incus to reach at all.
// A daemon running jobs is left alone.
func (a *app) restartIfStale(cmd *cobra.Command, c *api.Client, info api.VersionInfo) {
	mine, err := os.Getgroups()
	if err != nil || info.Groups == nil {
		return
	}
	reason := staleReason(missingGroups(mine, info.Groups, lookupGroup), info.Incus, incus.Client{}.Reachable())
	if reason == "" {
		return
	}
	stderr := cmd.ErrOrStderr()
	if jobs, err := c.Jobs(cmd.Context()); err != nil || slices.ContainsFunc(jobs, func(j api.Job) bool { return !j.Done() }) {
		_, _ = fmt.Fprintf(stderr, "The AgentBox daemon started before %s. Restart it when its jobs finish: agentbox daemon stop\n", reason)
		return
	}
	_, _ = fmt.Fprintf(stderr, "Restarting the AgentBox daemon, which started before %s\n", reason)
	if c.Shutdown(cmd.Context()) != nil {
		return
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && c.Ping(cmd.Context()) == nil; {
		time.Sleep(100 * time.Millisecond)
	}
	if err := a.launchDaemon(cmd, c); err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
	}
}

// staleReason says why a running daemon should be restarted, or "" when it
// shouldn't. Two things leave a daemon behind, and only one of them is about
// groups: host setup's ACL on the Incus socket applies to every process of
// this user at once, so a daemon that started when there was no Incus at all
// is stale without any group having changed.
func staleReason(missing []string, daemonIncus *bool, canUseIncus bool) string {
	switch {
	case len(missing) > 0:
		return "you joined " + strings.Join(missing, ", ")
	case daemonIncus != nil && !*daemonIncus && canUseIncus:
		return "Incus was set up on this machine"
	}
	return ""
}

// missingGroups lists the needed groups among mine that the daemon's groups lack.
func missingGroups(mine, daemon []int, lookup func(name string) (int, bool)) []string {
	var missing []string
	for _, name := range neededGroups {
		if gid, ok := lookup(name); ok && slices.Contains(mine, gid) && !slices.Contains(daemon, gid) {
			missing = append(missing, name)
		}
	}
	return missing
}

func lookupGroup(name string) (int, bool) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	gid, err := strconv.Atoi(g.Gid)
	return gid, err == nil
}

// launchDaemon starts the daemon in the background and waits until it answers.
func (a *app) launchDaemon(cmd *cobra.Command, c *api.Client) error {
	if err := a.startDaemon(); err != nil {
		return fmt.Errorf("starting the AgentBox daemon: %w", err)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if c.Ping(cmd.Context()) == nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Started the AgentBox daemon (log: %s)\n", a.paths.DaemonLog())
			return nil
		}
	}
	return fmt.Errorf("the AgentBox daemon didn't start: see %s", a.paths.DaemonLog())
}

// startDaemon runs `agentbox daemon` in its own session, so it outlives this
// command and the terminal's Ctrl-C.
func (a *app) startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.paths.Data, 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(a.paths.DaemonLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	closeInheritedFiles()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// closeInheritedFiles marks every descriptor above stderr close-on-exec, so the
// daemon doesn't hold on to files and pipes this process inherited. Electron,
// for one, leaks descriptors into the processes it starts, and a daemon holding
// them keeps the desktop app from exiting.
func closeInheritedFiles() {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if fd, err := strconv.Atoi(entry.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
}

func hostUser() (image.User, error) {
	u, err := user.Current()
	if err != nil {
		return image.User{}, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return image.User{}, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return image.User{}, err
	}
	return image.User{Name: u.Username, UID: uid, GID: gid}, nil
}

// exitCodeError makes the process exit with a command's own exit code.
type exitCodeError int

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// NewRootCmd builds the agentbox command tree.
func NewRootCmd() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:           "agentbox",
		Short:         "Isolated machines for AI coding agents",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			a.paths = p
			if a.env == "" {
				a.env = os.Getenv("AGENTBOX_ENV")
			}
			if a.env != "" && localOnly[topCommand(cmd)] {
				return fmt.Errorf("agentbox %s works on this machine only, not with --env", topCommand(cmd))
			}
			return err
		},
	}
	root.PersistentFlags().StringVar(&a.env, "env", "", "use an environment on a hub you signed in to (agentbox env list), instead of this machine")
	root.AddCommand(
		newAddCmd(a),
		newProjectsCmd(a),
		newProjectCmd(a),
		newRemoveCmd(a),
		newBriefCmd(a),
		newNotesCmd(a),
		newAuthCmd(a),
		newClaudeAccountCmd(a),
		newGitHubAccountCmd(a),
		newSecretsCmd(a),
		newImageCmd(a),
		newBaseCmd(a),
		newCreateCmd(a),
		newListCmd(a),
		newTitleCmd(a),
		newChatCmd(a),
		newFleetCmd(a),
		newRetireCmd(a),
		newMCPCmd(a),
		newDesktopCmd(a),
		newMemoryCmd(a),
		newAskCmd(a),
		newQuestionsCmd(a),
		newAnswerCmd(a),
		newAutonomyCmd(a),
		newFinishNoticesCmd(a),
		newContextBudgetCmd(a),
		newRolloverCmd(a),
		newConsolidationCmd(a),
		newConsolidationModelCmd(a),
		newLimitsCmd(a),
		newInterfaceCmd(a),
		newShellCmd(a),
		newExecCmd(a),
		newActionCmd(a, "start", "Start a stopped agent and its tmux session", "Started"),
		newActionCmd(a, "stop", "Stop an agent (its worktree and branch stay)", "Stopped"),
		newActionCmd(a, "pause", "Freeze an agent: its processes keep their memory but use no CPU", "Paused"),
		newActionCmd(a, "resume", "Unfreeze a paused agent", "Resumed"),
		newSnapshotCmd(a),
		newSnapshotsCmd(a),
		newRestoreCmd(a),
		newForkCmd(a),
		newTopCmd(a),
		newTokensCmd(a),
		newDestroyCmd(a),
		newDiffCmd(a),
		newPathCmd(a),
		newJobsCmd(a),
		newEventsCmd(a),
		newDaemonCmd(a),
		newHostCmd(a),
		newBrowserCmd(a),
		newMediaCmd(a),
		newAndroidCmd(a),
		newWhoamiCmd(),
		newVersionCmd(a),
		newRemoteCmd(a),
		newLoginCmd(a),
		newLogoutCmd(a),
		newEnvCmd(a),
		newWSLBridgeCmd(a),
	)
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := NewRootCmd().ExecuteContext(ctx)
	var code exitCodeError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	default:
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
}

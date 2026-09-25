package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/daemon"
	"agentbox/internal/incus"
)

func newDaemonCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the AgentBox daemon in the foreground (other commands start it on demand)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, err := hostUser()
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			srv, err := daemon.New(daemon.Config{
				Paths:  a.paths,
				Incus:  incus.Client{},
				User:   u,
				Binary: exe,
				Log:    cmd.ErrOrStderr(),
				// Pointed somewhere else to try the check against a
				// local server; empty is the real one.
				UpdateURL:   os.Getenv("AGENTBOX_UPDATE_URL"),
				PreviewAddr: os.Getenv("AGENTBOX_PREVIEW_ADDR"),
			})
			if err != nil {
				return err
			}
			return srv.Run(cmd.Context())
		},
	}
	cmd.AddCommand(newDaemonStartCmd(a), newDaemonStopCmd(a), newDaemonInstallCmd(), newDaemonUninstallCmd())
	return cmd
}

func newDaemonStartCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background, unless it's already running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := api.NewClient(a.paths.Socket())
			if c.Ping(cmd.Context()) != nil {
				if err := a.launchDaemon(cmd, c); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The AgentBox daemon is running on %s\n", c.Socket())
			return nil
		},
	}
}

func newDaemonStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon (running jobs are cancelled and rolled back)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := api.NewClient(a.paths.Socket())
			if err := c.Shutdown(cmd.Context()); err != nil {
				return fmt.Errorf("no daemon is answering on %s: %w", c.Socket(), err)
			}
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
				if c.Ping(cmd.Context()) != nil {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Stopped the AgentBox daemon")
					return nil
				}
			}
			return errors.New("the daemon is still running after 30s")
		},
	}
}

func unitPath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "systemd", "user", "agentbox.service"), nil
}

func newDaemonInstallCmd() *cobra.Command {
	var print bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Run the daemon as a systemd user service, started at login",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			unit := fmt.Sprintf(`[Unit]
Description=AgentBox daemon

[Service]
ExecStart=%s daemon
Restart=on-failure

[Install]
WantedBy=default.target
`, exe)
			if print {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), unit)
				return nil
			}
			path, err := unitPath()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
				return err
			}
			for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", "agentbox.service"}} {
				if out, err := exec.CommandContext(cmd.Context(), "systemctl", args...).CombinedOutput(); err != nil {
					return fmt.Errorf("systemctl %v: %w: %s", args, err, out)
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s and started agentbox.service\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&print, "print", false, "only print the unit file")
	return cmd
}

func newDaemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the systemd user service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := unitPath()
			if err != nil {
				return err
			}
			_ = exec.CommandContext(cmd.Context(), "systemctl", "--user", "disable", "--now", "agentbox.service").Run()
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			_ = exec.CommandContext(cmd.Context(), "systemctl", "--user", "daemon-reload").Run()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Removed agentbox.service")
			return nil
		},
	}
}

// inAgentSocket is api.InAgentSocket, unless AGENTBOX_IN_AGENT_SOCKET says
// otherwise: a seam so tests can point whoami at a path they control instead
// of the real in-agent socket, which may or may not exist on the machine
// running the tests.
func inAgentSocket() string {
	if s := os.Getenv("AGENTBOX_IN_AGENT_SOCKET"); s != "" {
		return s
	}
	return api.InAgentSocket
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Inside an agent: show which agent this is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			socket := inAgentSocket()
			if _, err := os.Stat(socket); err != nil {
				return fmt.Errorf("not inside an AgentBox agent (%s doesn't exist)", socket)
			}
			self, err := api.NewClient(socket).Self(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, self.Ref)
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintf(w, "  branch\t%s\n", self.Branch)
			_, _ = fmt.Fprintf(w, "  worktree\t%s\n", self.Worktree)
			_, _ = fmt.Fprintf(w, "  ip\t%s\n", self.IP)
			return w.Flush()
		},
	}
}

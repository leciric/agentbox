package hostwsl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// Main is the agentbox command on Windows: the `wsl` commands and the relay
// here, and every other command in the distro, where AgentBox runs.
func Main(args []string, version string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Printf("agentbox version %s\n", version)
		return 0
	}
	if len(args) == 0 || (args[0] != "wsl" && args[0] != "relay") {
		d, err := New()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		code, err := d.Forward(context.Background(), args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var root *cobra.Command
	if args[0] == "relay" {
		root = newRelayCmd()
	} else {
		root = newWSLCmd(version)
	}
	root.SetArgs(args[1:])
	if err := root.ExecuteContext(ctx); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			return int(code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// distro is New for the commands that can work without everything New finds:
// status says what's missing rather than failing on it.
func distro() (*Distro, error) {
	d, err := New()
	if err != nil && d.WSL == "" {
		return d, err
	}
	return d, nil
}

func newWSLCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "agentbox wsl",
		Short: "Manage the WSL distro AgentBox runs in on this computer",
		Long: `On Windows, AgentBox runs in a WSL 2 distro of its own, AgentBox: the daemon, Incus
and every agent are in there, and every agentbox command other than these runs there too.
Projects live on the distro's disk: clone them there (agentbox wsl shell).`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(newInitCmd(), newStartCmd(), newStopCmd(), newStatusCmd(), newShellCmd(), newUpgradeCmd(), newDeleteCmd())
	return root
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Make the distro, set AgentBox up in it and start its daemon (safe to run again)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := New()
			if err != nil {
				return err
			}
			if err := d.Init(cmd.Context()); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), `AgentBox's distro, %s, is ready. Next:
  agentbox image build          the machine every agent is copied from (the app's Setup page does this too)
  agentbox auth claude          a Claude Code login for your agents
  agentbox wsl shell            then git clone your project in there, and agentbox add it
`, d.Name)
			return nil
		},
	}
}

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the distro and its daemon, bringing its agentbox up to date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := New()
			if err != nil {
				return err
			}
			if err := d.ready(cmd.Context()); err != nil {
				return err
			}
			if _, err := d.EnsureBinary(cmd.Context()); err != nil {
				return err
			}
			return d.StartDaemon(cmd.Context())
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the distro, and with it the daemon and every agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := distro()
			if err != nil {
				return err
			}
			return d.Shutdown(cmd.Context())
		},
	}
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Say whether WSL and the distro are there, and what to do when they aren't",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, _ := distro()
			st := d.Status(cmd.Context())
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(st)
			}
			w := cmd.OutOrStdout()
			if st.WSL != "" {
				_, _ = fmt.Fprintf(w, "WSL %s\n", st.WSL)
			}
			if st.Exists {
				_, _ = fmt.Fprintf(w, "%s: %s, WSL %d, user %s\n", st.Name, st.State.State, st.Version, st.User)
			}
			if st.Problem != "" {
				_, _ = fmt.Fprintln(w, st.Problem)
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print it as JSON, for the app")
	return cmd
}

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Open a shell in the distro",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := New()
			if err != nil {
				return err
			}
			code, err := d.Shell(cmd.Context())
			if err == nil && code != 0 {
				return exitCode(code)
			}
			return err
		},
	}
}

func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Bring the distro's agentbox up to date with this one and restart the daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := New()
			if err != nil {
				return err
			}
			return d.Upgrade(cmd.Context())
		},
	}
}

func newDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete the distro: every project, agent and setting AgentBox has on this computer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := distro()
			if err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("this deletes %s and everything in it, pushed or not: run it again with --yes", d.Name)
			}
			return d.Unregister(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "delete it")
	return cmd
}

// newRelayCmd is `agentbox relay`, what the app connects to the daemon
// through (relay.go). It prints where it listens and serves until its stdin
// closes, which is when the app that started it has gone.
func newRelayCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "agentbox relay",
		Short:         "Carry this user's connections to the daemon in the distro, for the app",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := New()
			if err != nil {
				return err
			}
			d.Log = cmd.ErrOrStderr()
			return RunRelay(cmd.Context(), d, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// RunRelay listens where only this user can connect, says where on out, and
// serves until in ends or ctx does.
func RunRelay(ctx context.Context, d *Distro, in io.Reader, out io.Writer) error {
	ln, path, err := listenPrivate()
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, in)
		cancel()
	}()
	if _, err := fmt.Fprintf(out, "listening %s\n", path); err != nil {
		return err
	}
	r := &Relay{Distro: d}
	return r.Serve(ctx, ln)
}

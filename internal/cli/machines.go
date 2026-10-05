package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/machines"
	"agentbox/internal/machinesmedia"
	"agentbox/internal/machinesweb"
	"agentbox/internal/mcp"
)

// newMachinesCmd is desktop machines for AI tools running on the user's own
// machine rather than in an agent (internal/machines), plus the page that
// browses their screenshots and recordings. It runs here, not in AgentBox's
// VM: the machines are this machine's containers, and the AI tools asking for
// them are this machine's too (cmd/agentbox).
func newMachinesCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "machines",
		Short: "Desktop machines for Claude Code and Codex running on this computer, to test an app in",
		Long: `For the AI tools you run yourself, on this machine rather than in an AgentBox agent: their
screenshots and recordings go to one store, ` + "`<data>/machines/media`" + `, and agentbox machines serve
shows them beside your AgentBox agents' media.`,
	}
	cmd.AddCommand(newMachinesMCPCmd(a), newMachinesInstallCmd(), newMachinesUninstallCmd(),
		newMachinesBuildCmd(), newMachinesListCmd(), newMachinesRemoveCmd(), newMachinesServeCmd(a))
	return cmd
}

func newMachinesMCPCmd(a *app) *cobra.Command {
	var tool string
	cmd := &cobra.Command{
		Use:    "mcp",
		Short:  "Serve the session's machine over the Model Context Protocol, for the directory it runs in",
		Hidden: true, // started by the AI tool, from what `machines install` wrote
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			session, err := machinesSession(ctx, a, tool)
			var tools []mcp.Tool
			if err != nil {
				// Served anyway, every tool saying why: a session outside a
				// project is every session in the home directory, and a
				// server that fails to start is an error in each of them.
				tools = failingTools((&machines.Session{}).Tools(ctx), err)
			} else {
				tools = session.Tools(ctx)
			}
			srv := &mcp.Server{Name: "agentbox-machines", Version: version, Tools: tools}
			return srv.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "other", "the AI tool serving it, for its media: claude, codex or other")
	return cmd
}

func machinesSession(ctx context.Context, a *app, tool string) (*machines.Session, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	worktree, err := machines.Worktree(dir)
	if err != nil {
		return nil, err
	}
	backend, err := machines.DetectDocker(ctx)
	if err != nil {
		return nil, err
	}
	store := machinesmedia.Open(a.paths)
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	return &machines.Session{
		Backend: backend, Worktree: worktree, Media: store, Tool: tool, ID: hex.EncodeToString(id),
		Progress: func(line string) { fmt.Fprintln(os.Stderr, line) },
	}, nil
}

// failingTools are tools that all answer with err.
func failingTools(tools []mcp.Tool, err error) []mcp.Tool {
	for i := range tools {
		tools[i].Run = func(json.RawMessage) (string, error) { return "", err }
		tools[i].RunContent, tools[i].Wait = nil, nil
	}
	return tools
}

func newMachinesInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Give Claude Code and Codex the machine tools, in every session (user scope)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			return eachTarget(cmd, func(t machines.Target) (bool, error) { return machines.Install(t, bin) },
				"added to", "already in", "Sessions started from now on have it.")
		},
	}
}

func newMachinesUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Take the machine tools away from Claude Code and Codex",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return eachTarget(cmd, machines.Uninstall, "removed from", "not in", "Sessions started from now on don't have it.")
		},
	}
}

func eachTarget(cmd *cobra.Command, fn func(machines.Target) (bool, error), did, already, after string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	targets := machines.Targets(home)
	if len(targets) == 0 {
		return fmt.Errorf("found neither Claude Code nor Codex")
	}
	for _, t := range targets {
		changed, err := fn(t)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Path, err)
		}
		verb := already
		if changed {
			verb = did
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The %q MCP server is %s %s.\n", machines.ServerName, verb, t.Path)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), after)
	return nil
}

func newMachinesBuildCmd() *cobra.Command {
	var contextDir string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the machine image (machine_start builds it the first time otherwise)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if contextDir != "" {
				if err := machines.WriteContext(contextDir); err != nil {
					return err
				}
				args := machines.BuildArgs()
				keys := make([]string, 0, len(args))
				for k := range args {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote the build context to %s. Build it with:\n  docker build", contextDir)
				for _, k := range keys {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), " --build-arg %s=%s", k, args[k])
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), " -t %s %s\n", machines.ImageTag(), contextDir)
				return nil
			}
			d, err := machines.DetectDocker(cmd.Context())
			if err != nil {
				return err
			}
			tag := machines.ImageTag()
			if err := d.Build(cmd.Context(), tag, func(line string) { _, _ = fmt.Fprintln(cmd.ErrOrStderr(), line) }); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Built %s with %s.\n", tag, d.Name())
			return nil
		},
	}
	cmd.Flags().StringVar(&contextDir, "context", "", "write the build context into this directory instead of building")
	return cmd
}

func newMachinesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the machines, running or stopped",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := machines.DetectDocker(cmd.Context())
			if err != nil {
				return err
			}
			list, err := d.List(cmd.Context())
			if err != nil {
				return err
			}
			if len(list) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No machines.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "WORKTREE\tSTATE\tNAME")
			for _, st := range list {
				state := "stopped"
				if st.Running {
					state = "running"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", st.Worktree, state, st.Name)
			}
			return w.Flush()
		},
	}
}

func newMachinesRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm [worktree]",
		Short: "Delete a worktree's machine (this directory's by default), with what its Docker inside kept",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			dir, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			worktree, err := machines.Worktree(dir)
			if err != nil {
				return err
			}
			d, err := machines.DetectDocker(cmd.Context())
			if err != nil {
				return err
			}
			if err := d.Remove(cmd.Context(), worktree); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s's machine.\n", worktree)
			return nil
		},
	}
}

// machinesDefaultPort is serve's port unless --port says otherwise; taken,
// it falls back to any free one.
const machinesDefaultPort = 7790

func newMachinesServeCmd(a *app) *cobra.Command {
	var port int
	var openIt bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Browse every screenshot and recording in a web page on 127.0.0.1",
		Long: `Serves a page on 127.0.0.1 to browse every screenshot and recording on this machine: those
taken with agentbox machines mcp, and your AgentBox agents' media when AgentBox is running.
Group and filter them by repository, branch and session, kind and date, search their
captions, and open one to play, copy its path, download or delete it. New ones appear as
they're taken.

It listens on port ` + strconv.Itoa(machinesDefaultPort) + ` unless --port names another, or on any free port when that one is
taken, and prints the address. Thumbnails are made once and kept in <data>/machines/thumbs;
recordings get a poster frame when ffmpeg is installed.`,
		Example: `  agentbox machines serve --open
  agentbox machines serve --port 8080`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ln, err := listenMachines(port, cmd.Flags().Changed("port"))
			if err != nil {
				return err
			}
			store := machinesmedia.Open(a.paths)
			// Never started for this: on a front end, starting the daemon
			// would boot the VM. Not running only means no agents' media.
			srv := machinesweb.New(store, filepath.Join(a.paths.Data, "machines", "thumbs"),
				machinesweb.APIDaemon{Client: api.NewClient(a.paths.Socket())})
			srv.Logf = func(format string, args ...any) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
			}
			ctx := cmd.Context()
			go srv.Run(ctx)

			url := "http://" + ln.Addr().String() + "/"
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Serving screenshots and recordings at %s\n", url)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media from %s. Ctrl+C to stop.\n", store.Dir)
			if openIt {
				openInBrowser(url)
			}
			hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
			go func() {
				<-ctx.Done()
				sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = hs.Shutdown(sctx)
			}()
			if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", machinesDefaultPort, "the port to listen on, on 127.0.0.1")
	cmd.Flags().BoolVar(&openIt, "open", false, "open the page in your browser")
	return cmd
}

// listenMachines listens on 127.0.0.1:port. A default port that's taken
// (another serve, or anything else) gives way to a free one; a port asked for
// with --port doesn't.
func listenMachines(port int, explicit bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err == nil || explicit || !errors.Is(err, syscall.EADDRINUSE) {
		return ln, err
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

// openInBrowser opens url in the desktop's browser, best effort: the
// printed address is the answer on a machine with none.
func openInBrowser(url string) {
	name := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name = "explorer"
	}
	if b := os.Getenv("BROWSER"); b != "" && runtime.GOOS == "linux" {
		if _, err := exec.LookPath("xdg-open"); err != nil {
			name = b
		}
	}
	c := exec.Command(name, url)
	if c.Start() == nil {
		go func() { _ = c.Wait() }()
	}
}

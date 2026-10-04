package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/machinesmedia"
	"agentbox/internal/machinesweb"
)

// RunsOnFrontEnd reports whether a command line (os.Args[1:]) is one the
// VM's front end runs itself rather than forwards into the VM: `agentbox
// machines …` is for AI tools running on this machine, outside AgentBox, and
// its media and its web page are this machine's.
func RunsOnFrontEnd(args []string) bool {
	return len(args) > 0 && args[0] == "machines"
}

func newMachinesCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "machines",
		Short: "Screenshots and recordings for AI tools running on this machine, outside AgentBox",
		Long: `For the AI tools you run yourself, on this machine rather than in an AgentBox agent: their
screenshots and recordings go to one store, ` + "`<data>/machines/media`" + `, and agentbox machines serve
shows them beside your AgentBox agents' media.`,
	}
	cmd.AddCommand(newMachinesServeCmd(a))
	return cmd
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

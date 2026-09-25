package cli

import (
	"errors"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/hostwsl"
)

// newWSLBridgeCmd is the distro's end of the Windows app's relay to the daemon
// (package hostwsl, D94). The front end runs it through wsl.exe; nobody else
// has a reason to.
func newWSLBridgeCmd(a *app) *cobra.Command {
	var wait time.Duration
	cmd := &cobra.Command{
		Use:    "wsl-bridge",
		Short:  "Carry the Windows front end's connections to the daemon's socket",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			socket := a.paths.Socket()
			if cmd.Flags().Changed("wait") {
				// Whether the daemon answers, waiting up to wait for it.
				c := api.NewClient(socket)
				deadline := time.Now().Add(wait)
				for {
					if c.Ping(cmd.Context()) == nil {
						return nil
					}
					if time.Now().After(deadline) {
						return errors.New("the daemon isn't answering on " + socket)
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
			return hostwsl.Bridge(stdioConn{os.Stdin, os.Stdout}, socket)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 0, "only report whether the daemon answers, waiting up to this long")
	return cmd
}

type stdioConn struct {
	io.Reader
	io.WriteCloser
}

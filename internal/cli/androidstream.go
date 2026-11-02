package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/android/emulator"
	"agentbox/internal/devicestream"
)

// These run inside an agent, started by android.sh, on the agent's own
// emulator: the stream the app shows it with, and recordings.

func newAndroidServeCmd() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:    "serve",
		Short:  "Serve the emulator's screen as video, and input into it (internal)",
		Hidden: true, // started by android.sh
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ln, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			logger := log.New(cmd.ErrOrStderr(), "", log.LstdFlags)
			srv := &http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Found for each viewer: the emulator may have restarted.
					c, err := findEmulator(r.Context())
					if err != nil {
						http.Error(w, err.Error(), http.StatusServiceUnavailable)
						return
					}
					defer c.Close()
					devicestream.Serve(w, r, emulator.NewDevice(c), logger.Printf)
				}),
				ReadHeaderTimeout: 10 * time.Second,
			}
			go func() {
				<-cmd.Context().Done()
				_ = srv.Close()
			}()
			if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:5902", "where to listen")
	return cmd
}

func newAndroidCaptureCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "capture <file.mp4> <seconds>",
		Short:  "Record the emulator's screen to a file (internal)",
		Hidden: true, // run by android.sh record; stopped with SIGTERM
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			seconds, err := strconv.Atoi(args[1])
			if err != nil || seconds <= 0 {
				return fmt.Errorf("%q isn't a number of seconds", args[1])
			}
			c, err := findEmulator(cmd.Context())
			if err != nil {
				return err
			}
			defer c.Close()
			return devicestream.Record(cmd.Context(), emulator.NewDevice(c), args[0], time.Duration(seconds)*time.Second)
		},
	}
}

// findEmulator finds the agent's emulator's gRPC endpoint, waiting a little
// for one that is starting.
func findEmulator(ctx context.Context) (*emulator.Client, error) {
	dirs := emulator.RunningDirs(os.Getenv, os.Getuid())
	for i := 0; ; i++ {
		ep, err := emulator.Find(dirs, "")
		if err == nil {
			return emulator.New(ep), nil
		}
		if i == 20 {
			return nil, fmt.Errorf("the emulator isn't running: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

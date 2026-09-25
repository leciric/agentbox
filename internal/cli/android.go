package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// The android commands work on the host, on the agent they name, and inside an
// agent, on its own emulator. Screenshots, recordings and logs go to its media.
func newAndroidCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "android",
		Short: "An agent's own Android emulator, which you can watch and use in the app",
	}
	record := &cobra.Command{Use: "record", Short: "Record the emulator's screen, kept in the agent's media"}
	record.AddCommand(newAndroidRecordStartCmd(a), newRecordStopCmd(a), newRecordStatusCmd(a))
	cmd.AddCommand(
		newAndroidStatusCmd(a),
		newAndroidStartCmd(a),
		newAndroidStopCmd(a),
		newAndroidInstallCmd(a),
		newAndroidScreenshotCmd(a),
		record,
		newAndroidLogsCmd(a),
	)
	return cmd
}

func printAndroid(cmd *cobra.Command, st api.AndroidStatus) {
	out := cmd.OutOrStdout()
	switch {
	case st.Booted:
		fmt.Fprintf(out, "The emulator is running: %s\n", st.Device)
	case st.Running:
		fmt.Fprintln(out, "The emulator is starting")
	default:
		fmt.Fprintln(out, "The emulator isn't running")
	}
	if !st.Available {
		fmt.Fprintf(out, "This machine can't run emulators yet: %s\n", st.Problem)
	}
}

func newAndroidStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status [agent]",
		Short: "Show whether the emulator is running",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.Android(cmd.Context(), ref)
			if err != nil {
				return err
			}
			printAndroid(cmd, status)
			return nil
		},
	}
}

func newAndroidStartCmd(a *app) *cobra.Command {
	var req api.AndroidStartRequest
	cmd := &cobra.Command{
		Use:   "start [agent]",
		Short: "Start the emulator and wait until Android has booted",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Starting the emulator; Android takes about 20 seconds to boot…")
			status, err := c.StartAndroid(cmd.Context(), ref, req)
			if err != nil {
				return err
			}
			printAndroid(cmd, status)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Image, "image", "", `a system image, like "system-images;android-35;google_apis;x86_64" (default: the newest installed)`)
	f.IntVar(&req.MemoryMB, "memory", 2048, "the emulator's memory, in MB")
	f.IntVar(&req.Cores, "cores", 4, "the emulator's CPU cores")
	return cmd
}

func newAndroidStopCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [agent]",
		Short: "Stop the emulator (the app's data stays)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.StopAndroid(cmd.Context(), ref)
			if err != nil {
				return err
			}
			printAndroid(cmd, status)
			return nil
		},
	}
}

func newAndroidInstallCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "install [agent] <apk>",
		Short: "Install an APK from the agent's machine on its emulator",
		Long: `Install an APK on the emulator. The APK is a file on the agent's machine: inside an agent, relative
paths are relative to the current directory; on the host, to the agent's worktree.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) != 1 {
				return errors.New("usage: agentbox android install [agent] <apk>")
			}
			path := rest[0]
			if ref == "" && !filepath.IsAbs(path) {
				abs, err := filepath.Abs(path)
				if err != nil {
					return err
				}
				path = abs
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			result, err := c.InstallAPK(cmd.Context(), ref, api.AndroidInstallRequest{Path: path})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), result.Output)
			return nil
		},
	}
}

func newAndroidScreenshotCmd(a *app) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "screenshot [agent]",
		Short: "Take a screenshot of the emulator's screen",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.Screenshot(cmd.Context(), ref, api.ScreenshotRequest{Target: "android", Name: name})
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", `a name, like "medication-list"`)
	return cmd
}

func newAndroidRecordStartCmd(a *app) *cobra.Command {
	var name string
	var limit time.Duration
	cmd := &cobra.Command{
		Use:   "start [agent]",
		Short: "Start recording the emulator's screen",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			status, err := c.StartRecording(cmd.Context(), ref, api.RecordRequest{Target: "android", Name: name, LimitSeconds: int(limit.Seconds())})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recording %q. Run agentbox android record stop when you're done (it stops by itself after %s)\n",
				status.Name, time.Duration(status.LimitSeconds)*time.Second)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", `a name, like "create-medication"`)
	cmd.Flags().DurationVar(&limit, "limit", 10*time.Minute, "stop by itself after this long (at most 1h)")
	return cmd
}

func newAndroidLogsCmd(a *app) *cobra.Command {
	req := api.LogsRequest{Android: true}
	cmd := &cobra.Command{
		Use:   "logs [agent]",
		Short: "Keep the emulator's recent logcat, or one app's",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.AddLogs(cmd.Context(), ref, req)
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Package, "package", "", "only this app's lines, like com.example.pawly")
	f.StringVar(&req.Since, "since", "10m", "how far back")
	f.StringVar(&req.Name, "name", "", "a name")
	return cmd
}

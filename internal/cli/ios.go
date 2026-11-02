package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
)

// The ios commands drive the iOS Simulator, which runs on the Mac that
// AgentBox's VM runs on (internal/iossim): on the host, as the agent they
// name, and inside an agent, as itself. The simulators are the Mac's, shared
// by every agent; each agent has the one it opened last, which the commands
// use unless --device says another.
func newIOSCmd(a *app) *cobra.Command {
	var device string
	cmd := &cobra.Command{
		Use:   "ios",
		Short: "The iOS Simulator on the Mac, which you can watch and use in the app",
		Long: `The iOS Simulator runs on the Mac, beside AgentBox's VM, with Xcode. Open one, then tap, swipe and
type on it; coordinates are pixels of its screenshots. Install an app built for the simulator (a .app
from an iphonesimulator build in the worktree) and launch it by its bundle ID.`,
	}
	cmd.PersistentFlags().StringVar(&device, "device", "", `a simulator, by name ("iPhone 17 Pro") or UDID (default: the agent's own, or the one booted)`)
	cmd.AddCommand(
		newIOSListCmd(a),
		newIOSOpenCmd(a, &device, true),
		newIOSOpenCmd(a, &device, false),
		newIOSScreenshotCmd(a),
		newIOSTapCmd(a, &device),
		newIOSSwipeCmd(a, &device),
		newIOSTypeCmd(a, &device),
		newIOSKeyCmd(a, &device),
		newIOSButtonCmd(a, &device),
		newIOSInstallCmd(a, &device),
		newIOSLaunchCmd(a, &device),
		newIOSOpenURLCmd(a, &device),
	)
	return cmd
}

func printIOS(cmd *cobra.Command, st api.IOSStatus) {
	out := cmd.OutOrStdout()
	if !st.Available {
		_, _ = fmt.Fprintf(out, "The iOS Simulator is unavailable: %s\n", st.Problem)
		return
	}
	_, _ = fmt.Fprintf(out, "%s on the Mac\n", st.Xcode)
	for _, d := range st.Devices {
		mark := " "
		if d.UDID == st.Device {
			mark = "*"
		}
		_, _ = fmt.Fprintf(out, "%s %-28s %-10s %-9s %s\n", mark, d.Name, d.Runtime, d.State, d.UDID)
	}
	if st.Device != "" {
		_, _ = fmt.Fprintln(out, "* is this agent's simulator")
	}
}

func newIOSListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "list [agent]",
		Aliases: []string{"status"},
		Short:   "List the Mac's simulators",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			st, err := c.IOS(cmd.Context(), ref)
			if err != nil {
				return err
			}
			printIOS(cmd, st)
			return nil
		},
	}
}

// newIOSOpenCmd is `ios open`, or with open false `ios close`.
func newIOSOpenCmd(a *app, device *string, open bool) *cobra.Command {
	use, short := "open [agent]", "Boot a simulator, wait until it has, and make it the agent's"
	if !open {
		use, short = "close [agent]", "Shut a simulator down"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			req := api.IOSDeviceRequest{Device: *device}
			var st api.IOSStatus
			if open {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Booting the simulator; the first boot takes a minute…")
				st, err = c.OpenIOS(cmd.Context(), ref, req)
			} else {
				st, err = c.CloseIOS(cmd.Context(), ref, req)
			}
			if err != nil {
				return err
			}
			printIOS(cmd, st)
			return nil
		},
	}
}

func newIOSScreenshotCmd(a *app) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "screenshot [agent]",
		Short: "Take a screenshot of the agent's simulator, kept in its media",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.Screenshot(cmd.Context(), ref, api.ScreenshotRequest{Target: "ios", Name: name})
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", `a name, like "login-screen"`)
	return cmd
}

// iosInput sends one input to an agent's simulator: ref's, or inside an
// agent its own.
func iosInput(a *app, cmd *cobra.Command, ref string, in api.IOSInput) error {
	c, err := a.scopedClient(cmd, ref)
	if err != nil {
		return err
	}
	return c.IOSInput(cmd.Context(), ref, in)
}

func parseFloats(args []string) ([]float64, error) {
	out := make([]float64, len(args))
	for i, s := range args {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("%q isn't a number", s)
		}
		out[i] = f
	}
	return out, nil
}

func newIOSTapCmd(a *app, device *string) *cobra.Command {
	var fraction bool
	var hold time.Duration
	cmd := &cobra.Command{
		Use:   "tap [agent] <x> <y>",
		Short: "Tap the screen at a point, in pixels of its screenshots",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 2)
			if len(rest) != 2 {
				return errors.New("usage: agentbox ios tap [agent] <x> <y>")
			}
			xy, err := parseFloats(rest)
			if err != nil {
				return err
			}
			return iosInput(a, cmd, ref, api.IOSInput{Device: *device, Type: "tap", X: xy[0], Y: xy[1], Pixels: !fraction, DurationMS: int(hold.Milliseconds())})
		},
	}
	cmd.Flags().BoolVar(&fraction, "fraction", false, "x and y are fractions of the screen's width and height (0 to 1)")
	cmd.Flags().DurationVar(&hold, "hold", 0, "how long to hold, for a long press (like 1s)")
	return cmd
}

func newIOSSwipeCmd(a *app, device *string) *cobra.Command {
	var fraction bool
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "swipe [agent] <x1> <y1> <x2> <y2>",
		Short: "Swipe from one point to another, in pixels of the screenshots",
		Args:  cobra.RangeArgs(4, 5),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 4)
			if len(rest) != 4 {
				return errors.New("usage: agentbox ios swipe [agent] <x1> <y1> <x2> <y2>")
			}
			p, err := parseFloats(rest)
			if err != nil {
				return err
			}
			return iosInput(a, cmd, ref, api.IOSInput{
				Device: *device, Type: "swipe", X: p[0], Y: p[1], X2: p[2], Y2: p[3],
				Pixels: !fraction, DurationMS: int(duration.Milliseconds()),
			})
		},
	}
	cmd.Flags().BoolVar(&fraction, "fraction", false, "the points are fractions of the screen's width and height (0 to 1)")
	cmd.Flags().DurationVar(&duration, "duration", 300*time.Millisecond, "how long the swipe takes")
	return cmd
}

func newIOSTypeCmd(a *app, device *string) *cobra.Command {
	return &cobra.Command{
		Use:   "type [agent] <text>",
		Short: "Type text into whatever has the focus (tap a field first)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			return iosInput(a, cmd, ref, api.IOSInput{Device: *device, Type: "text", Text: strings.Join(rest, " ")})
		},
	}
}

func newIOSKeyCmd(a *app, device *string) *cobra.Command {
	var modifiers []string
	cmd := &cobra.Command{
		Use:   "key [agent] <key>",
		Short: "Press a key: return, escape, backspace, tab, space, up, down, left, right, or a letter",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) != 1 {
				return errors.New("usage: agentbox ios key [agent] <key>")
			}
			return iosInput(a, cmd, ref, api.IOSInput{Device: *device, Type: "key", Key: rest[0], Modifiers: modifiers})
		},
	}
	cmd.Flags().StringSliceVar(&modifiers, "with", nil, "modifiers to hold: shift, command, option, control")
	return cmd
}

func newIOSButtonCmd(a *app, device *string) *cobra.Command {
	var hold time.Duration
	cmd := &cobra.Command{
		Use:   "button [agent] <home|lock|siri|volumeUp|volumeDown>",
		Short: "Press one of the device's buttons",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) != 1 {
				return errors.New("usage: agentbox ios button [agent] <button>")
			}
			return iosInput(a, cmd, ref, api.IOSInput{Device: *device, Type: "button", Button: rest[0], DurationMS: int(hold.Milliseconds())})
		},
	}
	cmd.Flags().DurationVar(&hold, "hold", 0, "how long to hold it (like 2s)")
	return cmd
}

func newIOSInstallCmd(a *app, device *string) *cobra.Command {
	return &cobra.Command{
		Use:   "install [agent] <app>",
		Short: "Install an app built for the simulator: a .app directory in the agent's worktree",
		Long: `Install an app on the simulator. It's the .app directory of a build for the simulator (an
iphonesimulator SDK build), in the agent's worktree: inside an agent, relative paths are relative to
the current directory; on the host, to the agent's worktree.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) != 1 {
				return errors.New("usage: agentbox ios install [agent] <app>")
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
			result, err := c.InstallIOSApp(cmd.Context(), ref, api.IOSInstallRequest{Device: *device, Path: path})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
			return nil
		},
	}
}

func newIOSLaunchCmd(a *app, device *string) *cobra.Command {
	var stop bool
	cmd := &cobra.Command{
		Use:   "launch [agent] <bundle-id> [-- args…]",
		Short: "Launch an installed app by its bundle ID, restarting it if it runs",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) < 1 {
				return errors.New("usage: agentbox ios launch [agent] <bundle-id>")
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			result, err := c.LaunchIOSApp(cmd.Context(), ref, api.IOSLaunchRequest{Device: *device, BundleID: rest[0], Args: rest[1:], Terminate: stop})
			if err != nil {
				return err
			}
			if result.Output != "" {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stop, "stop", false, "stop the app instead")
	return cmd
}

func newIOSOpenURLCmd(a *app, device *string) *cobra.Command {
	return &cobra.Command{
		Use:   "openurl [agent] <url>",
		Short: "Open a URL: a page in Safari, or a link into an app",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			if len(rest) != 1 {
				return errors.New("usage: agentbox ios openurl [agent] <url>")
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			return c.OpenIOSURL(cmd.Context(), ref, api.IOSOpenURLRequest{Device: *device, URL: rest[0]})
		},
	}
}

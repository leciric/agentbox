package cli

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/image/draw"

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
		Long: `The iOS Simulator runs on the Mac, beside AgentBox's VM, with Xcode. Open one, read its screen
with ios ui, and act on what it lists: ios tap, ios type --into, ios scroll-to, ios wait. Each action
says what it changed. Targets are id:<testID> or a label, quoted. Screenshots are for judging how it
looks. Install an app built for the simulator (a .app from an iphonesimulator build in the worktree)
and launch it by its bundle ID; a development build connects to Metro with ios openurl (ios apps).`,
	}
	cmd.PersistentFlags().StringVar(&device, "device", "", `a simulator, by name ("iPhone 17 Pro") or UDID (default: the agent's own, or the one booted)`)
	cmd.AddCommand(
		newIOSListCmd(a),
		newIOSOpenCmd(a, &device, true),
		newIOSOpenCmd(a, &device, false),
		newIOSUICmd(a, &device),
		newIOSScreenshotCmd(a),
		newIOSTapCmd(a, &device),
		newIOSSwipeCmd(a, &device),
		newIOSTypeCmd(a, &device),
		newIOSKeyCmd(a, &device),
		newIOSButtonCmd(a, &device),
		newIOSInstallCmd(a, &device),
		newIOSLaunchCmd(a, &device),
		newIOSOpenURLCmd(a, &device),
		newIOSScrollToCmd(a, &device),
		newIOSWaitCmd(a, &device),
		newIOSAppsCmd(a, &device),
		newIOSForwardCmd(a),
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
	var pair pairFlags
	cmd := &cobra.Command{
		Use:   "screenshot [agent]",
		Short: "Take a screenshot of the agent's simulator, kept in its media",
		Long: `Take a screenshot of the simulator, kept in the agent's media. Inside an agent it also writes a
copy 600 pixels wide to look at, and prints its path.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			item, err := c.Screenshot(cmd.Context(), ref, api.ScreenshotRequest{Target: "ios", Name: name, Pair: pair.pair, Side: pair.side()})
			if err != nil {
				return err
			}
			printSaved(cmd, item)
			if ref == "" {
				body, err := c.MediaFile(cmd.Context(), "", item.ID)
				if err != nil {
					return fmt.Errorf("saved in media, but not the copy to look at: %w", err)
				}
				defer func() { _ = body.Close() }()
				path, size, err := saveIOSLook(body, cmp.Or(item.Name, item.ID))
				if err != nil {
					return fmt.Errorf("saved in media, but not the copy to look at: %w", err)
				}
				printIOSLook(cmd, path, size, "")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", `a name, like "login-screen"`)
	pair.add(cmd)
	return cmd
}

// iosTarget splits an action's arguments into the agent, if named first, and
// the rest: an agent goes first only when the rest still makes a command.
func iosTarget(args []string, valid func(rest []string) bool) (string, []string) {
	if len(args) > 1 && refPattern.MatchString(args[0]) && valid(args[1:]) {
		return args[0], args[1:]
	}
	return "", args
}

// iosUI sends a screen request and prints what it says.
func iosUI(a *app, cmd *cobra.Command, ref string, req api.IOSUIRequest) error {
	c, err := a.scopedClient(cmd, ref)
	if err != nil {
		return err
	}
	out, err := c.IOSUI(cmd.Context(), ref, req)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), out.Text)
	if len(out.Look) > 0 && ref == "" {
		path, size, err := saveIOSLook(bytes.NewReader(out.Look), "nothing-changed")
		if err != nil {
			return fmt.Errorf("couldn't save the screenshot to look at: %w", err)
		}
		printIOSLook(cmd, path, size, req.Device)
	}
	return nil
}

func printIOSLook(cmd *cobra.Command, path string, size image.Point, device string) {
	if device != "" {
		device = fmt.Sprintf(" --device %q", device)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "To look at it: %s (%d×%d; to tap a point in it: agentbox ios tap <x÷%d> <y÷%d> --fraction%s)\n", path, size.X, size.Y, size.X, size.Y, device)
}

func newIOSUICmd(a *app, device *string) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "ui [agent]",
		Short: "Read the screen: what's on it, from the app's accessibility, one element a line",
		Long: `Read the simulator's screen as text: each element on it with its kind, label, value and id
(React Native's testID), and whether a tap reaches it ("covered by …", "may be hidden"). What's off
the screen is summed up by direction. Act on what it lists by id:<id> or by label: ios tap, ios type
--into, ios scroll-to, ios wait. A screen with little in it (a canvas, a web view, a game) says to use
ios screenshot instead.`,
		Example: `  agentbox ios ui
  agentbox ios ui --all     # every element, off the screen too`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return iosUI(a, cmd, optionalRef(args), api.IOSUIRequest{Device: *device, All: all})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every element, off the screen too")
	return cmd
}

func newIOSScrollToCmd(a *app, device *string) *cobra.Command {
	var near string
	cmd := &cobra.Command{
		Use:   "scroll-to [agent] <target>",
		Short: "Scroll until an element is on the screen: id:<id> or its label",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := iosTarget(args, func(rest []string) bool { return len(rest) == 1 })
			if len(rest) != 1 {
				return errors.New("usage: agentbox ios scroll-to [agent] <id:… or label>")
			}
			return iosUI(a, cmd, ref, api.IOSUIRequest{Device: *device, Action: "scroll-to", Target: rest[0], Near: near})
		},
	}
	cmd.Flags().StringVar(&near, "near", "", "among elements with the same label, the one nearest this text")
	return cmd
}

func newIOSWaitCmd(a *app, device *string) *cobra.Command {
	var text, id string
	var gone bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait [agent] (--text <text> | --id <id>)",
		Short: "Wait until an element is on the screen, or with --gone isn't",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := text
			if id != "" {
				target = "id:" + id
			}
			if (text == "") == (id == "") {
				return errors.New("say what to wait for: --text or --id")
			}
			return iosUI(a, cmd, optionalRef(args), api.IOSUIRequest{Device: *device, Action: "wait", Target: target, Gone: gone, TimeoutMS: int(timeout.Milliseconds())})
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "an element whose label has this text")
	cmd.Flags().StringVar(&id, "id", "", "an element with this id (testID)")
	cmd.Flags().BoolVar(&gone, "gone", false, "wait until it's no longer on the screen")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "how long to wait (at most 1m)")
	return cmd
}

func newIOSAppsCmd(a *app, device *string) *cobra.Command {
	return &cobra.Command{
		Use:   "apps [agent]",
		Short: "List the apps installed on the simulator; a development build shows how to open it on Metro",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := optionalRef(args)
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			apps, err := c.IOSApps(cmd.Context(), ref, api.IOSDeviceRequest{Device: *device})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			openurl := "agentbox ios openurl"
			if ref != "" {
				openurl += " " + ref
			}
			if *device != "" {
				openurl += fmt.Sprintf(" --device %q", *device)
			}
			if len(apps) == 0 {
				_, _ = fmt.Fprintln(out, "No apps of yours are installed on this simulator")
			}
			for _, app := range apps {
				_, _ = fmt.Fprintf(out, "%-40s %s\n", app.BundleID, app.Name)
				if app.DevClient && app.OpenURL == "" {
					_, _ = fmt.Fprintln(out, "  a development build: ios launch only opens its launcher, where Enter URL manually takes http://localhost:<Metro's port>")
				} else if app.DevClient {
					_, _ = fmt.Fprintf(out, "  a development build: ios launch only opens its launcher; open it on your Metro (<port>: its port) with\n  %s %q\n", openurl, app.OpenURL)
				}
			}
			return nil
		},
	}
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
	var near string
	var force bool
	cmd := &cobra.Command{
		Use:   "tap [agent] (<target> | <x> <y>)",
		Short: "Tap an element (id:<id> or its label, from ios ui), or a point in pixels of the screenshots",
		Long: `Tap an element that ios ui lists, by id:<id> or by its label, and say what changed on the screen.
An element off the screen, covered or disabled is refused, with what to do instead. Or tap a point,
in pixels of the screenshots.`,
		Example: `  agentbox ios tap id:profile-save
  agentbox ios tap 'Sign out'
  agentbox ios tap Follow --near 'Ana Souza'
  agentbox ios tap 600 1200`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := iosTarget(args, func(rest []string) bool { return len(rest) == 1 || len(rest) == 2 })
			if len(rest) == 2 {
				xy, err := parseFloats(rest)
				if err != nil {
					return fmt.Errorf("%w; to tap an element whose label has spaces, quote it", err)
				}
				return iosInput(a, cmd, ref, api.IOSInput{Device: *device, Type: "tap", X: xy[0], Y: xy[1], Pixels: !fraction, DurationMS: int(hold.Milliseconds())})
			}
			if len(rest) != 1 {
				return errors.New("usage: agentbox ios tap [agent] <id:… or label>, or agentbox ios tap [agent] <x> <y>; quote a label with spaces")
			}
			if fraction || hold > 0 {
				return errors.New("--fraction and --hold are for a point: tap x y")
			}
			return iosUI(a, cmd, ref, api.IOSUIRequest{Device: *device, Action: "tap", Target: rest[0], Near: near, Force: force})
		},
	}
	cmd.Flags().StringVar(&near, "near", "", "among elements with the same label, the one nearest this text")
	cmd.Flags().BoolVar(&force, "force", false, "tap even when the hit-test can't confirm the tap lands on it")
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
	var into, near string
	cmd := &cobra.Command{
		Use:   "type [agent] <text>",
		Short: "Type text into a field (--into id:<id> or its label), or whatever has the focus",
		Example: `  agentbox ios type 'hello there' --into id:profile-bio-input
  agentbox ios type 'https://example.com' --into 'My LinkedIn'`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, rest := splitRef(args, 1)
			text := strings.Join(rest, " ")
			if into == "" {
				return iosInput(a, cmd, ref, api.IOSInput{Device: *device, Type: "text", Text: text})
			}
			return iosUI(a, cmd, ref, api.IOSUIRequest{Device: *device, Action: "type", Text: text, Target: into, Near: near})
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "the text field to type into, tapped first, at the end of its text: id:<id> or its label")
	cmd.Flags().StringVar(&near, "near", "", "among fields with the same label, the one nearest this text")
	return cmd
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

func newIOSForwardCmd(a *app) *cobra.Command {
	var stop bool
	cmd := &cobra.Command{
		Use:   "forward [agent] [port…]",
		Short: "Make the agent's ports reach the Mac's localhost, where the simulator's apps open them",
		Long: `An app in the simulator can't reach the agent's machine: forward its ports, and the app reaches
them at http://localhost:<port> on the Mac, the same port numbers. A dev build connects to the agent's
Metro this way, and the app to its API. With no ports, it lists the forwards; --stop gives them back
(all of the agent's, with no ports). A port is one agent's while it runs: another agent takes it
only once that one has stopped. Forwards end when the Mac restarts the simulator helper, as an
AgentBox update does: forward again.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) > 0 && refPattern.MatchString(args[0]) {
				ref, args = args[0], args[1:]
			}
			req := api.IOSForwardRequest{Stop: stop}
			for _, arg := range args {
				port, err := strconv.Atoi(arg)
				if err != nil {
					return fmt.Errorf("%q isn't a port", arg)
				}
				req.Ports = append(req.Ports, port)
			}
			c, err := a.scopedClient(cmd, ref)
			if err != nil {
				return err
			}
			// A port new to the project waits for the user to allow it.
			waiting := time.AfterFunc(2*time.Second, func() {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Waiting for the user to allow these ports in AgentBox (a request in the app)…")
			})
			forwards, err := c.ForwardIOS(cmd.Context(), ref, req)
			waiting.Stop()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(forwards) == 0 {
				_, _ = fmt.Fprintln(out, "No ports are forwarded to the Mac")
			}
			for _, f := range forwards {
				_, _ = fmt.Fprintf(out, "http://localhost:%d on the Mac → port %d of %s\n", f.Port, f.Port, f.Agent)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stop, "stop", false, "give the ports back")
	return cmd
}

// saveIOSLook writes a screenshot, 600 pixels wide, to the agent's
// temporary directory, to look at: small enough to read, and kept out of
// its media.
func saveIOSLook(r io.Reader, name string) (string, image.Point, error) {
	src, err := png.Decode(r)
	if err != nil {
		return "", image.Point{}, err
	}
	b := src.Bounds()
	w := min(600, b.Dx())
	h := max(1, b.Dy()*w/max(1, b.Dx()))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	dir := filepath.Join(os.TempDir(), "ios")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", image.Point{}, err
	}
	name = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '-'
	}, name)
	path := filepath.Join(dir, name+".png")
	f, err := os.Create(path)
	if err != nil {
		return "", image.Point{}, err
	}
	if err := png.Encode(f, dst); err != nil {
		_ = f.Close()
		return "", image.Point{}, err
	}
	return path, dst.Bounds().Size(), f.Close()
}

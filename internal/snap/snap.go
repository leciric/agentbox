// Package snap takes a SnapShot: a picture of the window the user is working
// in, with its app, its title and, where the desktop exposes it, a summary of
// its accessibility tree, to send to a project's chat or to an agent as a
// bug report.
//
// It runs on the user's machine (agentbox snap, which the desktop app's
// global shortcut also runs), never in the VM: the window is there. What a
// desktop gives cheaply decides how much a capture holds:
//
//   - Hyprland: the active window from hyprctl, cut out of the screen by grim.
//   - Sway: the focused window from swaymsg's tree, and grim.
//   - KDE Plasma: spectacle's active-window capture.
//   - GNOME: gnome-screenshot's window capture where GNOME still allows it.
//   - X11: xdotool's active window, captured by ImageMagick's import or maim.
//   - A Mac: the frontmost app and window title from System Events, and the
//     screen from screencapture.
//
// Anything else, or a window capture that fails, falls back to the whole
// screen (grim, gnome-screenshot, spectacle, import, screencapture). On
// Linux the AT-SPI tree of the window's app is added when the accessibility
// bus answers (accessibility.go).
package snap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decoding the captures
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Capture is one SnapShot.
type Capture struct {
	Image    []byte
	MimeType string // image/png or image/jpeg
	App      string // the window's app: its class, or the Mac's app name
	Title    string // the window's title
	PID      int    // the window's process, when the desktop says
	Desktop  string // hyprland, sway, kde, gnome, x11, mac or screen
	// Window is false when this is the whole screen: the desktop couldn't
	// give the window alone.
	Window bool
	// Accessibility is the window's accessibility tree, summarized; empty
	// when the desktop or the app exposes none.
	Accessibility string
	TakenAt       time.Time
}

// Env is what a capture runs against: the environment and the commands it
// may run. Tests replace both.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Run runs a command and returns its standard output.
	Run  func(ctx context.Context, name string, args ...string) ([]byte, error)
	GOOS string
	// TempDir is where tools that only write files write them.
	TempDir string
}

// System is the real environment.
func System(goos string) Env {
	return Env{
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			var stderr bytes.Buffer
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				if msg := strings.TrimSpace(stderr.String()); msg != "" {
					return out, fmt.Errorf("%s: %w: %s", name, err, lastLine(msg))
				}
				return out, fmt.Errorf("%s: %w", name, err)
			}
			return out, nil
		},
		GOOS:    goos,
		TempDir: os.TempDir(),
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// Desktop names the desktop a capture would go through.
func (e Env) Desktop() string {
	if e.GOOS == "darwin" {
		return "mac"
	}
	current := strings.ToLower(e.Getenv("XDG_CURRENT_DESKTOP"))
	switch {
	case e.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		return "hyprland"
	case e.Getenv("SWAYSOCK") != "":
		return "sway"
	case strings.Contains(current, "kde"):
		return "kde"
	case strings.Contains(current, "gnome"):
		return "gnome"
	case e.Getenv("WAYLAND_DISPLAY") == "" && e.Getenv("DISPLAY") != "":
		return "x11"
	}
	return "screen"
}

// Take captures the active window, or the whole screen when the desktop can't
// give the window alone. full asks for the whole screen anyway.
func Take(ctx context.Context, e Env, full bool) (Capture, error) {
	c := Capture{Desktop: e.Desktop(), TakenAt: time.Now()}
	var err error
	if !full {
		err = e.window(ctx, &c)
		if err == nil {
			c.Window = true
		}
	}
	if !c.Window {
		if serr := e.screen(ctx, &c); serr != nil {
			if err != nil {
				return c, fmt.Errorf("couldn't capture the window (%v) or the screen (%v)", err, serr)
			}
			return c, serr
		}
	}
	c.MimeType = "image/png"
	if e.GOOS == "linux" {
		c.Accessibility = Accessibility(ctx, c.PID, c.App, c.Title)
	}
	return c, nil
}

// window captures the active window on the desktops that can name it.
func (e Env) window(ctx context.Context, c *Capture) error {
	switch c.Desktop {
	case "hyprland":
		out, err := e.Run(ctx, "hyprctl", "-j", "activewindow")
		if err != nil {
			return err
		}
		w, err := parseHyprland(out)
		if err != nil {
			return err
		}
		c.App, c.Title, c.PID = w.app, w.title, w.pid
		c.Image, err = e.Run(ctx, "grim", "-g", w.geometry(), "-")
		return err
	case "sway":
		out, err := e.Run(ctx, "swaymsg", "-t", "get_tree")
		if err != nil {
			return err
		}
		w, err := parseSway(out)
		if err != nil {
			return err
		}
		c.App, c.Title, c.PID = w.app, w.title, w.pid
		c.Image, err = e.Run(ctx, "grim", "-g", w.geometry(), "-")
		return err
	case "kde":
		return e.toFile(ctx, c, "spectacle", "--background", "--nonotify", "--activewindow", "--output")
	case "gnome":
		return e.toFile(ctx, c, "gnome-screenshot", "--window", "--file")
	case "x11":
		id, err := e.Run(ctx, "xdotool", "getactivewindow")
		if err != nil {
			return err
		}
		win := strings.TrimSpace(string(id))
		if title, err := e.Run(ctx, "xdotool", "getwindowname", win); err == nil {
			c.Title = strings.TrimSpace(string(title))
		}
		if class, err := e.Run(ctx, "xdotool", "getwindowclassname", win); err == nil {
			c.App = strings.TrimSpace(string(class))
		} else if class, err := e.Run(ctx, "xprop", "-id", win, "WM_CLASS"); err == nil {
			// xdotool before 3.2021 has no getwindowclassname.
			c.App = wmClass(string(class))
		}
		if pid, err := e.Run(ctx, "xdotool", "getwindowpid", win); err == nil {
			c.PID, _ = strconv.Atoi(strings.TrimSpace(string(pid)))
		}
		if _, err := e.LookPath("import"); err == nil {
			c.Image, err = e.Run(ctx, "import", "-window", win, "png:-")
			return err
		}
		c.Image, err = e.Run(ctx, "maim", "-i", win)
		return err
	case "mac":
		// The window's bounds would need its CGWindowID, which nothing
		// cheap gives: the app and title come with the whole screen.
		if out, err := e.Run(ctx, "osascript", "-e", macFrontmost); err == nil {
			c.App, c.Title, _ = strings.Cut(strings.TrimSpace(string(out)), "\n")
		}
		return errors.New("a Mac captures the screen, with the frontmost window's app and title")
	}
	return fmt.Errorf("this desktop (%s) can't name its active window", c.Desktop)
}

const macFrontmost = `tell application "System Events"
	set p to first application process whose frontmost is true
	set t to ""
	try
		set t to name of front window of p
	end try
	return (name of p) & linefeed & t
end tell`

// screen captures the whole screen with the first tool this desktop has.
func (e Env) screen(ctx context.Context, c *Capture) error {
	type tool struct {
		name string
		args []string
		file bool // writes a file rather than standard output
	}
	var tools []tool
	if e.GOOS == "darwin" {
		tools = []tool{{"screencapture", []string{"-x", "-t", "png"}, true}}
	} else {
		tools = []tool{
			{"grim", []string{"-"}, false},
			{"gnome-screenshot", []string{"--file"}, true},
			{"spectacle", []string{"--background", "--nonotify", "--fullscreen", "--output"}, true},
			{"import", []string{"-window", "root", "png:-"}, false},
			{"maim", nil, false},
		}
		if c.Desktop == "x11" { // grim is Wayland's
			tools = tools[1:]
		}
	}
	var tried []string
	for _, t := range tools {
		if _, err := e.LookPath(t.name); err != nil {
			continue
		}
		var err error
		if t.file {
			err = e.toFile(ctx, c, t.name, t.args...)
		} else {
			c.Image, err = e.Run(ctx, t.name, t.args...)
		}
		if err == nil && len(c.Image) > 0 {
			return nil
		}
		tried = append(tried, fmt.Sprint(err))
	}
	if len(tried) == 0 {
		return errors.New("no screenshot tool found: install grim (Wayland), gnome-screenshot, spectacle, or ImageMagick (X11)")
	}
	return fmt.Errorf("the screen couldn't be captured: %s", strings.Join(tried, "; "))
}

// toFile runs a tool that writes its capture to the file named by its last
// argument, and reads that file.
func (e Env) toFile(ctx context.Context, c *Capture, name string, args ...string) error {
	f := filepath.Join(e.TempDir, fmt.Sprintf("agentbox-snap-%d.png", time.Now().UnixNano()))
	defer func() { _ = os.Remove(f) }()
	if _, err := e.Run(ctx, name, append(args, f)...); err != nil {
		return err
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return fmt.Errorf("%s wrote no picture", name)
	}
	c.Image = data
	return nil
}

type window struct {
	app, title string
	pid        int
	x, y, w, h int
}

func (w window) geometry() string { return fmt.Sprintf("%d,%d %dx%d", w.x, w.y, w.w, w.h) }

// parseHyprland reads `hyprctl -j activewindow`.
func parseHyprland(data []byte) (window, error) {
	var w struct {
		At    []int  `json:"at"`
		Size  []int  `json:"size"`
		Class string `json:"class"`
		Title string `json:"title"`
		PID   int    `json:"pid"`
	}
	if err := json.Unmarshal(data, &w); err != nil || len(w.At) != 2 || len(w.Size) != 2 {
		return window{}, errors.New("no active window on Hyprland")
	}
	if w.Size[0] <= 0 || w.Size[1] <= 0 {
		return window{}, errors.New("the active window on Hyprland has no size")
	}
	return window{app: w.Class, title: w.Title, pid: w.PID, x: w.At[0], y: w.At[1], w: w.Size[0], h: w.Size[1]}, nil
}

// parseSway finds the focused window in `swaymsg -t get_tree`.
func parseSway(data []byte) (window, error) {
	type node struct {
		Focused       bool                              `json:"focused"`
		Type          string                            `json:"type"`
		Name          string                            `json:"name"`
		PID           int                               `json:"pid"`
		AppID         string                            `json:"app_id"`
		Rect          struct{ X, Y, Width, Height int } `json:"rect"`
		WindowProps   struct{ Class string }            `json:"window_properties"`
		Nodes         []node                            `json:"nodes"`
		FloatingNodes []node                            `json:"floating_nodes"`
	}
	var root node
	if err := json.Unmarshal(data, &root); err != nil {
		return window{}, errors.New("sway's tree doesn't parse")
	}
	var find func(n node) *node
	find = func(n node) *node {
		if n.Focused && (n.Type == "con" || n.Type == "floating_con") {
			return &n
		}
		for _, kids := range [][]node{n.Nodes, n.FloatingNodes} {
			for _, k := range kids {
				if f := find(k); f != nil {
					return f
				}
			}
		}
		return nil
	}
	f := find(root)
	if f == nil {
		return window{}, errors.New("sway has no focused window")
	}
	app := f.AppID
	if app == "" {
		app = f.WindowProps.Class
	}
	return window{app: app, title: f.Name, pid: f.PID, x: f.Rect.X, y: f.Rect.Y, w: f.Rect.Width, h: f.Rect.Height}, nil
}

// Fit makes a capture's picture small enough to send in a chat: one under
// limit bytes stays as it is, a bigger one becomes a JPEG, scaled down until
// it fits.
func Fit(c *Capture, limit int) error {
	if len(c.Image) <= limit {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(c.Image))
	if err != nil {
		return fmt.Errorf("the capture isn't a picture: %w", err)
	}
	for scale := 1.0; scale > 0.1; scale *= 0.75 {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, shrink(img, scale), &jpeg.Options{Quality: 85}); err != nil {
			return err
		}
		if buf.Len() <= limit {
			c.Image, c.MimeType = buf.Bytes(), "image/jpeg"
			return nil
		}
	}
	return errors.New("the capture is too big to send, even scaled down")
}

// shrink scales an image by nearest neighbour: plenty for a screenshot read
// by a model, and no dependency.
func shrink(src image.Image, scale float64) image.Image {
	if scale >= 1 {
		return src
	}
	b := src.Bounds()
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		sy := b.Min.Y + y*b.Dy()/h
		for x := range w {
			dst.Set(x, y, src.At(b.Min.X+x*b.Dx()/w, sy))
		}
	}
	return dst
}

// wmClass is the class in xprop's WM_CLASS(STRING) = "instance", "Class".
func wmClass(out string) string {
	fields := strings.Split(out, "\"")
	if len(fields) < 3 {
		return ""
	}
	return fields[len(fields)-2]
}

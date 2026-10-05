package snap

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeEnv is a desktop with the given variables and tools, which records the
// commands a capture runs.
func fakeEnv(t *testing.T, vars map[string]string, tools map[string]func(args []string) ([]byte, error)) (Env, *[]string) {
	var ran []string
	return Env{
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(name string) (string, error) {
			if _, ok := tools[name]; ok {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			ran = append(ran, name+" "+strings.Join(args, " "))
			tool, ok := tools[name]
			if !ok {
				return nil, errors.New(name + ": not found")
			}
			return tool(args)
		},
		GOOS:    "linux",
		TempDir: t.TempDir(),
	}, &ran
}

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 13), uint8((x*31 + y*17) * (x ^ y)), 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// On Hyprland, the active window comes from hyprctl and grim cuts exactly its
// rectangle out of the screen.
func TestTakeHyprlandWindow(t *testing.T) {
	shot := pngBytes(4, 4)
	env, ran := fakeEnv(t, map[string]string{"HYPRLAND_INSTANCE_SIGNATURE": "abc", "WAYLAND_DISPLAY": "wayland-1"}, map[string]func([]string) ([]byte, error){
		"hyprctl": func([]string) ([]byte, error) {
			return []byte(`{"address":"0x1","at":[1930,40],"size":[1280,720],"class":"firefox","title":"Pull requests · GitHub","pid":4242}`), nil
		},
		"grim": func([]string) ([]byte, error) { return shot, nil },
	})
	c, err := Take(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Window || c.Desktop != "hyprland" || c.App != "firefox" || c.Title != "Pull requests · GitHub" || c.PID != 4242 || !bytes.Equal(c.Image, shot) {
		t.Errorf("capture = %+v", c)
	}
	if want := []string{"hyprctl -j activewindow", "grim -g 1930,40 1280x720 -"}; !slices.Equal(*ran, want) {
		t.Errorf("ran %q, want %q", *ran, want)
	}
}

// A desktop that can't name its window, or whose window capture fails,
// falls back to the whole screen with the first tool it has.
func TestTakeFallsBackToTheScreen(t *testing.T) {
	shot := pngBytes(2, 2)
	env, ran := fakeEnv(t, map[string]string{"HYPRLAND_INSTANCE_SIGNATURE": "abc"}, map[string]func([]string) ([]byte, error){
		"hyprctl": func([]string) ([]byte, error) { return []byte(`{}`), nil }, // no window focused
		"grim":    func([]string) ([]byte, error) { return shot, nil },
	})
	c, err := Take(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Window || !bytes.Equal(c.Image, shot) || (*ran)[len(*ran)-1] != "grim -" {
		t.Errorf("capture window=%v, ran %q", c.Window, *ran)
	}

	// GNOME's gnome-screenshot writes a file rather than standard output.
	env, _ = fakeEnv(t, map[string]string{"XDG_CURRENT_DESKTOP": "ubuntu:GNOME", "WAYLAND_DISPLAY": "wayland-0"}, map[string]func([]string) ([]byte, error){
		"gnome-screenshot": func(args []string) ([]byte, error) {
			if slices.Contains(args, "--window") {
				return nil, errors.New("not allowed")
			}
			return nil, os.WriteFile(args[len(args)-1], shot, 0o600)
		},
	})
	if c, err = Take(context.Background(), env, false); err != nil || c.Window || c.Desktop != "gnome" || !bytes.Equal(c.Image, shot) {
		t.Errorf("GNOME: %+v, %v", c, err)
	}

	env, _ = fakeEnv(t, map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, nil)
	if _, err := Take(context.Background(), env, true); err == nil || !strings.Contains(err.Error(), "install grim") {
		t.Errorf("no tool at all: %v", err)
	}
}

// On X11, xdotool names the window and ImageMagick's import captures it.
func TestTakeX11Window(t *testing.T) {
	shot := pngBytes(3, 3)
	env, _ := fakeEnv(t, map[string]string{"DISPLAY": ":0"}, map[string]func([]string) ([]byte, error){
		"xdotool": func(args []string) ([]byte, error) {
			switch args[0] {
			case "getactivewindow":
				return []byte("8388611\n"), nil
			case "getwindowname":
				return []byte("Terminal\n"), nil
			case "getwindowclassname":
				return []byte("xterm\n"), nil
			}
			return []byte("77\n"), nil
		},
		"import": func(args []string) ([]byte, error) {
			if args[1] != "8388611" {
				return nil, errors.New("wrong window")
			}
			return shot, nil
		},
	})
	c, err := Take(context.Background(), env, false)
	if err != nil || !c.Window || c.App != "xterm" || c.Title != "Terminal" || c.PID != 77 {
		t.Errorf("capture = %+v, %v", c, err)
	}
}

func TestWMClass(t *testing.T) {
	if got := wmClass(`WM_CLASS(STRING) = "agentbox-desktop", "AgentBox"` + "\n"); got != "AgentBox" {
		t.Errorf("wmClass = %q", got)
	}
	if got := wmClass("WM_CLASS:  not found.\n"); got != "" {
		t.Errorf("wmClass of nothing = %q", got)
	}
}

func TestParseSwayFindsTheFocusedWindow(t *testing.T) {
	w, err := parseSway([]byte(`{"type":"root","nodes":[{"type":"output","nodes":[{"type":"workspace","nodes":[
		{"type":"con","name":"vim","app_id":"foot","pid":1,"rect":{"x":0,"y":0,"width":10,"height":10}},
		{"type":"con","focused":true,"name":"Docs","pid":9,"window_properties":{"class":"Chromium"},"rect":{"x":5,"y":6,"width":700,"height":500}}]}]}]}`))
	if err != nil || w.app != "Chromium" || w.title != "Docs" || w.geometry() != "5,6 700x500" {
		t.Errorf("parseSway = %+v, %v", w, err)
	}
}

// A picture too big for a chat becomes a JPEG small enough.
func TestFit(t *testing.T) {
	c := Capture{Image: pngBytes(600, 400), MimeType: "image/png"}
	before := len(c.Image)
	if err := Fit(&c, before); err != nil || c.MimeType != "image/png" {
		t.Fatalf("a picture that fits changed: %v %s", err, c.MimeType)
	}
	if err := Fit(&c, before/4); err != nil {
		t.Fatal(err)
	}
	if c.MimeType != "image/jpeg" || len(c.Image) > before/4 {
		t.Errorf("fitted to %d bytes as %s, limit %d", len(c.Image), c.MimeType, before/4)
	}
}

type fakeNode struct {
	role, name, text string
	kids             []*fakeNode
}

func (n *fakeNode) Role() string { return n.role }
func (n *fakeNode) Name() string { return n.name }
func (n *fakeNode) Text() string { return n.text }
func (n *fakeNode) Children() []node {
	out := make([]node, len(n.kids))
	for i, k := range n.kids {
		out[i] = k
	}
	return out
}

func TestSummarizeFoldsUnnamedContainers(t *testing.T) {
	tree := &fakeNode{role: "frame", name: "Settings — AgentBox", kids: []*fakeNode{
		{role: "panel", kids: []*fakeNode{
			{role: "push button", name: "Save"},
			{role: "entry", name: "Project name", text: "pawly\n  stack"},
			{role: "label", name: "Same", text: "Same"},
		}},
		{role: "separator"},
		{role: "check box"},
		{role: "generic", kids: []*fakeNode{{role: "button", name: "Cancel", kids: []*fakeNode{{role: "label", name: "Cancel"}}}}},
	}}
	want := `frame "Settings — AgentBox"
  push button "Save"
  entry "Project name": pawly stack
  label "Same"
  check box
  button "Cancel"`
	if got := Summarize(tree); got != want {
		t.Errorf("Summarize =\n%s\nwant\n%s", got, want)
	}

	var wide []*fakeNode
	for range maxNodes + 10 {
		wide = append(wide, &fakeNode{role: "link", name: "x"})
	}
	if got := Summarize(&fakeNode{role: "document web", kids: wide}); !strings.HasSuffix(got, "(cut: the window has more)") {
		t.Errorf("a huge tree isn't cut: …%s", got[len(got)-40:])
	}
}

func TestReport(t *testing.T) {
	taken := time.Date(2026, 10, 5, 14, 3, 0, 0, time.UTC)
	got := Report("  The Save button does nothing.\n", "firefox", "Settings", "hyprland", true, "push button \"Save\"\n```evil", taken)
	want := "Bug report from a SnapShot of a window: firefox, \"Settings\".\n\nThe Save button does nothing.\n\n" +
		"Captured 2026-10-05 14:03 UTC on hyprland; the picture is attached. The window's accessibility tree, summarized (role \"name\": text):\n\n" +
		"```\npush button \"Save\"\n'''evil\n```"
	if got != want {
		t.Errorf("Report =\n%s\nwant\n%s", got, want)
	}
	if got := Report("", "", "", "screen", false, "", taken); got != "Bug report from a SnapShot of the screen.\n\nCaptured 2026-10-05 14:03 UTC; the picture is attached." {
		t.Errorf("a bare report = %q", got)
	}
}

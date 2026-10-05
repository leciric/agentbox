package desktop

import (
	"image"
	"slices"
	"strings"
	"testing"
	"time"
)

// Auto takes the browser's page only when Chromium's windows are all there is
// to see: the dock, the desktop and a minimised window don't count, and a
// terminal or an Electron app beside it does.
func TestBrowserOnly(t *testing.T) {
	chromium := parseWindow(`WM_CLASS(STRING) = "chromium (/home/dev/.config/agentbox/browser)", "Chromium"
_NET_WM_WINDOW_TYPE(ATOM) = _NET_WM_WINDOW_TYPE_NORMAL
_NET_WM_STATE(ATOM) = _NET_WM_STATE_MAXIMIZED_VERT`)
	if chromium.class == "" || !strings.Contains(chromium.kind, "_NORMAL") || !strings.Contains(chromium.state, "MAXIMIZED") {
		t.Fatalf("parseWindow() = %+v", chromium)
	}
	dock := window{class: `"xfce4-panel", "Xfce4-panel"`, kind: "_NET_WM_WINDOW_TYPE_DOCK"}
	terminal := window{class: `"xfce4-terminal", "Xfce4-terminal"`, kind: "_NET_WM_WINDOW_TYPE_NORMAL"}
	hidden := terminal
	hidden.state = "_NET_WM_STATE_HIDDEN"
	electron := window{class: `"agentbox", "AgentBox"`, kind: "_NET_WM_WINDOW_TYPE_NORMAL"}
	for _, c := range []struct {
		name    string
		windows []window
		want    bool
	}{
		{"browser", []window{dock, chromium}, true},
		{"minimised terminal", []window{dock, chromium, hidden}, true},
		{"terminal", []window{dock, chromium, terminal}, false},
		{"electron", []window{dock, electron}, false},
		{"nothing", []window{dock}, false},
	} {
		if got := browserOnly(c.windows); got != c.want {
			t.Errorf("%s: browserOnly() = %t, want %t", c.name, got, c.want)
		}
	}
}

// A recording with nothing to draw on is one ffmpeg; one with input to draw
// is a capture writing raw frames at a steady rate to the recorder, and an
// encoder of what it makes of them. Either way it's encoded once.
func TestFFmpegArgs(t *testing.T) {
	opts := RecordOptions{Out: "/t/r.mp4", Limit: time.Minute}
	plain := strings.Join(ffmpegArgs([]string{"-f", "x11grab", "-i", ":99"}, EvenSize, opts), " ")
	if !strings.Contains(plain, "-t 60 "+strings.Join(EncodeArgs, " ")+" -y /t/r.mp4") {
		t.Errorf("not encoded to the file: %s", plain)
	}
	capture := strings.Join(captureArgs([]string{"-f", "x11grab", "-i", ":99"}, image.Pt(1262, 749), opts), " ")
	for _, want := range []string{"scale=1262:749", "-fps_mode cfr -r 15", "-t 60", "-pix_fmt bgr0 -f rawvideo pipe:1"} {
		if !strings.Contains(capture, want) {
			t.Errorf("capture lacks %q: %s", want, capture)
		}
	}
	if strings.Contains(capture, "libx264") {
		t.Errorf("the capture encodes: %s", capture)
	}
	encode := strings.Join(composite(nil, image.Pt(1262, 749), opts).args, " ")
	if !strings.Contains(encode, "-video_size 1262x749 -framerate 15 -i pipe:0 -vf "+EvenSize+" "+strings.Join(EncodeArgs, " ")+" -y /t/r.mp4") {
		t.Errorf("the encoder doesn't encode the frames to the file: %s", encode)
	}
	if !slices.Contains(EncodeArgs, "yuv420p") || !slices.Contains(EncodeArgs, "+faststart") {
		t.Errorf("EncodeArgs = %v: Chromium plays 4:2:0 with its moov first", EncodeArgs)
	}
}

// The overlay is blended onto a BGR0 frame, each pixel once even where what
// it drew overlaps, and the frame is left alone where it drew nothing.
func TestBlend(t *testing.T) {
	rects := mergeRects([]image.Rectangle{image.Rect(0, 0, 4, 4), image.Rect(10, 10, 12, 12), image.Rect(2, 2, 6, 6)})
	if len(rects) != 2 || rects[0] != image.Rect(0, 0, 6, 6) {
		t.Fatalf("mergeRects() = %v", rects)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 1))
	copy(canvas.Pix[0:4], []byte{0xff, 0, 0, 0xff})    // opaque red
	copy(canvas.Pix[4:8], []byte{0x40, 0x40, 0, 0x80}) // half yellow, premultiplied
	frame := []byte{10, 20, 30, 0, 200, 200, 200, 0, 1, 2, 3, 0, 4, 5, 6, 0}
	blend(frame, 16, canvas, canvas.Rect)
	want := []byte{0, 0, 0xff, 0, 100, 0x40 + 100, 0x40 + 100, 0, 1, 2, 3, 0, 4, 5, 6, 0}
	if !slices.Equal(frame, want) {
		t.Errorf("blend() = %v, want %v", frame, want)
	}
}

// A click draws a ripple where it was, mapped onto the frame, and a key a
// caption at the bottom; both are gone once they've faded, and what draw
// says it drew covers what it did.
func TestOverlayDraws(t *testing.T) {
	ov, err := newOverlay(40, false)
	if err != nil {
		t.Fatal(err)
	}
	ov.place(image.Pt(100, 50), 0.5)
	ov.add(InputEvent{T: 10, Button: 1, X: 500, Y: 450})
	ov.add(InputEvent{T: 10, Key: "a"})
	frame := image.NewRGBA(image.Rect(0, 0, 640, 400))
	drawn := ov.draw(frame, 10.2)
	painted := func(x, y int) bool { return frame.RGBAAt(x, y).A != 0 }
	if !painted(200, 200) {
		t.Error("no ripple at the click, (500,450) on the display being (200,200) on the frame")
	}
	if !painted(320, 360) {
		t.Error("no caption above the bottom")
	}
	if painted(20, 20) {
		t.Error("painted where nothing happened")
	}
	for y := range 400 {
		for x := range 640 {
			if painted(x, y) && !slices.ContainsFunc(drawn, func(r image.Rectangle) bool { return image.Pt(x, y).In(r) }) {
				t.Fatalf("(%d,%d) painted outside what draw returned: %v", x, y, drawn)
			}
		}
	}
	clear(frame.Pix)
	if got := ov.draw(frame, 15); len(got) != 0 || painted(200, 200) || painted(320, 360) {
		t.Errorf("still drawing 5 s on: %v", got)
	}
}

package desktop

import (
	"context"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoTimeSkipsCuts(t *testing.T) {
	e := Edit{Cuts: Cuts{Start: 100, Recorded: 10, Cuts: []Cut{{From: 2, To: 5}}}}
	for _, c := range []struct {
		t, want float64
		ok      bool
	}{{101, 1, true}, {103, 2, true}, {106, 3, true}, {99, 0, false}, {111, 0, false}} {
		if v, ok := e.videoTime(c.t); v != c.want || ok != c.ok {
			t.Errorf("videoTime(%v) = %v, %v; want %v, %v", c.t, v, ok, c.want, c.ok)
		}
	}
}

func TestPlanZoomsOnClicksAndBacksOut(t *testing.T) {
	e := Edit{Size: image.Pt(1440, 900), Cuts: Cuts{Start: 0, Recorded: 20}, Events: []InputEvent{
		{T: 2, Button: 1, X: 1100, Y: 120},
		{T: 2.5, Key: "a"},
		{T: 12, Button: 1, X: 300, Y: 700},
	}}
	shots := e.Plan()
	var zoomed, back int
	for i, s := range shots {
		if i > 0 && s.At < shots[i-1].At {
			t.Fatalf("shots go back in time: %+v", shots)
		}
		if s.Zoom >= zoomMin {
			zoomed++
			if s.X-720/s.Zoom < 0 || s.X+720/s.Zoom > 1440 {
				t.Errorf("shot leaves the screen: %+v", s)
			}
		} else if i > 0 {
			back++
		}
	}
	if zoomed == 0 || back == 0 {
		t.Errorf("want shots that zoom in and back out, got %+v", shots)
	}
	if f := zoomFilter(shots, e.Size); !strings.HasPrefix(f, "zoompan=") {
		t.Errorf("filter: %s", f)
	}
}

// Between two chapters the camera backs out to the whole screen, however
// soon the next one's clicks come, when there is time for it to.
func TestPlanBacksOutBetweenChapters(t *testing.T) {
	e := Edit{Size: image.Pt(1440, 900), Cuts: Cuts{Recorded: 20},
		Events: []InputEvent{{T: 2, Button: 1, X: 200, Y: 200}, {T: 5.5, Button: 1, X: 260, Y: 240}},
		Notes:  []Note{{T: 0.5, Text: "One"}, {T: 3.6, Text: "Two"}}}
	shots := e.Plan()
	between := cameraAt(shots, 4.2)
	if between.Zoom != 1 {
		t.Errorf("between the chapters the camera is at %+v, want the whole screen; shots %+v", between, shots)
	}
	for _, at := range []float64{2, 5.5} {
		if s := cameraAt(shots, at); s.Zoom < zoomMin {
			t.Errorf("at the click at %v the camera is at %+v, want it zoomed in", at, s)
		}
	}
	// Without the second chapter, the clicks are one shot.
	e.Notes = e.Notes[:1]
	if s := cameraAt(e.Plan(), 4.2); s.Zoom < zoomMin {
		t.Errorf("within a chapter the camera backed out: %+v", s)
	}
}

// cameraAt follows the shots it is given, easing between them.
func TestCameraAtEases(t *testing.T) {
	shots := []Shot{{At: 0, Zoom: 1, X: 720, Y: 450}, {At: 1, Zoom: 1, X: 720, Y: 450}, {At: 2, Zoom: 2, X: 400, Y: 300}}
	if s := cameraAt(shots, 0.5); s.Zoom != 1 {
		t.Errorf("held shot = %+v", s)
	}
	mid := cameraAt(shots, 1.5)
	if mid.Zoom != 1.5 || mid.X != 560 {
		t.Errorf("halfway = %+v, want zoom 1.5 at x 560", mid)
	}
	early, late := cameraAt(shots, 1.1), cameraAt(shots, 1.9)
	if early.Zoom-1 >= 0.1 || 2-late.Zoom >= 0.1 {
		t.Errorf("not eased: %+v at 1.1, %+v at 1.9", early, late)
	}
	if s := cameraAt(shots, 9); s.Zoom != 2 {
		t.Errorf("after the last shot = %+v", s)
	}
	// The camera at 2x on 400,300 shows 40,75 to 760,525 of the screen.
	if p := cameraAt(shots, 9).onScreen(image.Pt(1440, 900), 400, 300); p != image.Pt(720, 450) {
		t.Errorf("the centre is at %v on screen", p)
	}
}

// A caption that would cover a click made while it is on goes to the top.
func TestCaptionsKeepClearOfClicks(t *testing.T) {
	size := image.Pt(1440, 900)
	whole := []Shot{{Zoom: 1, X: 720, Y: 450}}
	card := image.Rect(0, 0, 600, 90)
	e := Edit{Size: size, Cuts: Cuts{Recorded: 20}, Events: []InputEvent{{T: 3, Button: 1, X: 720, Y: 740}}}
	if at := e.placeCaption(card, whole, 2, 5); at.Y > 100 {
		t.Errorf("a caption over a click is at %v, want it at the top", at)
	}
	if at := e.placeCaption(card, whole, 6, 9); at.Y < 600 {
		t.Errorf("a caption with no click under it is at %v, want it at the bottom", at)
	}
}

func TestCaptionCardsWrapToTwoLines(t *testing.T) {
	for _, size := range []image.Point{{1440, 900}, {800, 520}} {
		short, err := captionCard(size, "1/3", "Open the settings")
		if err != nil {
			t.Fatal(err)
		}
		long, err := captionCard(size, "2/3", strings.Repeat("Fill in the display name and the email address ", 6))
		if err != nil {
			t.Fatal(err)
		}
		if long.Rect.Dx() > size.X || long.Rect.Dy() > size.Y/3 {
			t.Errorf("%v: a long caption is %v", size, long.Rect.Size())
		}
		if long.Rect.Dy() <= short.Rect.Dy() || long.Rect.Dy() > 3*short.Rect.Dy() {
			t.Errorf("%v: a long caption is %v tall, a short one %v: want two lines", size, long.Rect.Dy(), short.Rect.Dy())
		}
	}
	face, err := cardFace(30, false)
	if err != nil {
		t.Fatal(err)
	}
	lines, fits := wrap(face, strings.Repeat("word ", 100), 30*64*12, 2)
	if fits || len(lines) != 2 || !strings.HasSuffix(lines[1], "…") {
		t.Errorf("wrap = %q, %v", lines, fits)
	}
}

// An edit keeps the video's timeline, frame for frame, and its size.
func TestRenderKeepsTheTimeline(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s isn't installed", tool)
		}
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=640x400:rate=15", "-t", "6"}, EncodeArgs...)
	if out, err := exec.Command("ffmpeg", append(args, "-y", in)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	write := func(name string, v any) string {
		b, _ := json.Marshal(v)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cuts := write("cuts", Cuts{Recorded: 6, Start: 100})
	events := write("events", InputEvent{T: 103, Button: 1, X: 500, Y: 100})
	notes := write("notes", Note{T: 102.5, Text: "Click the corner"})
	e, err := ReadEdit(events, cuts, notes, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.Title = "A title"
	out := filepath.Join(dir, "out.mp4")
	if err := e.Render(context.Background(), in, out, os.Stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := duration(t, out), duration(t, in); got < want-0.01 || got > want+0.01 {
		t.Errorf("the edit plays %vs, the recording %vs", got, want)
	}
	if size, err := videoSize(context.Background(), out); err != nil || size != image.Pt(640, 400) {
		t.Errorf("the edit is %v, %v", size, err)
	}
}

// What an edit is worked out from may be missing: a recording that wasn't
// trimmed has no cuts, and one with no input drawn on, no events.
func TestReadEditWithoutTheRecordersFiles(t *testing.T) {
	dir := t.TempDir()
	e, err := ReadEdit(filepath.Join(dir, "events"), filepath.Join(dir, "cuts"), "", 42)
	if err != nil {
		t.Fatal(err)
	}
	if e.Cuts.Start != 42 || len(e.Events) != 0 {
		t.Errorf("edit = %+v", e)
	}
}

// A click is logged where it is in the frame: on a browser recording, the
// page, which is not where it is on the display.
func TestEventLogKeepsClicksInTheFramesCoordinates(t *testing.T) {
	ov, err := newOverlay(0, true)
	if err != nil {
		t.Fatal(err)
	}
	ov.place(image.Pt(100, 50), 2)
	path := filepath.Join(t.TempDir(), "events")
	l := logEvents(path, ov)
	l.on(InputEvent{T: 1, Button: 1, X: 150, Y: 80})
	l.on(InputEvent{T: 2, Key: "a"})
	l.write()
	var got []InputEvent
	if err := readLines(path, func(b []byte) error {
		var e InputEvent
		got = append(got, e)
		return json.Unmarshal(b, &got[len(got)-1])
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].X != 100 || got[0].Y != 60 || got[1].Key != "a" {
		t.Errorf("logged %+v", got)
	}
}

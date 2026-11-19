package devicestream

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// violet says whether the pixel at x,y of a width-wide RGB24 image is the
// touches' violet over grey, rather than the grey.
func violet(img []byte, width, x, y int) bool {
	p := img[(y*width+x)*3:]
	r, g, b := int(p[0]), int(p[1]), int(p[2])
	return b > g+30 && r > g+10
}

func grey(width, height int) []byte {
	img := make([]byte, width*height*3)
	for i := range img {
		img[i] = 0x80
	}
	return img
}

func TestTouchesDrawATapAsARippleThatEnds(t *testing.T) {
	var ts Touches
	at := time.Unix(100, 0)
	ts.Add(Touch{At: at, X: 0.25, Y: 0.5, Down: true})
	ts.Add(Touch{At: at.Add(10 * time.Millisecond), X: 0.25, Y: 0.5})

	if ts.Showing(at.Add(-time.Millisecond)) {
		t.Error("a ripple is drawn before its tap")
	}
	img := grey(400, 800)
	if !ts.Draw(img, 400, 800, at.Add(100*time.Millisecond)) {
		t.Fatal("nothing drawn 100 ms after a tap")
	}
	if !violet(img, 400, 100, 400) {
		t.Error("the ripple isn't where the finger landed")
	}
	if violet(img, 400, 300, 400) || violet(img, 400, 100, 100) {
		t.Error("the ripple is drawn away from where the finger landed")
	}
	if ts.Showing(at.Add(rippleDuration)) {
		t.Error("the ripple is still drawn once it is over")
	}
	img = grey(400, 800)
	if ts.Draw(img, 400, 800, at.Add(time.Second)) || !bytes.Equal(img, grey(400, 800)) {
		t.Error("a frame with no touches on it was drawn on")
	}
}

func TestTouchesFollowAFingerHeldDown(t *testing.T) {
	var ts Touches
	at := time.Unix(100, 0)
	// A swipe: one ripple where it starts, and a dot that follows the
	// finger until it lifts.
	ts.Add(Touch{At: at, X: 0.5, Y: 0.9, Down: true})
	ts.Add(Touch{At: at.Add(800 * time.Millisecond), X: 0.5, Y: 0.2, Down: true})
	img := grey(400, 800)
	ts.Draw(img, 400, 800, at.Add(900*time.Millisecond))
	if !violet(img, 400, 200, 160) {
		t.Error("no dot where the finger is")
	}
	if violet(img, 400, 200, 720) {
		t.Error("the dot is still where the finger was")
	}
	ts.Add(Touch{At: at.Add(time.Second), X: 0.5, Y: 0.2})
	if ts.Showing(at.Add(1100 * time.Millisecond)) {
		t.Error("the dot is drawn after the finger lifted")
	}
	// The move wasn't a tap of its own: one ripple, at the start.
	if n := len(ts.taps); n > 1 {
		t.Errorf("%d taps kept, want the swipe's one", n)
	}
}

func TestTouchesDrawNoneWhenNil(t *testing.T) {
	var ts *Touches
	if ts.Showing(time.Now()) || ts.Draw(grey(4, 4), 4, 4, time.Now()) {
		t.Error("nil touches drew something")
	}
}

func TestTouchesClipAtTheEdge(t *testing.T) {
	var ts Touches
	at := time.Unix(100, 0)
	ts.Add(Touch{At: at, X: 0, Y: 1, Down: true})
	img := grey(100, 200)
	// Its corner of the screen: drawn without going outside the image.
	if !ts.Draw(img, 100, 200, at.Add(200*time.Millisecond)) || !violet(img, 100, 2, 197) {
		t.Error("a tap in the corner isn't drawn")
	}
}

// The recorder draws the taps into the video it writes, and only there.
func TestRecordDrawsTheTaps(t *testing.T) {
	if _, err := exec.LookPath(FFmpeg); err != nil {
		t.Skip("no ffmpeg")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := ffmpegHasX264(ctx); err != nil {
		t.Skip(err)
	}
	dev := newFakeDevice()
	screen := grey(64, 128)
	dev.frames <- Frame{Width: 64, Height: 128, Pixels: screen}
	var ts Touches
	ts.Add(Touch{At: time.Now().Add(-50 * time.Millisecond), X: 0.5, Y: 0.5, Down: true})
	file := filepath.Join(t.TempDir(), "rec.mp4")
	if err := Record(ctx, dev, file, 300*time.Millisecond, &ts); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(screen, grey(64, 128)) {
		t.Error("the device's frame was drawn on")
	}
	out, err := exec.CommandContext(ctx, FFmpeg, "-v", "error", "-i", file, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil || len(out) != 64*128*3 {
		t.Fatalf("reading the recording's first frame: %v (%d bytes)", err, len(out))
	}
	if !violet(out, 64, 32, 64) {
		t.Errorf("the tap isn't on the recording: %v", out[(64*64+32)*3:][:3])
	}
}

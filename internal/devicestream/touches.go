package devicestream

import (
	"image"
	"math"
	"sync"
	"time"

	"golang.org/x/image/vector"
)

// A phone recording is the device's screen as it was, frame for frame: no
// cuts, no zoom and no cards, which a desktop recording's edit adds
// (desktop.Edit). All it gets is the touches, drawn live by the recorder as it
// encodes, so the one pass makes it: a tap is a ripple where the finger
// landed, the same violet ripple a desktop recording's clicks get, and a
// finger held down or dragged is a dot that follows it until it lifts.

// Touch is a finger touching the screen, or lifting off it, at X,Y: from 0 to
// 1 across and down the screen as the video shows it.
type Touch struct {
	At   time.Time
	X, Y float64
	// Down is true while the finger is on the screen: its first touch and
	// every move after it. A Touch with Down false lifts it.
	Down bool
}

const (
	// rippleDuration and the ripple's size and colour are the desktop
	// recorder's (internal/desktop/overlay.go), at the size of a 1080p
	// display: a phone's video is scaled by its width to match.
	rippleDuration = 500 * time.Millisecond
	rippleRadius   = 26.0
	rippleStroke   = 2.5
	// dotRadius is the held finger's dot.
	dotRadius = 14.0
	// touchScaleWidth is the video width the sizes above are for: a phone
	// at its usual 720 wide gets them at 1.5x, about a fingertip.
	touchScaleWidth = 480.0
)

// AgentBox's violet, #8b5cf6, as the desktop recorder's ripple.
var touchColour = [3]float64{0x8b, 0x5c, 0xf6}

// Touches keeps the touches made while a recording runs, and draws them onto
// its frames. It is safe to add to while a frame is being drawn.
type Touches struct {
	mu   sync.Mutex
	taps []Touch // where each finger landed: the ripples
	// held is the finger on the screen now, if one is.
	held *Touch
}

// Add records t. A touch that follows one that is down moves that finger;
// one that follows a lift, or nothing, is a tap.
func (ts *Touches) Add(t Touch) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if !t.Down {
		ts.held = nil
		return
	}
	if ts.held == nil {
		ts.taps = append(ts.taps, t)
	}
	ts.held = &t
}

// active is what is on the screen at now: the ripples still growing, and the
// finger held, if one is. Ripples long over are forgotten.
func (ts *Touches) active(now time.Time) (ripples []Touch, held *Touch) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	keep := ts.taps[:0]
	for _, t := range ts.taps {
		age := now.Sub(t.At)
		if age >= rippleDuration {
			continue
		}
		keep = append(keep, t)
		if age >= 0 {
			ripples = append(ripples, t)
		}
	}
	ts.taps = keep
	if ts.held != nil && !ts.held.At.After(now) {
		h := *ts.held
		held = &h
	}
	return ripples, held
}

// Showing says whether there is a touch to draw at now.
func (ts *Touches) Showing(now time.Time) bool {
	if ts == nil {
		return false
	}
	ripples, held := ts.active(now)
	return len(ripples) > 0 || held != nil
}

// Draw draws the touches at now onto pixels, a width x height RGB24 image,
// and says whether there were any. Nothing is drawn, and pixels isn't
// touched, when there are none.
func (ts *Touches) Draw(pixels []byte, width, height int, now time.Time) bool {
	if ts == nil {
		return false
	}
	ripples, held := ts.active(now)
	if len(ripples) == 0 && held == nil {
		return false
	}
	scale := max(1, float64(width)/touchScaleWidth)
	for _, t := range ripples {
		// Grows from a quarter of its size, quickly at first, as it
		// fades: the desktop recorder's ripple.
		p := math.Sqrt(float64(now.Sub(t.At)) / float64(rippleDuration))
		r := rippleRadius * scale * (0.25 + 0.75*p)
		cx, cy := t.X*float64(width), t.Y*float64(height)
		disc(pixels, width, height, cx, cy, r, 0, 0xaf/255.0*(1-p))
		disc(pixels, width, height, cx, cy, r, r-rippleStroke*scale, 1-p)
	}
	if held != nil {
		cx, cy := held.X*float64(width), held.Y*float64(height)
		r := dotRadius * scale
		disc(pixels, width, height, cx, cy, r, 0, 0.55)
		disc(pixels, width, height, cx, cy, r, r-rippleStroke*scale, 0.95)
	}
	return true
}

// disc blends the violet into pixels over the ring from inner to r around
// cx,cy (a whole disc when inner is 0), at alpha.
func disc(pixels []byte, width, height int, cx, cy, r, inner, alpha float64) {
	if alpha <= 0 || r <= 0 {
		return
	}
	box := image.Rect(int(cx-r)-1, int(cy-r)-1, int(cx+r)+2, int(cy+r)+2).Intersect(image.Rect(0, 0, width, height))
	if box.Empty() {
		return
	}
	var z vector.Rasterizer
	z.Reset(box.Dx(), box.Dy())
	ox, oy := float32(cx)-float32(box.Min.X), float32(cy)-float32(box.Min.Y)
	circlePath(&z, ox, oy, float32(r), false)
	if inner > 0 {
		circlePath(&z, ox, oy, float32(inner), true)
	}
	mask := image.NewAlpha(image.Rect(0, 0, box.Dx(), box.Dy()))
	z.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	for y := range box.Dy() {
		row := pixels[((box.Min.Y+y)*width+box.Min.X)*3:]
		for x := range box.Dx() {
			a := float64(mask.Pix[y*mask.Stride+x]) / 255 * alpha
			if a == 0 {
				continue
			}
			for c := range 3 {
				v := float64(row[x*3+c])
				row[x*3+c] = uint8(v + (touchColour[c]-v)*a + 0.5)
			}
		}
	}
}

// circlePath adds a circle to z, as four cubic Béziers; reverse winds it the
// other way, which cuts it out of a circle around it.
func circlePath(z *vector.Rasterizer, cx, cy, r float32, reverse bool) {
	const k = 0.5522847498 // a quarter circle's control points
	c := r * k
	if !reverse {
		z.MoveTo(cx+r, cy)
		z.CubeTo(cx+r, cy+c, cx+c, cy+r, cx, cy+r)
		z.CubeTo(cx-c, cy+r, cx-r, cy+c, cx-r, cy)
		z.CubeTo(cx-r, cy-c, cx-c, cy-r, cx, cy-r)
		z.CubeTo(cx+c, cy-r, cx+r, cy-c, cx+r, cy)
	} else {
		z.MoveTo(cx+r, cy)
		z.CubeTo(cx+r, cy-c, cx+c, cy-r, cx, cy-r)
		z.CubeTo(cx-c, cy-r, cx-r, cy-c, cx-r, cy)
		z.CubeTo(cx-r, cy+c, cx-c, cy+r, cx, cy+r)
		z.CubeTo(cx+c, cy+r, cx+r, cy+c, cx+r, cy)
	}
	z.ClosePath()
}

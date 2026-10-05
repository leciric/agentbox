package desktop

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// The overlay on a recording made with --input desktop is drawn live, frame by
// frame, by the recorder (Record), into the one pass that encodes the video,
// rather than onto the finished video by a second encode: that second pass was
// most of the wait between stopping a recording and having it. Nor is it a
// window on the display: there is no compositor, so an overlay window is an
// opaque box over whatever it covers, it lands in the desktop tools'
// screenshots too, and it redraws in front of the capture as it changes. The
// recorder's frames exist only in the video.
//
// A click is a ripple at the pointer: a disc that grows and fades out in half
// a second. Keys are a caption, one pill at a time at the bottom centre, over
// the dock rather than over the windows above it: typed text builds up into
// the words being typed, a combo reads Ctrl+C (and Ctrl+C ×3 when repeated),
// and the pill fades out once the keys stop. A browser recording, which has
// no cursor of its own, gets one drawn where the pointer is.

const (
	// captionHold is how long a caption stays after the last key it shows,
	// and how close together keys must be to add to it.
	captionHold    = 1.2
	captionFadeIn  = 0.12
	captionFadeOut = 0.35
	// captionRunes is as much typed text as a caption shows: the end of it,
	// after an ellipsis, once there is more.
	captionRunes = 40

	captionHeight  = 40
	captionPadding = 18
	// captionAdvance is the caption font's advance, which the pill is sized
	// by. A monospaced face is what makes that possible without measuring
	// text.
	captionAdvance = 11.2

	rippleRadius   = 26
	rippleDuration = 0.5
	rippleStroke   = 2.5
)

var (
	// AgentBox's violet, #8b5cf6.
	rippleColour  = color.NRGBA{0x8b, 0x5c, 0xf6, 0xff}
	captionBack   = color.NRGBA{0x14, 0x14, 0x1a, 0xdb}
	captionBorder = color.NRGBA{0xff, 0xff, 0xff, 0x2f}
	captionText   = color.NRGBA{0xf4, 0xf2, 0xff, 0xff}
)

// captionFonts are where Debian keeps DejaVu Sans Mono, which has the arrows
// and symbols key names use; Go Mono, built in, stands in without it.
var captionFonts = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
	"/usr/share/fonts/TTF/DejaVuSansMono.ttf",
}

// overlay is the input drawn over a recording, as it happens: add the
// events as they come, and draw it onto each frame.
type overlay struct {
	// bottom is how far above the frame's bottom edge the key caption's
	// centre sits.
	bottom int
	face   font.Face

	mu      sync.Mutex
	evs     []InputEvent
	pointer image.Point
	// cursor draws the pointer, for a frame that doesn't have it already.
	cursor bool
	// origin and scale map the display's coordinates, which events have,
	// onto the frame's: frame = (display - origin) * scale.
	origin image.Point
	scale  float64
}

func newOverlay(bottom int, cursor bool) (*overlay, error) {
	face, err := captionFace()
	if err != nil {
		return nil, err
	}
	return &overlay{bottom: bottom, face: face, cursor: cursor, scale: 1}, nil
}

// captionFace is the caption's font, at the size whose advance is
// captionAdvance.
func captionFace() (font.Face, error) {
	data := gomono.TTF
	for _, path := range captionFonts {
		if b, err := os.ReadFile(path); err == nil {
			data = b
			break
		}
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, err
	}
	probe, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 100, DPI: 72})
	if err != nil {
		return nil, err
	}
	adv, _ := probe.GlyphAdvance('0')
	size := 100 * captionAdvance / (float64(adv) / 64)
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// add takes one event from the display.
func (o *overlay) add(e InputEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if e.Button != 0 {
		o.pointer = image.Pt(e.X, e.Y)
	}
	// What was typed a while ago no longer shapes the caption, once the
	// caption it was in has gone. A new slice, since draw reads the old one
	// outside the lock.
	if n := len(o.evs); n > 0 && e.T-o.evs[n-1].T > captionHold+captionFadeOut+rippleDuration {
		o.evs = nil
	}
	o.evs = append(o.evs, e)
}

// move takes where the pointer is now.
func (o *overlay) move(x, y int) {
	o.mu.Lock()
	o.pointer = image.Pt(x, y)
	o.mu.Unlock()
}

// place maps the display onto the frame: the frame's top-left corner is at
// origin on the display, and one display pixel is scale of the frame's.
func (o *overlay) place(origin image.Point, scale float64) {
	o.mu.Lock()
	o.origin, o.scale = origin, scale
	o.mu.Unlock()
}

// draw draws the overlay at time now (InputEvent.T's clock) onto dst, and
// returns the rectangles it drew on, for the caller to clear before the next
// frame.
func (o *overlay) draw(dst *image.RGBA, now float64) []image.Rectangle {
	o.mu.Lock()
	evs := o.evs
	pointer, origin, scale, cursor := o.pointer, o.origin, o.scale, o.cursor
	o.mu.Unlock()
	if scale <= 0 {
		scale = 1
	}
	frame := func(x, y int) (float64, float64) {
		return float64(x-origin.X) * scale, float64(y-origin.Y) * scale
	}
	var drawn []image.Rectangle
	bounds := dst.Bounds()

	for _, e := range evs {
		t := now - e.T
		if e.Button == 0 || t < 0 || t >= rippleDuration {
			continue
		}
		// Grows from a quarter of its size, quickly at first, as it fades.
		p := math.Sqrt(t / rippleDuration)
		r := rippleRadius * (0.25 + 0.75*p)
		cx, cy := frame(e.X, e.Y)
		fill := rippleColour
		fill.A = uint8(0xaf * (1 - p))
		ring := rippleColour
		ring.A = uint8(0xff * (1 - p))
		var path vector.Rasterizer
		box := shape(&path, bounds, cx-r-2, cy-r-2, cx+r+2, cy+r+2, func(add func(pathFn)) {
			add(circle(cx, cy, r, false))
		})
		drawn = paint(dst, &path, box, fill, drawn)
		box = shape(&path, bounds, cx-r-2, cy-r-2, cx+r+2, cy+r+2, func(add func(pathFn)) {
			add(circle(cx, cy, r, false))
			add(circle(cx, cy, r-rippleStroke, true))
		})
		drawn = paint(dst, &path, box, ring, drawn)
	}

	if c, ok := captionAt(evs, now); ok {
		alpha := 1.0
		if c.first {
			alpha = min(alpha, (now-c.from)/captionFadeIn)
		}
		if c.last {
			alpha = min(alpha, (c.to-now)/captionFadeOut)
		}
		alpha = max(0, min(1, alpha))
		w := float64(textCells(c.text))*captionAdvance + 2*captionPadding
		cx, cy := float64(bounds.Dx())/2, float64(bounds.Dy()-o.bottom)
		if o.bottom <= 0 {
			cy = float64(bounds.Dy() - captionHeight)
		}
		x0, y0 := cx-w/2, cy-captionHeight/2
		var path vector.Rasterizer
		box := shape(&path, bounds, x0, y0, x0+w, y0+captionHeight, func(add func(pathFn)) {
			add(roundedRect(x0, y0, w, captionHeight, captionHeight/2, false))
		})
		drawn = paint(dst, &path, box, fade(captionBack, alpha), drawn)
		box = shape(&path, bounds, x0, y0, x0+w, y0+captionHeight, func(add func(pathFn)) {
			add(roundedRect(x0, y0, w, captionHeight, captionHeight/2, false))
			add(roundedRect(x0+1, y0+1, w-2, captionHeight-2, captionHeight/2-1, true))
		})
		drawn = paint(dst, &path, box, fade(captionBorder, alpha), drawn)
		m := o.face.Metrics()
		d := font.Drawer{Dst: dst, Src: image.NewUniform(fade(captionText, alpha)), Face: o.face}
		d.Dot = fixed.Point26_6{
			X: fixed.Int26_6((cx - float64(textCells(c.text))*captionAdvance/2) * 64),
			Y: fixed.Int26_6(cy*64) + (m.Ascent-m.Descent)/2,
		}
		d.DrawString(c.text)
	}

	if cursor {
		x, y := frame(pointer.X, pointer.Y)
		drawn = drawCursor(dst, x, y, drawn)
	}
	return drawn
}

// fade is c with its opacity multiplied by alpha.
func fade(c color.NRGBA, alpha float64) color.NRGBA {
	c.A = uint8(float64(c.A) * alpha)
	return c
}

// captionAt is the caption showing at now, if any.
func captionAt(evs []InputEvent, now float64) (caption, bool) {
	var past []InputEvent
	for _, e := range evs {
		if e.T <= now {
			past = append(past, e)
		}
	}
	for _, c := range captions(past) {
		if c.from <= now && now < c.to && c.text != "" {
			return c, true
		}
	}
	return caption{}, false
}

// A pathFn adds one closed figure to a rasterizer whose origin is at (ox, oy).
type pathFn func(r *vector.Rasterizer, ox, oy float64)

// shape resets r to cover the box (x0, y0)-(x1, y1), clipped to bounds, and
// adds the figures fill gives it. A figure added backwards cuts a hole in the
// ones before it. It returns the box, empty when it is off the frame.
func shape(r *vector.Rasterizer, bounds image.Rectangle, x0, y0, x1, y1 float64, fill func(add func(pathFn))) image.Rectangle {
	box := image.Rect(int(math.Floor(x0)), int(math.Floor(y0)), int(math.Ceil(x1)), int(math.Ceil(y1))).Intersect(bounds)
	if box.Empty() {
		return box
	}
	r.Reset(box.Dx(), box.Dy())
	ox, oy := float64(box.Min.X), float64(box.Min.Y)
	fill(func(p pathFn) { p(r, ox, oy) })
	return box
}

// paint fills what r covers with c, over dst.
func paint(dst *image.RGBA, r *vector.Rasterizer, box image.Rectangle, c color.NRGBA, drawn []image.Rectangle) []image.Rectangle {
	if box.Empty() || c.A == 0 {
		return drawn
	}
	r.DrawOp = draw.Over
	r.Draw(dst, box, image.NewUniform(c), image.Point{})
	return append(drawn, box)
}

// kappa is how far a cubic Bézier's control points sit to draw a quarter circle.
const kappa = 0.5523

func circle(cx, cy, r float64, reverse bool) pathFn {
	return roundedRect(cx-r, cy-r, 2*r, 2*r, r, reverse)
}

// roundedRect is a w×h rectangle at (x, y) with corners of radius r: a pill
// when r is half the height, a circle when it is half of both.
func roundedRect(x, y, w, h, r float64, reverse bool) pathFn {
	return func(p *vector.Rasterizer, ox, oy float64) {
		x, y := float32(x-ox), float32(y-oy)
		w, h, r := float32(w), float32(h), float32(r)
		k := r * kappa
		// The corners clockwise, each a line into it and a curve round it.
		type seg struct{ lx, ly, c1x, c1y, c2x, c2y, ex, ey float32 }
		segs := []seg{
			{x + w - r, y, x + w - r + k, y, x + w, y + r - k, x + w, y + r},
			{x + w, y + h - r, x + w, y + h - r + k, x + w - r + k, y + h, x + w - r, y + h},
			{x + r, y + h, x + r - k, y + h, x, y + h - r + k, x, y + h - r},
			{x, y + r, x, y + r - k, x + r - k, y, x + r, y},
		}
		if !reverse {
			p.MoveTo(x+r, y)
			for _, s := range segs {
				p.LineTo(s.lx, s.ly)
				p.CubeTo(s.c1x, s.c1y, s.c2x, s.c2y, s.ex, s.ey)
			}
			p.ClosePath()
			return
		}
		// The same outline, anticlockwise.
		p.MoveTo(x+r, y)
		for i := len(segs) - 1; i >= 0; i-- {
			s := segs[i]
			p.LineTo(s.ex, s.ey)
			p.CubeTo(s.c2x, s.c2y, s.c1x, s.c1y, s.lx, s.ly)
		}
		p.ClosePath()
	}
}

// cursorShape is the usual arrow pointer, its tip at (0, 0): the outline, then
// the inside, a pixel in from it.
var cursorShape = [2][]image.Point{
	{{0, 0}, {0, 19}, {5, 15}, {8, 22}, {12, 20}, {9, 14}, {15, 14}},
	{{1, 2}, {1, 16}, {5, 13}, {8, 20}, {10, 19}, {7, 12}, {12, 12}},
}

func drawCursor(dst *image.RGBA, x, y float64, drawn []image.Rectangle) []image.Rectangle {
	bounds := dst.Bounds()
	for i, c := range []color.NRGBA{{0, 0, 0, 0xff}, {0xff, 0xff, 0xff, 0xff}} {
		pts := cursorShape[i]
		var path vector.Rasterizer
		box := shape(&path, bounds, x, y, x+16, y+23, func(add func(pathFn)) {
			add(func(p *vector.Rasterizer, ox, oy float64) {
				p.MoveTo(float32(x-ox+float64(pts[0].X)), float32(y-oy+float64(pts[0].Y)))
				for _, pt := range pts[1:] {
					p.LineTo(float32(x-ox+float64(pt.X)), float32(y-oy+float64(pt.Y)))
				}
				p.ClosePath()
			})
		})
		drawn = paint(dst, &path, box, c, drawn)
	}
	return drawn
}

// caption is one stretch of time the key caption shows the same text. A pill
// is one or more captions in a row: first is the one it appears with, and
// last the one it fades out with. Captions follow one another without a gap,
// so a pill that changes, or gives way to the next, never blinks.
type caption struct {
	from, to    float64
	text        string
	first, last bool
}

// captions groups key presses into what the caption shows.
func captions(evs []InputEvent) []caption {
	type state struct {
		t    float64
		text string
		pill int
	}
	var states []state
	pill := 0
	var (
		text, label string
		typing      bool // the pill is text being typed, still open for more
		count       int  // how many times running the pill's combo was pressed
		last        float64
	)
	for _, e := range evs {
		if e.Key == "" {
			// A click ends the typing: what comes after it goes somewhere else.
			typing, label = false, ""
			continue
		}
		cont := pill > 0 && e.T-last < captionHold
		switch {
		case e.typed() && cont && typing:
			text += e.Key
		case e.typed():
			pill++
			text, typing, label = e.Key, true, ""
		case e.Key == "Backspace" && len(e.Mods) == 0 && cont && typing && text != "":
			_, size := utf8.DecodeLastRuneInString(text)
			text = text[:len(text)-size]
		case (e.Key == "Enter" || e.Key == "Tab") && len(e.Mods) == 0 && cont && typing:
			text += map[string]string{"Enter": " ⏎", "Tab": " ⇥"}[e.Key]
			typing = false
		default:
			l := comboLabel(e)
			if cont && !typing && l == label {
				count++
			} else {
				pill++
				label, count, typing = l, 1, false
			}
			text = label
			if count > 1 {
				text = fmt.Sprintf("%s ×%d", label, count)
			}
		}
		last = e.T
		states = append(states, state{e.T, text, pill})
	}

	out := make([]caption, 0, len(states))
	for i, s := range states {
		c := caption{from: s.t, to: s.t + captionHold + captionFadeOut, text: shorten(s.text)}
		c.first = i == 0 || states[i-1].pill != s.pill
		if i+1 < len(states) && states[i+1].t < c.to {
			c.to = states[i+1].t
		} else {
			c.last = true
		}
		out = append(out, c)
	}
	return out
}

// comboLabel is how a key that isn't typed text reads: its modifiers and its
// name, joined the way a shortcut is written.
func comboLabel(e InputEvent) string {
	names := map[string]string{"ctrl": "Ctrl", "alt": "Alt", "super": "Super", "shift": "Shift"}
	var parts []string
	for _, m := range e.Mods {
		if n := names[m]; n != "" {
			parts = append(parts, n)
		}
	}
	key := e.Key
	switch {
	case key == " ":
		key = "Space"
	case len(parts) > 0 && utf8.RuneCountInString(key) == 1:
		key = strings.ToUpper(key)
	}
	return strings.Join(append(parts, key), "+")
}

// shorten keeps the end of a long caption, which is where the typing is.
func shorten(s string) string {
	if utf8.RuneCountInString(s) <= captionRunes {
		return s
	}
	r := []rune(s)
	return "…" + string(r[len(r)-captionRunes+1:])
}

// textCells is how many monospaced cells s takes: two for a wide character.
func textCells(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 ||
			r >= 0xf900 && r <= 0xfaff || r >= 0xfe30 && r <= 0xfe4f || r >= 0xff00 && r <= 0xff60 ||
			r >= 0x1f300 && r <= 0x1faff || r >= 0x20000) {
			n++
		}
	}
	return n
}

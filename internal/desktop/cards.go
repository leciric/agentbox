package desktop

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The cards an edit lays over a recording: a caption for each chapter, a
// lower third with its step ("2/5") and its title, and a title card over its
// opening moments. They are drawn here, once each, as images ffmpeg lays over
// the frames: drawtext's box has no rounded corners, wraps nothing and fits
// one font to a line.

var (
	cardBackground = color.NRGBA{R: 17, G: 19, B: 26, A: 214}
	cardText       = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	cardMuted      = color.NRGBA{R: 255, G: 255, B: 255, A: 150}
	cardRule       = color.NRGBA{R: 255, G: 255, B: 255, A: 56}
	cardAccent     = color.NRGBA{R: 122, G: 162, B: 255, A: 255}
	titleBackdrop  = color.NRGBA{R: 10, G: 12, B: 18, A: 200}
)

// cardFonts are tried in order for the cards: Debian's DejaVu Sans, and Go's
// own sans, built in, when it isn't there.
var cardFonts = map[bool][]string{
	false: {"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "/usr/share/fonts/TTF/DejaVuSans.ttf"},
	true:  {"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", "/usr/share/fonts/TTF/DejaVuSans-Bold.ttf"},
}

func cardFace(size float64, bold bool) (font.Face, error) {
	data := goregular.TTF
	if bold {
		data = gobold.TTF
	}
	for _, path := range cardFonts[bold] {
		if b, err := os.ReadFile(path); err == nil {
			data = b
			break
		}
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// captionSize is the caption's font size for a video of height h: one that
// still reads with the video played at half its size.
func captionSize(h int) float64 {
	return math.Round(clamp(float64(h)/26, 22, 46))
}

// wrap breaks text into lines no wider than width, at most max of them, the
// last ending in an ellipsis when the text doesn't fit.
func wrap(face font.Face, text string, width fixed.Int26_6, max int) (lines []string, fits bool) {
	words := strings.Fields(text)
	var line string
	for i, w := range words {
		next := strings.TrimSpace(line + " " + w)
		if line == "" || font.MeasureString(face, next) <= width {
			line = next
			continue
		}
		lines = append(lines, line)
		if len(lines) == max {
			return ellipsize(face, lines, strings.Join(words[i:], " "), width), false
		}
		line = w
	}
	if line != "" {
		lines = append(lines, line)
	}
	// A single word wider than the line is cut short too.
	for i, l := range lines {
		if font.MeasureString(face, l) > width {
			lines[i] = ellipsize(face, nil, l, width)[0]
			fits = false
			return lines, fits
		}
	}
	return lines, true
}

// ellipsize ends the last of lines with as much of rest as fits, and "…".
func ellipsize(face font.Face, lines []string, rest string, width fixed.Int26_6) []string {
	last := ""
	if n := len(lines); n > 0 {
		last, lines = lines[n-1], lines[:n-1]
	}
	runes := []rune(strings.TrimSpace(last + " " + rest))
	for len(runes) > 0 && font.MeasureString(face, string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return append(lines, strings.TrimRight(string(runes), " ,.;:")+"…")
}

// captionCard draws a chapter's caption for a video of size: its step, like
// "2/5", and its text in at most two lines. It is the card alone, as small as
// it can be; where it goes is captionAt.
func captionCard(size image.Point, step, text string) (*image.NRGBA, error) {
	fs := captionSize(size.Y)
	maxW := math.Min(float64(size.X)*0.84, fs*30)
	pad := image.Pt(int(fs*0.85), int(fs*0.62))

	stepFace, err := cardFace(fs*0.72, true)
	if err != nil {
		return nil, err
	}
	stepW := 0
	if step != "" {
		stepW = font.MeasureString(stepFace, step).Ceil() + int(fs*1.3) // with its rule and gaps
	}
	textW := fixed.I(int(maxW) - 2*pad.X - stepW)
	// Text that won't fit in two lines is set smaller before it is cut short.
	var face font.Face
	var lines []string
	for _, scale := range []float64{1, 0.88} {
		if face, err = cardFace(fs*scale, false); err != nil {
			return nil, err
		}
		var fits bool
		if lines, fits = wrap(face, text, textW, 2); fits {
			break
		}
	}
	m := face.Metrics()
	lineH := int(float64(m.Height.Ceil()) * 1.12)
	widest := 0
	for _, l := range lines {
		widest = max(widest, font.MeasureString(face, l).Ceil())
	}
	w := 2*pad.X + stepW + widest
	h := 2*pad.Y + lineH*len(lines)
	shadow := int(fs * 0.4)
	img := image.NewNRGBA(image.Rect(0, 0, w+2*shadow, h+2*shadow))
	card := image.Rect(shadow, shadow, shadow+w, shadow+h)
	radius := fs * 0.55
	// A soft shadow lifts it off a light page.
	for i := shadow; i > 0; i -= 2 {
		roundRect(img, card.Inset(-i).Add(image.Pt(0, i/2)), radius+float64(i), color.NRGBA{A: uint8(10)})
	}
	roundRect(img, card, radius, cardBackground)

	// The text's first baseline: the lines centred in the card.
	base := card.Min.Y + pad.Y + (lineH-m.Height.Ceil())/2 + m.Ascent.Ceil()
	x := card.Min.X + pad.X
	if step != "" {
		sm := stepFace.Metrics()
		// The step sits level with the middle of the text.
		mid := card.Min.Y + h/2
		d := font.Drawer{Dst: img, Src: image.NewUniform(cardMuted), Face: stepFace,
			Dot: fixed.P(x, mid+(sm.Ascent.Ceil()-sm.Descent.Ceil())/2)}
		d.DrawString(step)
		ruleX := x + font.MeasureString(stepFace, step).Ceil() + int(fs*0.6)
		draw.Draw(img, image.Rect(ruleX, card.Min.Y+pad.Y, ruleX+max(1, int(fs/22)), card.Max.Y-pad.Y), image.NewUniform(cardRule), image.Point{}, draw.Over)
		x += stepW
	}
	for i, l := range lines {
		d := font.Drawer{Dst: img, Src: image.NewUniform(cardText), Face: face, Dot: fixed.P(x, base+i*lineH)}
		d.DrawString(l)
	}
	return img, nil
}

// cardAt is where a caption card goes on the video: centred, its bottom
// above the dock and the keys the recorder drew on it.
func cardAt(size image.Point, card image.Rectangle) image.Point {
	bottom := int(float64(size.Y)*0.06) + dockClearance
	return image.Pt((size.X-card.Dx())/2, max(0, size.Y-bottom-card.Dy()))
}

// dockClearance is what a caption keeps clear of at the bottom of the video:
// the desktop's dock, and the keys the recorder draws over it.
const dockClearance = 66

// titleCard draws the card a recording opens on, over the whole video: its
// title and, under it, a line saying what it is.
func titleCard(size image.Point, title, sub string) (*image.NRGBA, error) {
	img := image.NewNRGBA(image.Rectangle{Max: size})
	draw.Draw(img, img.Rect, image.NewUniform(titleBackdrop), image.Point{}, draw.Src)
	fs := captionSize(size.Y)
	big, err := cardFace(fs*1.5, true)
	if err != nil {
		return nil, err
	}
	small, err := cardFace(fs*0.8, false)
	if err != nil {
		return nil, err
	}
	width := fixed.I(int(float64(size.X) * 0.74))
	lines, _ := wrap(big, title, width, 2)
	var subs []string
	if sub != "" {
		subs, _ = wrap(small, sub, width, 1)
	}
	bm, sm := big.Metrics(), small.Metrics()
	bigH := int(float64(bm.Height.Ceil()) * 1.1)
	accent := int(fs * 0.18)
	gap := int(fs * 0.9)
	total := accent + gap + bigH*len(lines)
	if len(subs) > 0 {
		total += int(fs*0.5) + sm.Height.Ceil()
	}
	y := (size.Y - total) / 2
	// A short accent rule above the title.
	ruleW := int(fs * 2.2)
	roundRect(img, image.Rect((size.X-ruleW)/2, y, (size.X+ruleW)/2, y+accent), float64(accent)/2, cardAccent)
	y += accent + gap
	for _, l := range lines {
		d := font.Drawer{Dst: img, Src: image.NewUniform(cardText), Face: big}
		d.Dot = fixed.P((size.X-d.MeasureString(l).Ceil())/2, y+bm.Ascent.Ceil())
		d.DrawString(l)
		y += bigH
	}
	if len(subs) > 0 {
		y += int(fs * 0.5)
		d := font.Drawer{Dst: img, Src: image.NewUniform(cardMuted), Face: small}
		d.Dot = fixed.P((size.X-d.MeasureString(subs[0]).Ceil())/2, y+sm.Ascent.Ceil())
		d.DrawString(subs[0])
	}
	return img, nil
}

// roundRect fills r, its corners rounded to radius, over what img has there,
// with the edges antialiased.
func roundRect(img *image.NRGBA, r image.Rectangle, radius float64, c color.NRGBA) {
	radius = math.Min(radius, float64(min(r.Dx(), r.Dy()))/2)
	b := r.Intersect(img.Rect)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// How far the pixel's centre is outside the rounded rectangle.
			px, py := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(math.Max(float64(r.Min.X)+radius-px, px-(float64(r.Max.X)-radius)), 0)
			dy := math.Max(math.Max(float64(r.Min.Y)+radius-py, py-(float64(r.Max.Y)-radius)), 0)
			cover := clamp(radius-math.Hypot(dx, dy)+0.5, 0, 1)
			if cover > 0 {
				over(img, x, y, c, cover)
			}
		}
	}
}

// over blends c, at cover of its alpha, over img's pixel at x,y.
func over(img *image.NRGBA, x, y int, c color.NRGBA, cover float64) {
	i := img.PixOffset(x, y)
	p := img.Pix[i : i+4 : i+4]
	sa := float64(c.A) / 255 * cover
	da := float64(p[3]) / 255
	oa := sa + da*(1-sa)
	if oa == 0 {
		return
	}
	for k, sc := range []uint8{c.R, c.G, c.B} {
		p[k] = uint8(math.Round((float64(sc)*sa + float64(p[k])*da*(1-sa)) / oa))
	}
	p[3] = uint8(math.Round(oa * 255))
}

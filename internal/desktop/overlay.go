package desktop

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// The overlay on a recording made with --input desktop is drawn onto the video
// once it has been recorded, as ASS subtitles that ffmpeg's libass burns in,
// rather than by a window on the display while it records. Nothing on the
// display can do it as well: there is no compositor, so an overlay window is
// an opaque box over whatever it covers, it lands in the desktop tools'
// screenshots too, and it redraws in front of ffmpeg as it changes. Subtitles
// are antialiased and animated per frame, and exist only in the video.
//
// A click is a ripple at the pointer: a disc that grows and fades out in half
// a second. Keys are a caption, one pill at a time at the bottom centre, over
// the dock rather than over the windows above it: typed text builds up into
// the words being typed, a combo reads Ctrl+C (and Ctrl+C ×3 when repeated),
// and the pill fades out once the keys stop.

// OverlayOptions place the overlay on the video.
type OverlayOptions struct {
	// Start is when the video's first frame was taken, on InputEvent.T's clock.
	Start float64
	// Width and Height are the video's, which the display's pixels map onto
	// one to one.
	Width, Height int
	// Bottom is how far above the bottom edge the key caption's centre sits:
	// the middle of the dock.
	Bottom int
}

const (
	// captionHold is how long a caption stays after the last key it shows,
	// and how close together keys must be to add to it.
	captionHold    = 1.2
	captionFadeIn  = 0.12
	captionFadeOut = 0.35
	// captionRunes is as much typed text as a caption shows: the end of it,
	// after an ellipsis, once there is more.
	captionRunes = 40

	captionHeight   = 40
	captionFontSize = 22
	captionPadding  = 18
	// captionAdvance is DejaVu Sans Mono's advance at captionFontSize, which
	// the pill is sized by. A monospaced face is what makes that possible
	// without measuring text.
	captionAdvance = 0.51 * captionFontSize
	captionFont    = "DejaVu Sans Mono"

	rippleRadius   = 26
	rippleDuration = 0.5
)

// ASS colours are &HBBGGRR&, and alpha counts up from opaque (00) to
// transparent (FF).
const (
	// AgentBox's violet, #8b5cf6.
	rippleColour  = "&HF65C8B&"
	captionBack   = "&H1A1414&"
	captionBorder = "&HFFFFFF&"
	captionText   = "&HFFF2F4&"
)

// ReadInputLog reads what LogInput wrote. A line it can't read, like the last
// one of a log cut off mid-write, is skipped rather than failing the overlay.
func ReadInputLog(r io.Reader) ([]InputEvent, error) {
	var evs []InputEvent
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var e InputEvent
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.T > 0 {
			evs = append(evs, e)
		}
	}
	slices.SortStableFunc(evs, func(a, b InputEvent) int {
		switch {
		case a.T < b.T:
			return -1
		case a.T > b.T:
			return 1
		}
		return 0
	})
	return evs, sc.Err()
}

// Overlay renders events as an ASS script over a video of opts' size.
func Overlay(evs []InputEvent, opts OverlayOptions) string {
	w, h := max(opts.Width, 1), max(opts.Height, 1)
	var b strings.Builder
	fmt.Fprintf(&b, `[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
ScaledBorderAndShadow: yes
WrapStyle: 2

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,%s,%d,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,0,0,5,0,0,0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, w, h, captionFont, captionFontSize)

	cx, cy := w/2, h-opts.Bottom
	if opts.Bottom <= 0 {
		cy = h - captionHeight
	}
	for _, c := range captions(evs) {
		from, to := c.from-opts.Start, c.to-opts.Start
		if to <= 0 || c.text == "" {
			continue
		}
		fadeIn, fadeOut := 0, 0
		if c.first && from >= 0 {
			fadeIn = int(captionFadeIn * 1000)
		}
		if c.last {
			fadeOut = int(captionFadeOut * 1000)
		}
		pw := float64(textCells(c.text))*captionAdvance + 2*captionPadding
		fmt.Fprintf(&b, "Dialogue: 1,%s,%s,Default,,0,0,0,,{\\an5\\pos(%d,%d)\\p1\\bord1\\shad0\\blur0.6\\1c%s\\1a&H24&\\3c%s\\3a&HD0&\\fad(%d,%d)}%s\n",
			assTime(from), assTime(to), cx, cy, captionBack, captionBorder, fadeIn, fadeOut, roundedRect(pw, captionHeight, captionHeight/2))
		fmt.Fprintf(&b, "Dialogue: 2,%s,%s,Default,,0,0,0,,{\\an5\\pos(%d,%d)\\1c%s\\fad(%d,%d)}%s\n",
			assTime(from), assTime(to), cx, cy, captionText, fadeIn, fadeOut, assText(c.text))
	}
	circle := roundedRect(2*rippleRadius, 2*rippleRadius, rippleRadius)
	grow := int(rippleDuration * 1000)
	for _, e := range evs {
		t := e.T - opts.Start
		if e.Button == 0 || t < 0 {
			continue
		}
		from, to := assTime(t), assTime(t+rippleDuration)
		fmt.Fprintf(&b, "Dialogue: 3,%s,%s,Default,,0,0,0,,{\\an5\\pos(%d,%d)\\p1\\bord0\\shad0\\blur1\\1c%s\\1a&H50&\\fscx25\\fscy25\\t(0,%d,0.5,\\fscx100\\fscy100\\1a&HFF&)}%s\n",
			from, to, e.X, e.Y, rippleColour, grow, circle)
		fmt.Fprintf(&b, "Dialogue: 3,%s,%s,Default,,0,0,0,,{\\an5\\pos(%d,%d)\\p1\\bord2.5\\shad0\\blur0.6\\1a&HFF&\\3c%s\\3a&H00&\\fscx25\\fscy25\\t(0,%d,0.5,\\fscx100\\fscy100\\3a&HFF&)}%s\n",
			from, to, e.X, e.Y, rippleColour, grow, circle)
	}
	return b.String()
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

// assText makes typed text safe to put in a subtitle: libass reads { as the
// start of an override block and a backslash as the start of an escape, so
// they are swapped for their fullwidth lookalikes.
func assText(s string) string {
	return strings.NewReplacer("{", "｛", "}", "｝", `\`, "＼").Replace(s)
}

// assTime is a subtitle timestamp: hours, minutes, seconds and centiseconds.
func assTime(s float64) string {
	cs := int(math.Round(max(s, 0) * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}

// roundedRect is an ASS drawing of a w×h rectangle with corners of radius r,
// each a cubic Bézier: a pill when r is half the height, a circle when it is
// half of both.
func roundedRect(w, h, r float64) string {
	k := r * 0.5523 // how far a Bézier's control points sit to draw a quarter circle
	p := func(v float64) string { return fmt.Sprintf("%.0f", v) }
	return strings.Join([]string{
		"m", p(r), "0", "l", p(w - r), "0",
		"b", p(w - r + k), "0", p(w), p(r - k), p(w), p(r),
		"l", p(w), p(h - r),
		"b", p(w), p(h - r + k), p(w - r + k), p(h), p(w - r), p(h),
		"l", p(r), p(h),
		"b", p(r - k), p(h), "0", p(h - r + k), "0", p(h - r),
		"l", "0", p(r),
		"b", "0", p(r - k), p(r - k), "0", p(r), "0",
	}, " ")
}

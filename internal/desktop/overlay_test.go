package desktop

import (
	"math"
	"strings"
	"testing"
)

func TestKeyName(t *testing.T) {
	for _, c := range []struct {
		syms  []uint32
		shift bool
		want  string
	}{
		{[]uint32{'a', 'A'}, false, "a"},
		{[]uint32{'a', 'A'}, true, "A"},
		{[]uint32{'1', '!'}, true, "!"},
		{[]uint32{0x01000000 + '€'}, false, "€"}, // a keycode xdotool borrowed for a character
		{[]uint32{0xe9}, true, "É"},              // one keysym, shifted
		{[]uint32{0xff0d}, false, "Enter"},
		{[]uint32{0xff09, 0xfe20}, true, "Tab"},
		{[]uint32{0xff51}, false, "←"},
		{[]uint32{0xffc2}, false, "F5"},
		{[]uint32{0xfe52}, false, ""}, // a dead key types nothing by itself
		{nil, false, ""},
	} {
		if got := keyName(c.syms, c.shift); got != c.want {
			t.Errorf("keyName(%#x, shift %v) = %q, want %q", c.syms, c.shift, got, c.want)
		}
	}
}

func TestModifierName(t *testing.T) {
	for _, c := range []struct {
		sym  uint32
		want string
	}{
		{0xffe1, "shift"}, {0xffe2, "shift"},
		{0xffe3, "ctrl"}, {0xffe4, "ctrl"},
		{0xffe9, "alt"}, {0xffea, "alt"}, {0xffe7, "alt"}, {0xffe8, "alt"}, {0xfe03, "alt"},
		{0xffeb, "super"}, {0xffec, "super"},
		{0xffe5, "caps"},
		{'a', ""}, // an ordinary key is not a modifier
	} {
		if got := modifierName(c.sym); got != c.want {
			t.Errorf("modifierName(%#x) = %q, want %q", c.sym, got, c.want)
		}
	}
}

// typing makes an event for each character of s, 50 ms apart from at.
func typing(at float64, s string) []InputEvent {
	var evs []InputEvent
	for i, r := range s {
		evs = append(evs, InputEvent{T: at + float64(i)*0.05, Key: string(r)})
	}
	return evs
}

func TestCaptionsGroupWhatWasTyped(t *testing.T) {
	var evs []InputEvent
	evs = append(evs, typing(10, "helo")...)
	evs = append(evs, InputEvent{T: 10.3, Key: "Backspace"})
	evs = append(evs, typing(10.35, "lo world")...)
	evs = append(evs, InputEvent{T: 10.8, Key: "Enter"})
	evs = append(evs,
		InputEvent{T: 11.0, Key: "c", Mods: []string{"ctrl"}},
		InputEvent{T: 11.2, Key: "c", Mods: []string{"ctrl"}},
		InputEvent{T: 11.4, Key: "T", Mods: []string{"ctrl", "shift"}},
		InputEvent{T: 11.5, Key: "Tab", Mods: []string{"shift"}},
		// Long after, so a new pill.
		InputEvent{T: 20, Key: "Esc"},
		InputEvent{T: 20.5, Button: 1, X: 10, Y: 10},
		InputEvent{T: 20.6, Key: "a"},
	)
	cs := captions(evs)

	var shown []string
	for _, c := range cs {
		if c.first {
			shown = append(shown, "|")
		}
		shown = append(shown, c.text)
	}
	got := strings.Join(shown, " ")
	want := "| h he hel helo hel hell hello hello  hello w hello wo hello wor hello worl hello world hello world ⏎ " +
		"| Ctrl+C Ctrl+C ×2 | Ctrl+Shift+T | Shift+Tab | Esc | a"
	if got != want {
		t.Errorf("captions:\n got %s\nwant %s", got, want)
	}

	for i, c := range cs {
		if i+1 < len(cs) && !c.last && c.to != cs[i+1].from {
			t.Errorf("caption %q ends at %v, but the next starts at %v: the pill would blink", c.text, c.to, cs[i+1].from)
		}
	}
	// "Shift+Tab" gives way to Esc long after it faded; Esc to a, which the
	// click between them keeps from joining it, while it still shows.
	for _, c := range cs {
		switch c.text {
		case "Shift+Tab":
			if !c.last || math.Abs(c.to-(11.5+captionHold+captionFadeOut)) > 1e-9 {
				t.Errorf("Shift+Tab should fade out on its own: %+v", c)
			}
		case "Esc":
			if c.last || c.to != 20.6 {
				t.Errorf("Esc should give way to the next pill without fading: %+v", c)
			}
		}
	}
}

func TestCaptionsKeepTheEndOfLongTyping(t *testing.T) {
	cs := captions(typing(0, strings.Repeat("abcdefghij", 5)))
	last := cs[len(cs)-1].text
	if !strings.HasPrefix(last, "…") || !strings.HasSuffix(last, "hij") || len([]rune(last)) != captionRunes {
		t.Errorf("long typing shows as %q", last)
	}
}

func TestOverlay(t *testing.T) {
	evs := []InputEvent{
		{T: 97, Key: "q"}, // gone before the video starts: not shown
		{T: 101.5, Button: 1, X: 300, Y: 200},
		{T: 102, Key: "{"},
		{T: 102.05, Key: `\`},
	}
	ass := Overlay(evs, OverlayOptions{Start: 100, Width: 1440, Height: 900, Bottom: 38})
	for _, want := range []string{
		"PlayResX: 1440\nPlayResY: 900\n",
		`Dialogue: 3,0:00:01.50,0:00:02.00,Default,,0,0,0,,{\an5\pos(300,200)`,
		`Dialogue: 2,0:00:02.00,0:00:02.05,Default,,0,0,0,,{\an5\pos(720,862)\1c&HFFF2F4&\fad(120,0)}｛`,
		`\fad(0,350)}｛＼`,
	} {
		if !strings.Contains(ass, want) {
			t.Errorf("the overlay has no %q:\n%s", want, ass)
		}
	}
	if strings.Contains(ass, "}q\n") {
		t.Errorf("a key pressed before the video started is drawn onto it:\n%s", ass)
	}
}

func TestReadInputLogSkipsATornLine(t *testing.T) {
	evs, err := ReadInputLog(strings.NewReader(`{"t":2,"key":"b"}
{"t":1,"key":"a"}
{"t":3,"ke`))
	if err != nil || len(evs) != 2 || evs[0].Key != "a" || evs[1].Key != "b" {
		t.Errorf("ReadInputLog = %+v, %v", evs, err)
	}
}

package desktop

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// InputEvent is one key pressed or button clicked on the display, as
// LogInput writes it, one JSON object a line, for Overlay to draw onto a
// recording afterwards.
type InputEvent struct {
	// T is when it happened, in seconds since the Unix epoch: the clock
	// ffmpeg's x11grab stamps its frames with, so a recording's own start
	// time places every event on its timeline.
	T float64 `json:"t"`
	// Key is the key pressed: the character it types ("a", "A", " "), or its
	// name for one that types nothing ("Enter", "←", "F5"). Modifier
	// keys aren't events of their own; they are in Mods.
	Key string `json:"key,omitempty"`
	// Mods are the modifiers held with Key: ctrl, alt, super and shift.
	Mods []string `json:"mods,omitempty"`
	// Button is the mouse button pressed (1 left, 2 middle, 3 right), at X,Y.
	Button int `json:"button,omitempty"`
	X      int `json:"x,omitempty"`
	Y      int `json:"y,omitempty"`
}

// LogInput writes every key press and button press on the display to w, until
// ctx ends or the display goes away.
func LogInput(ctx context.Context, w io.Writer) error {
	x, err := dialX(Display)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { x.Close() })
	defer stop()
	defer x.Close()
	enc := json.NewEncoder(w)
	held := map[string]bool{}
	err = x.start(func(in rawInput) bool {
		now := float64(time.Now().UnixMicro()) / 1e6
		switch in.kind {
		case xButtonPress:
			// 4 to 7 are the scroll wheel, which isn't a click.
			if in.detail >= 4 && in.detail <= 7 {
				return true
			}
			return enc.Encode(InputEvent{T: now, Button: int(in.detail), X: in.x, Y: in.y}) == nil
		case xKeyPress, xKeyRelease:
			syms := x.keysyms[in.detail]
			base := uint32(0)
			if len(syms) > 0 {
				base = syms[0]
			}
			if mod := modifierName(base); mod != "" {
				held[mod] = in.kind == xKeyPress
				return true
			}
			if in.kind == xKeyRelease {
				return true
			}
			key := keyName(syms, held["shift"])
			if key == "" {
				return true
			}
			var mods []string
			for _, m := range []string{"ctrl", "alt", "super", "shift"} {
				if held[m] {
					mods = append(mods, m)
				}
			}
			return enc.Encode(InputEvent{T: now, Key: key, Mods: mods}) == nil
		}
		return true
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// modifierName is what a modifier key counts as, or "" for any other key.
func modifierName(sym uint32) string {
	switch sym {
	case 0xffe1, 0xffe2: // Shift_L, Shift_R
		return "shift"
	case 0xffe3, 0xffe4: // Control_L, Control_R
		return "ctrl"
	case 0xffe9, 0xffea, 0xffe7, 0xffe8, 0xfe03: // Alt, Meta, AltGr
		return "alt"
	case 0xffeb, 0xffec: // Super_L, Super_R
		return "super"
	case 0xffe5: // Caps_Lock, which isn't worth showing either
		return "caps"
	}
	return ""
}

// keyNames name the keys that type nothing, in the words Overlay shows.
var keyNames = map[uint32]string{
	0xff08: "Backspace", 0xff09: "Tab", 0xfe20: "Tab", 0xff0d: "Enter", 0xff8d: "Enter",
	0xff1b: "Esc", 0xffff: "Delete", 0xff9f: "Delete", 0xff63: "Insert",
	0xff50: "Home", 0xff57: "End", 0xff55: "PgUp", 0xff56: "PgDn",
	0xff51: "←", 0xff52: "↑", 0xff53: "→", 0xff54: "↓",
	0xff95: "Home", 0xff9c: "End", 0xff9a: "PgUp", 0xff9b: "PgDn",
	0xff96: "←", 0xff97: "↑", 0xff98: "→", 0xff99: "↓",
	0xff61: "PrtSc", 0xff13: "Pause", 0xff67: "Menu", 0xff80: " ",
	0xffaa: "*", 0xffab: "+", 0xffad: "-", 0xffae: ".", 0xffaf: "/",
}

// keyName is what a key press shows as: the character the keycode's keysyms
// type, shifted when shift is held, or the name of a key that types none.
func keyName(syms []uint32, shift bool) string {
	sym := uint32(0)
	if len(syms) > 0 {
		sym = syms[0]
	}
	if shift && len(syms) > 1 && syms[1] != 0 {
		sym = syms[1]
	}
	if name, ok := keyNames[sym]; ok {
		return name
	}
	var r rune
	switch {
	case sym >= 0xffbe && sym <= 0xffd5: // F1…F24
		return "F" + strconv.Itoa(int(sym-0xffbe+1))
	case sym >= 0xffb0 && sym <= 0xffb9: // the keypad's digits
		r = rune('0' + sym - 0xffb0)
	case sym >= 0x20 && sym <= 0x7e, sym >= 0xa0 && sym <= 0xff: // Latin-1 is its own code point
		r = rune(sym)
	case sym >= 0x01000100 && sym <= 0x0110ffff: // Unicode
		r = rune(sym - 0x01000000)
	default:
		return ""
	}
	if shift && (len(syms) < 2 || syms[1] == 0) {
		r = unicode.ToUpper(r)
	}
	if !unicode.IsPrint(r) {
		return ""
	}
	return string(r)
}

// typed reports whether an event types its key as text: a printable
// character, not an arrow, with nothing but shift held.
func (e InputEvent) typed() bool {
	if e.Key == "" || len([]rune(e.Key)) != 1 || strings.ContainsAny(e.Key, "←↑→↓") {
		return false
	}
	return !slices.ContainsFunc(e.Mods, func(m string) bool { return m != "shift" })
}

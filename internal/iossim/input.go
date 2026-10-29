package iossim

import (
	"fmt"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/devicestream"
)

// op is one message to simhost, a line of JSON on its stdin.
type op struct {
	Op     string  `json:"op"`              // "touch", "key", "button", "stream" or "keyframe"
	Phase  string  `json:"phase,omitempty"` // touch: "down", "move" or "up"
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	Usage  int     `json:"usage,omitempty"` // key: a USB HID keyboard usage
	Button string  `json:"button,omitempty"`
	Down   bool    `json:"down,omitempty"` // key and button: pressed, or let go
	On     bool    `json:"on,omitempty"`   // stream
}

// step is an op and how long to wait after it, so a tap is held and a swipe
// moves at a finger's pace.
type step struct {
	op   op
	wait time.Duration
}

// HID keyboard usages (USB HID Usage Tables, page 7) of the keys that have
// names; letters and digits are worked out in usageOf.
var namedKeys = map[string]int{
	"return": 0x28, "enter": 0x28, "escape": 0x29, "esc": 0x29, "backspace": 0x2a, "delete": 0x2a,
	"tab": 0x2b, "space": 0x2c, "right": 0x4f, "left": 0x50, "down": 0x51, "up": 0x52,
	"forwarddelete": 0x4c, "home": 0x4a, "end": 0x4d, "pageup": 0x4b, "pagedown": 0x4e,
}

var modifierKeys = map[string]int{
	"control": 0xe0, "ctrl": 0xe0, "shift": 0xe1, "option": 0xe2, "alt": 0xe2, "command": 0xe3, "cmd": 0xe3,
}

const usageShift = 0xe1

// usShifted are the characters a US keyboard types with shift, and the key
// under them.
var usShifted = map[rune]rune{
	'!': '1', '@': '2', '#': '3', '$': '4', '%': '5', '^': '6', '&': '7', '*': '8', '(': '9', ')': '0',
	'_': '-', '+': '=', '{': '[', '}': ']', '|': '\\', ':': ';', '"': '\'', '~': '`', '<': ',', '>': '.', '?': '/',
}

var usPunctuation = map[rune]int{
	'-': 0x2d, '=': 0x2e, '[': 0x2f, ']': 0x30, '\\': 0x31, ';': 0x33, '\'': 0x34, '`': 0x35, ',': 0x36, '.': 0x37, '/': 0x38,
	' ': 0x2c, '\n': 0x28, '\t': 0x2b,
}

// usageOf is the key that types r on a US keyboard, and whether it takes
// shift. ok is false for anything else, which text pastes instead.
func usageOf(r rune) (usage int, shift, ok bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return 0x04 + int(r-'a'), false, true
	case r >= 'A' && r <= 'Z':
		return 0x04 + int(r-'A'), true, true
	case r >= '1' && r <= '9':
		return 0x1e + int(r-'1'), false, true
	case r == '0':
		return 0x27, false, true
	}
	if base, ok := usShifted[r]; ok {
		u, _, _ := usageOf(base)
		return u, true, true
	}
	if u, ok := usPunctuation[r]; ok {
		return u, false, true
	}
	return 0, false, false
}

// keyUsage is a key's usage from its name: a named key, or one character.
func keyUsage(name string) (int, error) {
	if u, ok := namedKeys[strings.ToLower(name)]; ok {
		return u, nil
	}
	if r := []rune(name); len(r) == 1 {
		if u, _, ok := usageOf(r[0]); ok {
			return u, nil
		}
	}
	return 0, fmt.Errorf("unknown key %q: use a letter, a digit, or return, escape, backspace, tab, space, up, down, left or right", name)
}

var buttons = map[string]bool{"home": true, "lock": true, "siri": true, "volumeUp": true, "volumeDown": true}

const keyGap = 15 * time.Millisecond

// plan turns an input into simhost's steps. Pixel coordinates are made
// fractions of the screen's size, which size reports once simhost knows it.
// Text that a US keyboard can't type comes back as paste, for the caller to
// put on the simulator's pasteboard and paste with ⌘V.
func plan(in api.IOSInput, size func() (w, h int, err error)) (steps []step, paste string, err error) {
	x, y, x2, y2 := in.X, in.Y, in.X2, in.Y2
	if in.Pixels && (in.Type == "tap" || in.Type == "swipe" || in.Type == "touch") {
		w, h, err := size()
		if err != nil {
			return nil, "", err
		}
		x, y, x2, y2 = x/float64(w), y/float64(h), x2/float64(w), y2/float64(h)
	}
	inside := func(vs ...float64) error {
		for _, v := range vs {
			if v < 0 || v > 1 {
				return fmt.Errorf("the point (%g, %g) is off the screen", in.X, in.Y)
			}
		}
		return nil
	}
	switch in.Type {
	case "touch":
		if in.Phase != "down" && in.Phase != "move" && in.Phase != "up" {
			return nil, "", fmt.Errorf("unknown touch phase %q: use down, move or up", in.Phase)
		}
		if err := inside(x, y); err != nil {
			return nil, "", err
		}
		return []step{{op: op{Op: "touch", Phase: in.Phase, X: x, Y: y}}}, "", nil
	case "tap":
		if err := inside(x, y); err != nil {
			return nil, "", err
		}
		hold := time.Duration(max(in.DurationMS, 80)) * time.Millisecond
		return []step{
			{op: op{Op: "touch", Phase: "down", X: x, Y: y}, wait: hold},
			{op: op{Op: "touch", Phase: "up", X: x, Y: y}},
		}, "", nil
	case "swipe":
		if err := inside(x, y, x2, y2); err != nil {
			return nil, "", err
		}
		duration := time.Duration(in.DurationMS) * time.Millisecond
		if duration <= 0 {
			duration = 300 * time.Millisecond
		}
		const frame = 16 * time.Millisecond
		n := max(int(duration/frame), 2)
		steps = append(steps, step{op: op{Op: "touch", Phase: "down", X: x, Y: y}, wait: frame})
		for i := 1; i <= n; i++ {
			t := float64(i) / float64(n)
			steps = append(steps, step{op: op{Op: "touch", Phase: "move", X: x + (x2-x)*t, Y: y + (y2-y)*t}, wait: frame})
		}
		return append(steps, step{op: op{Op: "touch", Phase: "up", X: x2, Y: y2}}), "", nil
	case "key":
		usage, err := keyUsage(in.Key)
		if err != nil {
			return nil, "", err
		}
		var mods []int
		for _, m := range in.Modifiers {
			u, ok := modifierKeys[strings.ToLower(m)]
			if !ok {
				return nil, "", fmt.Errorf("unknown modifier %q: use shift, command, option or control", m)
			}
			mods = append(mods, u)
		}
		return press(usage, mods...), "", nil
	case "text":
		for _, r := range in.Text {
			if _, _, ok := usageOf(r); !ok {
				return nil, in.Text, nil
			}
		}
		for _, r := range in.Text {
			usage, shift, _ := usageOf(r)
			if shift {
				steps = append(steps, press(usage, usageShift)...)
			} else {
				steps = append(steps, press(usage)...)
			}
		}
		return steps, "", nil
	case "button":
		if !buttons[in.Button] {
			return nil, "", fmt.Errorf("unknown button %q: use home, lock, siri, volumeUp or volumeDown", in.Button)
		}
		switch in.Phase {
		case "down", "up":
			return []step{{op: op{Op: "button", Button: in.Button, Down: in.Phase == "down"}}}, "", nil
		case "":
			hold := time.Duration(max(in.DurationMS, 80)) * time.Millisecond
			return []step{
				{op: op{Op: "button", Button: in.Button, Down: true}, wait: hold},
				{op: op{Op: "button", Button: in.Button}},
			}, "", nil
		}
		return nil, "", fmt.Errorf("unknown button phase %q: use down or up", in.Phase)
	}
	return nil, "", fmt.Errorf("unknown input %q: use touch, tap, swipe, key, text or button", in.Type)
}

// press holds modifiers, presses and lets go of usage, and lets go of the
// modifiers.
func press(usage int, modifiers ...int) []step {
	var steps []step
	for _, m := range modifiers {
		steps = append(steps, step{op: op{Op: "key", Usage: m, Down: true}})
	}
	steps = append(steps, step{op: op{Op: "key", Usage: usage, Down: true}, wait: keyGap}, step{op: op{Op: "key", Usage: usage}, wait: keyGap})
	for i := len(modifiers) - 1; i >= 0; i-- {
		steps = append(steps, step{op: op{Op: "key", Usage: modifiers[i]}})
	}
	return steps
}

// browserKeys are the keys a viewer names as KeyboardEvent.key does
// (devicestream's key input), by their HID usage.
var browserKeys = map[string]int{
	"Enter": 0x28, "Escape": 0x29, "Backspace": 0x2a, "Tab": 0x2b, "Delete": 0x4c,
	"ArrowRight": 0x4f, "ArrowLeft": 0x50, "ArrowDown": 0x51, "ArrowUp": 0x52,
	"Home": 0x4a, "End": 0x4d, "PageUp": 0x4b, "PageDown": 0x4e,
	"Control": 0xe0, "Shift": 0xe1, "Alt": 0xe2, "Meta": 0xe3,
}

// wheelReach is how far a finger drags, as a fraction of the screen, for
// each wheel click: an iPhone has no wheel, so a viewer's scrolling is a
// short swipe the other way.
const wheelReach = 0.08

// fromViewer is what a viewer's input (internal/devicestream's) is on a
// simulator: an api.IOSInput for plan, or simhost's steps for a key held
// and let go as the viewer's are. Neither, for a button an iPhone hasn't
// got (back, recents, rotate).
func fromViewer(in devicestream.Input) (*api.IOSInput, []step, error) {
	switch in.Type {
	case devicestream.InputTouch:
		phase := in.Action
		if phase == "cancel" {
			phase = "up"
		}
		return &api.IOSInput{Type: "touch", Phase: phase, X: in.X, Y: in.Y}, nil, nil
	case devicestream.InputKey:
		if in.Action != "down" && in.Action != "up" {
			return nil, nil, fmt.Errorf("unknown key action %q", in.Action)
		}
		usage, ok := browserKeys[in.Key]
		// Else a character, with Control or Meta held: its key.
		if r := []rune(in.Key); !ok && len(r) == 1 {
			usage, _, ok = usageOf(r[0])
		}
		if !ok {
			return nil, nil, fmt.Errorf("unknown key %q", in.Key)
		}
		return nil, []step{{op: op{Op: "key", Usage: usage, Down: in.Action == "down"}}}, nil
	case devicestream.InputText:
		return &api.IOSInput{Type: "text", Text: in.Text}, nil, nil
	case devicestream.InputWheel:
		clamp := func(v float64) float64 { return min(max(v, 0), 1) }
		// float64() rounds each product on its own, which arm64 would
		// otherwise fuse into the subtraction: the same swipe everywhere.
		return &api.IOSInput{
			Type: "swipe", X: in.X, Y: in.Y, DurationMS: 100,
			X2: clamp(in.X - float64(in.DX*wheelReach)), Y2: clamp(in.Y - float64(in.DY*wheelReach)),
		}, nil, nil
	case devicestream.InputButton:
		if in.Button == "home" {
			return &api.IOSInput{Type: "button", Button: "home"}, nil, nil
		}
		return nil, nil, nil
	}
	return nil, nil, fmt.Errorf("unknown input %q", in.Type)
}

// pasteSteps is ⌘V, after text was put on the simulator's pasteboard.
func pasteSteps() []step {
	u, _ := keyUsage("v")
	return press(u, modifierKeys["command"])
}

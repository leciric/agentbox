package emulator

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unicode"

	"agentbox/internal/devicestream"
)

// Device is an emulator as a devicestream.Device.
type Device struct {
	c *Client
	// MaxSize bounds the video's longer side; the emulator scales its
	// screen down to it.
	MaxSize int

	once          sync.Once
	width, height int // the screen's own, upright
	sizeErr       error
	mu            sync.Mutex
	rotation      Rotation
}

// DefaultMaxSize is a phone's long side at about two thirds: sharp at the
// sizes the app shows it at, and cheap to encode.
const DefaultMaxSize = 1600

func NewDevice(c *Client) *Device {
	return &Device{c: c, MaxSize: DefaultMaxSize}
}

func (d *Device) Platform() string { return "android" }

func (d *Device) size(ctx context.Context) (int, int, error) {
	d.once.Do(func() { d.width, d.height, d.sizeErr = d.c.ScreenSize(ctx) })
	return d.width, d.height, d.sizeErr
}

func (d *Device) Screen(ctx context.Context, fn func(devicestream.Frame) error) error {
	w, h, err := d.size(ctx)
	if err != nil {
		return err
	}
	side := min(max(w, h), d.MaxSize)
	// A square box, so the screen fits it the same turned either way.
	return d.c.StreamScreen(ctx, side, side, func(img Image) error {
		d.mu.Lock()
		d.rotation = img.Rotation
		d.mu.Unlock()
		return fn(devicestream.Frame{Width: img.Width, Height: img.Height, Pixels: img.Pixels, At: time.Now()})
	})
}

// panel maps x, y from 0 to 1 across the screen as shown, turned by r, to
// the touch panel's pixels, which stay those of the upright screen.
func panel(x, y float64, r Rotation, width, height int) (int, int) {
	switch r {
	case Landscape:
		x, y = 1-y, x
	case ReversePortrait:
		x, y = 1-x, 1-y
	case ReverseLandscape:
		x, y = y, 1-x
	}
	return devicestream.Point(x, y, width, height)
}

// androidButtons are the emulator's names for the device's own keys.
var androidButtons = map[string]string{"home": "GoHome", "back": "GoBack", "recents": "AppSwitch"}

func (d *Device) Input(ctx context.Context, in devicestream.Input) error {
	w, h, err := d.size(ctx)
	if err != nil {
		return err
	}
	d.mu.Lock()
	r := d.rotation
	d.mu.Unlock()
	switch in.Type {
	case devicestream.InputTouch:
		x, y := panel(in.X, in.Y, r, w, h)
		pressed := in.Action == "down" || in.Action == "move"
		return d.c.SendTouch(ctx, Touch{X: x, Y: y, ID: in.ID, Pressed: pressed})
	case devicestream.InputKey:
		event := KeyDown
		if in.Action == "up" {
			event = KeyUp
		}
		return d.c.SendKey(ctx, event, in.Key)
	case devicestream.InputText:
		return d.typeText(ctx, in.Text)
	case devicestream.InputWheel:
		x, y := panel(in.X, in.Y, r, w, h)
		if err := d.c.MoveMouse(ctx, x, y); err != nil {
			return err
		}
		// The web's dy > 0 scrolls down, the wheel turned toward you.
		return d.c.Wheel(ctx, int(in.DX*120), int(-in.DY*120))
	case devicestream.InputButton:
		if in.Button == "rotate" {
			next := Landscape
			if r == Landscape || r == ReverseLandscape {
				next = Portrait
			}
			return d.c.SetRotation(ctx, next)
		}
		if key, ok := androidButtons[in.Button]; ok {
			return d.c.SendKey(ctx, KeyPress, key)
		}
		return nil
	}
	return nil
}

// typeText types ASCII on the emulator's keyboard, and pastes the rest: its
// keyboard map has nothing else.
func (d *Device) typeText(ctx context.Context, text string) error {
	if isASCII(text) {
		return d.c.TypeText(ctx, text)
	}
	if err := d.c.SetClipboard(ctx, text); err != nil {
		return err
	}
	if err := d.c.SendKey(ctx, KeyDown, "Control"); err != nil {
		return err
	}
	err := d.c.SendKey(ctx, KeyPress, "v")
	if up := d.c.SendKey(ctx, KeyUp, "Control"); err == nil {
		err = up
	}
	if err != nil {
		return fmt.Errorf("pasting text: %w", err)
	}
	return nil
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII || (r < 0x20 && r != '\n' && r != '\t') {
			return false
		}
	}
	return true
}

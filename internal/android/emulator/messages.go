package emulator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"google.golang.org/protobuf/encoding/protowire"
)

// Rotation is the device's, in quarter turns counterclockwise from portrait
// (the proto's SkinRotation).
type Rotation int

const (
	Portrait Rotation = iota
	Landscape
	ReversePortrait
	ReverseLandscape
)

// Image is one screen image, RGB888, row after row.
type Image struct {
	Width, Height int
	Rotation      Rotation
	Seq           uint32
	TimestampUs   uint64
	Pixels        []byte
}

// imageFormat is an ImageFormat message: RGB888 (2), at width x height when
// they are set, the emulator scaling the screen to fit.
func imageFormat(width, height int) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, 2) // RGB888
	if width > 0 {
		b = protowire.AppendTag(b, 3, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(width))
	}
	if height > 0 {
		b = protowire.AppendTag(b, 4, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(height))
	}
	return b
}

// fields calls fn on each field of message b.
func fields(b []byte, fn func(num protowire.Number, typ protowire.Type, v uint64, bytes []byte)) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			fn(num, typ, v, nil)
			b = b[n:]
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			fn(num, typ, 0, v)
			b = b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
		}
	}
	return nil
}

// parseImage reads an Image message.
func parseImage(b []byte) (Image, error) {
	var img Image
	var inner error
	err := fields(b, func(num protowire.Number, typ protowire.Type, v uint64, bytes []byte) {
		switch {
		case num == 1 && typ == protowire.BytesType: // format
			inner = fields(bytes, func(num protowire.Number, typ protowire.Type, v uint64, bytes []byte) {
				switch {
				case num == 2 && typ == protowire.BytesType: // rotation
					_ = fields(bytes, func(num protowire.Number, typ protowire.Type, v uint64, _ []byte) {
						if num == 1 && typ == protowire.VarintType {
							img.Rotation = Rotation(v)
						}
					})
				case num == 3 && typ == protowire.VarintType:
					img.Width = int(v)
				case num == 4 && typ == protowire.VarintType:
					img.Height = int(v)
				}
			})
		case num == 4 && typ == protowire.BytesType:
			img.Pixels = bytes
		case num == 5 && typ == protowire.VarintType:
			img.Seq = uint32(v)
		case num == 6 && typ == protowire.VarintType:
			img.TimestampUs = v
		}
	})
	if err == nil {
		err = inner
	}
	if err != nil {
		return Image{}, fmt.Errorf("reading the emulator's image: %w", err)
	}
	if img.Width <= 0 || img.Height <= 0 || img.Width > 16384 || img.Height > 16384 {
		return Image{}, fmt.Errorf("the emulator sent a %dx%d image", img.Width, img.Height)
	}
	if len(img.Pixels) != img.Width*img.Height*3 {
		return Image{}, fmt.Errorf("the emulator sent %d bytes for a %dx%d image", len(img.Pixels), img.Width, img.Height)
	}
	return img, nil
}

// StreamScreen calls fn with the screen, scaled to fit width x height (0 for
// the screen's own size), as it is now and then each time it changes, until
// ctx ends or fn fails.
func (c *Client) StreamScreen(ctx context.Context, width, height int, fn func(Image) error) error {
	res, err := c.call(ctx, "streamScreenshot", imageFormat(width, height))
	if err != nil {
		return err
	}
	defer res.Close()
	for {
		msg, err := res.Next()
		if err == io.EOF {
			return errors.New("the emulator stopped sending its screen")
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		img, err := parseImage(msg)
		if err != nil {
			return err
		}
		if err := fn(img); err != nil {
			return err
		}
	}
}

// Screen returns the screen once, at its own size.
func (c *Client) Screen(ctx context.Context) (Image, error) {
	msg, err := c.unary(ctx, "getScreenshot", imageFormat(0, 0))
	if err != nil {
		return Image{}, err
	}
	return parseImage(msg)
}

// Touch is one finger on the screen, in the screen's own pixels.
type Touch struct {
	X, Y    int
	ID      int
	Pressed bool // down or moving, rather than lifted
}

// Touch is a TouchEvent message.
func touchEvent(touches []Touch) []byte {
	var b []byte
	for _, t := range touches {
		var m []byte
		m = protowire.AppendTag(m, 1, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(int64(t.X)))
		m = protowire.AppendTag(m, 2, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(int64(t.Y)))
		m = protowire.AppendTag(m, 3, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(int64(t.ID)))
		pressure := 0
		if t.Pressed {
			pressure = 1
		}
		m = protowire.AppendTag(m, 4, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(pressure))
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, m)
	}
	return b
}

func (c *Client) SendTouch(ctx context.Context, touches ...Touch) error {
	_, err := c.unary(ctx, "sendTouch", touchEvent(touches))
	return err
}

// KeyEvent is a KeyboardEvent's type.
type KeyEvent int

const (
	KeyDown KeyEvent = iota
	KeyUp
	KeyPress
)

// keyboardEvent is a KeyboardEvent message naming the key as the web does
// ("Enter", "ArrowLeft", "a"), or, for Android's own keys, "GoHome", "GoBack"
// and "AppSwitch"; or typing text.
func keyboardEvent(event KeyEvent, key, text string) []byte {
	var b []byte
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(event))
	if key != "" {
		b = protowire.AppendTag(b, 4, protowire.BytesType)
		b = protowire.AppendString(b, key)
	}
	if text != "" {
		b = protowire.AppendTag(b, 5, protowire.BytesType)
		b = protowire.AppendString(b, text)
	}
	return b
}

func (c *Client) SendKey(ctx context.Context, event KeyEvent, key string) error {
	_, err := c.unary(ctx, "sendKey", keyboardEvent(event, key, ""))
	return err
}

// TypeText types text. The emulator types it through its keyboard map, which
// has ASCII only: SetClipboard and a paste reach the rest.
func (c *Client) TypeText(ctx context.Context, text string) error {
	_, err := c.unary(ctx, "sendKey", keyboardEvent(KeyPress, "", text))
	return err
}

// SetClipboard puts text on the device's clipboard.
func (c *Client) SetClipboard(ctx context.Context, text string) error {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, text)
	_, err := c.unary(ctx, "setClipboard", b)
	return err
}

// SetRotation turns the device, as its rotation sensor would.
func (c *Client) SetRotation(ctx context.Context, r Rotation) error {
	// The emulator's own rotate buttons tilt the device about z.
	z := map[Rotation]float32{Portrait: 0, Landscape: 90, ReversePortrait: 180, ReverseLandscape: -90}[r]
	var data []byte
	for _, v := range []float32{0, 0, z} {
		data = protowire.AppendFixed32(data, math.Float32bits(v))
	}
	var value []byte
	value = protowire.AppendTag(value, 1, protowire.BytesType)
	value = protowire.AppendBytes(value, data)
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, 1) // ROTATION
	b = protowire.AppendTag(b, 3, protowire.BytesType)
	b = protowire.AppendBytes(b, value)
	b = protowire.AppendTag(b, 4, protowire.VarintType)
	b = protowire.AppendVarint(b, 1) // STEP: turn at once
	_, err := c.unary(ctx, "setPhysicalModel", b)
	return err
}

// HardwareConfig returns the emulator's hardware settings, like hw.lcd.width.
func (c *Client) HardwareConfig(ctx context.Context) (map[string]string, error) {
	msg, err := c.unary(ctx, "getStatus", nil)
	if err != nil {
		return nil, err
	}
	config := map[string]string{}
	err = fields(msg, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) {
		if num != 5 || typ != protowire.BytesType {
			return
		}
		_ = fields(b, func(num protowire.Number, typ protowire.Type, _ uint64, entry []byte) {
			if num != 1 || typ != protowire.BytesType {
				return
			}
			var key, value string
			_ = fields(entry, func(num protowire.Number, _ protowire.Type, _ uint64, s []byte) {
				switch num {
				case 1:
					key = string(s)
				case 2:
					value = string(s)
				}
			})
			config[key] = value
		})
	})
	return config, err
}

// ScreenSize is the screen's own size in pixels, upright.
func (c *Client) ScreenSize(ctx context.Context) (width, height int, err error) {
	config, err := c.HardwareConfig(ctx)
	if err != nil {
		return 0, 0, err
	}
	width, _ = strconv.Atoi(config["hw.lcd.width"])
	height, _ = strconv.Atoi(config["hw.lcd.height"])
	if width <= 0 || height <= 0 {
		return 0, 0, errors.New("the emulator didn't say its screen's size")
	}
	return width, height, nil
}

// MoveMouse moves the mouse pointer to x, y with no buttons down: where the
// wheel then scrolls.
func (c *Client) MoveMouse(ctx context.Context, x, y int) error {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(int64(x)))
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(int64(y)))
	_, err := c.unary(ctx, "sendMouse", b)
	return err
}

// Wheel turns the mouse wheel by dx, dy clicks (dy > 0 away from you, which
// scrolls up), in 120ths of a click.
func (c *Client) Wheel(ctx context.Context, dx, dy int) error {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(int64(dx)))
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(int64(dy)))
	_, err := c.unary(ctx, "injectWheel", b)
	return err
}

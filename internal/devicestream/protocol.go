// Package devicestream shows a device's screen (an Android emulator, an iOS
// Simulator) as H.264 video and passes input into it, over one WebSocket.
//
// A server runs next to the device, in the agent's machine: it reads the
// screen as raw frames, sent only when the screen changes, encodes them
// (Encoder: ffmpeg's libx264, no B-frames, zero latency) and serves Serve's
// WebSocket. The daemon relays that WebSocket, message for message, to its
// own API socket, and the desktop app's DeviceStreamView
// (desktop/src/renderer/components/device/) decodes it with WebCodecs. What
// is particular to one kind of device is a Device; the wire format below is
// the same for all of them.
//
// # Wire format, version 1
//
// Server to viewer, binary messages whose first byte says what they are:
//
//	0x01 config  JSON (UTF-8): {"v":1, "platform":"android", "codec":"avc1.42c01f",
//	             "width":720, "height":1600, "description":"<base64 avcC>"}
//	             How to decode the frames that follow: codec is an RFC 6381
//	             codec string and description the AVCDecoderConfigurationRecord
//	             (SPS and PPS), as WebCodecs' VideoDecoderConfig takes them;
//	             width and height are the video's, upright as shown. Sent
//	             first, and again whenever the video starts over (the device
//	             turned, or the viewer asked for a keyframe); the frame after
//	             it is a keyframe.
//	0x02 frame   flags (1 byte: bit 0, a keyframe), timestamp (8 bytes, big
//	             endian, microseconds on the server's clock), then one access
//	             unit in AVCC form: NAL units each after its length in 4 bytes,
//	             big endian. Frames come only when the screen changes, so a
//	             still screen sends nothing; the viewer shows each one as it
//	             is decoded (no reordering: there are no B-frames).
//	0x03 status  JSON: {"error":"…"}, why the stream ended, just before the
//	             server closes the WebSocket.
//
// Viewer to server, text messages, each a JSON object with a "type":
//
//	{"type":"touch", "action":"down"|"move"|"up"|"cancel", "x":0.5, "y":0.25, "id":0}
//	    A finger, at x and y from 0 to 1 across the video as it is shown
//	    (from the top left), so the viewer needs nothing of the device's
//	    geometry. id tells fingers apart.
//	{"type":"key", "action":"down"|"up", "key":"Enter"}
//	    A key that isn't text, named as KeyboardEvent.key names it: Enter,
//	    Backspace, Delete, Tab, Escape, the arrows, Home, End, PageUp,
//	    PageDown, and the modifiers Shift, Control, Alt and Meta.
//	{"type":"text", "text":"héllo"}
//	    Text to type into what has focus.
//	{"type":"wheel", "x":0.5, "y":0.5, "dx":0, "dy":1}
//	    Scrolling at x, y, in wheel clicks; dy > 0 scrolls down, as
//	    WheelEvent.deltaY does.
//	{"type":"button", "button":"home"|"back"|"recents"|"rotate"}
//	    The device's own buttons. rotate turns it a quarter turn; a device
//	    without a button (an iPhone has no back) ignores it.
//	{"type":"keyframe"}
//	    Start the video over: a config and a keyframe follow. A viewer asks
//	    after its decoder failed.
//
// Unknown message types and fields are ignored both ways, so either side can
// grow without the other: a change that an old peer would misread takes a
// new "v".
package devicestream

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Version is the wire format's, in config messages.
const Version = 1

const (
	msgConfig byte = 0x01
	msgFrame  byte = 0x02
	msgStatus byte = 0x03
)

// Config describes the video that follows.
type Config struct {
	V           int    `json:"v"`
	Platform    string `json:"platform"`
	Codec       string `json:"codec"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Description []byte `json:"description"` // base64 in JSON
}

// Packet is one encoded frame.
type Packet struct {
	Key  bool
	At   time.Time
	Data []byte // AVCC
}

// Input is a message from the viewer.
type Input struct {
	Type   string  `json:"type"`
	Action string  `json:"action,omitempty"`
	X      float64 `json:"x,omitempty"`
	Y      float64 `json:"y,omitempty"`
	ID     int     `json:"id,omitempty"`
	Key    string  `json:"key,omitempty"`
	Text   string  `json:"text,omitempty"`
	DX     float64 `json:"dx,omitempty"`
	DY     float64 `json:"dy,omitempty"`
	Button string  `json:"button,omitempty"`
}

// The types of Input.
const (
	InputTouch    = "touch"
	InputKey      = "key"
	InputText     = "text"
	InputWheel    = "wheel"
	InputButton   = "button"
	InputKeyframe = "keyframe"
)

// EncodeConfig is a config message.
func EncodeConfig(c Config) []byte {
	c.V = Version
	data, _ := json.Marshal(c)
	return append([]byte{msgConfig}, data...)
}

// EncodeFrame is a frame message.
func EncodeFrame(p Packet) []byte {
	out := make([]byte, 10+len(p.Data))
	out[0] = msgFrame
	if p.Key {
		out[1] = 1
	}
	binary.BigEndian.PutUint64(out[2:10], uint64(p.At.UnixMicro()))
	copy(out[10:], p.Data)
	return out
}

// EncodeStatus is a status message saying why the stream ended.
func EncodeStatus(err error) []byte {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	return append([]byte{msgStatus}, data...)
}

// Message is a server message, decoded: one of Config, Packet and Err is set.
type Message struct {
	Config *Config
	Packet *Packet
	Err    string
}

// DecodeMessage reads a server message, for tests and Go viewers.
func DecodeMessage(b []byte) (Message, error) {
	if len(b) == 0 {
		return Message{}, errors.New("an empty message")
	}
	switch b[0] {
	case msgConfig:
		var c Config
		if err := json.Unmarshal(b[1:], &c); err != nil {
			return Message{}, fmt.Errorf("a config message: %w", err)
		}
		return Message{Config: &c}, nil
	case msgFrame:
		if len(b) < 10 {
			return Message{}, errors.New("a frame message too short")
		}
		return Message{Packet: &Packet{
			Key:  b[1]&1 != 0,
			At:   time.UnixMicro(int64(binary.BigEndian.Uint64(b[2:10]))),
			Data: b[10:],
		}}, nil
	case msgStatus:
		var s struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(b[1:], &s); err != nil {
			return Message{}, fmt.Errorf("a status message: %w", err)
		}
		return Message{Err: s.Error}, nil
	}
	return Message{}, fmt.Errorf("unknown message type %#x", b[0])
}

// DecodeInput reads a viewer's message.
func DecodeInput(b []byte) (Input, error) {
	var in Input
	if err := json.Unmarshal(b, &in); err != nil {
		return Input{}, fmt.Errorf("reading input: %w", err)
	}
	if in.Type == "" {
		return Input{}, errors.New("reading input: no type")
	}
	return in, nil
}

// CodecString is the RFC 6381 codec string ("avc1.42c01f") of an
// AVCDecoderConfigurationRecord: its profile, constraints and level.
func CodecString(avcC []byte) (string, error) {
	if len(avcC) < 4 || avcC[0] != 1 {
		return "", errors.New("not an AVCDecoderConfigurationRecord")
	}
	return fmt.Sprintf("avc1.%02x%02x%02x", avcC[1], avcC[2], avcC[3]), nil
}

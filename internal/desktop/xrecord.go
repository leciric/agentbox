package desktop

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// A client of the X server's RECORD extension, which is how a recording sees
// the keys pressed and the buttons clicked on the display: every client's
// input, the desktop tools' XTEST included, without grabbing anything or
// putting a window on the screen. It speaks the few requests it needs straight
// over the display's socket, rather than through a binding, since Xlib isn't
// something a Go binary can link without cgo. Xvnc runs without an
// authorization file, so the connection carries none.

var le = binary.LittleEndian

// X's core event types, and the core request a client remaps keys with.
const (
	xKeyPress              = 2
	xKeyRelease            = 3
	xButtonPress           = 4
	xButtonRelease         = 5
	xChangeKeyboardMapping = 100
	xGetKeyboardMapping    = 101
	xQueryExtension        = 98
	recordCreateContext    = 1
	recordEnableContext    = 5
	recordAllClients       = 3
	recordFromServer       = 0
	recordFromClient       = 1
)

// rawInput is one device event, as RECORD reports it.
type rawInput struct {
	kind   byte // xKeyPress … xButtonRelease
	detail byte // the keycode or the button
	x, y   int  // where the pointer was, on the root window
}

// xRecorder is one connection to the display, recording from it once start
// has enabled the context.
type xRecorder struct {
	conn   net.Conn
	r      *bufio.Reader
	idBase uint32
	minKey byte
	maxKey byte
	record byte // RECORD's major opcode

	// keysyms is the keyboard mapping, keycode by keycode, kept up to date
	// from the ChangeKeyboardMapping requests recorded alongside the input:
	// xdotool type borrows a spare keycode for a character the keyboard
	// doesn't have, and gives it back right after.
	keysyms map[byte][]uint32
}

func dialX(display string) (*xRecorder, error) {
	n := strings.TrimPrefix(display, ":")
	if i := strings.IndexByte(n, '.'); i >= 0 {
		n = n[:i]
	}
	conn, err := net.Dial("unix", "/tmp/.X11-unix/X"+n)
	if err != nil {
		return nil, err
	}
	x := &xRecorder{conn: conn, r: bufio.NewReader(conn), keysyms: map[byte][]uint32{}}
	if err := x.setup(); err != nil {
		conn.Close()
		return nil, err
	}
	return x, nil
}

func (x *xRecorder) Close() error { return x.conn.Close() }

func (x *xRecorder) setup() error {
	// Little-endian, protocol 11.0, no authorization.
	if _, err := x.conn.Write([]byte{'l', 0, 11, 0, 0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	head := make([]byte, 8)
	if _, err := io.ReadFull(x.r, head); err != nil {
		return err
	}
	body := make([]byte, int(le.Uint16(head[6:]))*4)
	if _, err := io.ReadFull(x.r, body); err != nil {
		return err
	}
	if head[0] != 1 {
		reason := body
		if head[0] == 0 && int(head[1]) <= len(body) {
			reason = body[:head[1]]
		}
		return fmt.Errorf("the display refused the connection: %s", strings.TrimSpace(string(reason)))
	}
	if len(body) < 28 {
		return errors.New("the display's setup reply is too short")
	}
	x.idBase = le.Uint32(body[4:])
	x.minKey, x.maxKey = body[26], body[27]

	name := "RECORD"
	req := make([]byte, 8+pad4(len(name)))
	req[0] = xQueryExtension
	le.PutUint16(req[2:], uint16(len(req)/4))
	le.PutUint16(req[4:], uint16(len(name)))
	copy(req[8:], name)
	reply, err := x.roundTrip(req)
	if err != nil {
		return err
	}
	if reply[8] == 0 {
		return errors.New("the display has no RECORD extension")
	}
	x.record = reply[9]

	count := int(x.maxKey) - int(x.minKey) + 1
	reply, err = x.roundTrip([]byte{xGetKeyboardMapping, 0, 2, 0, x.minKey, byte(count), 0, 0})
	if err != nil {
		return err
	}
	x.setKeysyms(x.minKey, count, int(reply[1]), reply[32:])
	return nil
}

// roundTrip sends one request and reads its reply, skipping any event that
// arrives first.
func (x *xRecorder) roundTrip(req []byte) ([]byte, error) {
	if _, err := x.conn.Write(req); err != nil {
		return nil, err
	}
	for {
		reply, err := x.readReply()
		if err != nil {
			return nil, err
		}
		switch reply[0] {
		case 0:
			return nil, fmt.Errorf("the display answered request %d with error %d", req[0], reply[1])
		case 1:
			return reply, nil
		}
	}
}

func (x *xRecorder) readReply() ([]byte, error) {
	head := make([]byte, 32)
	if _, err := io.ReadFull(x.r, head); err != nil {
		return nil, err
	}
	if head[0] != 1 {
		return head, nil
	}
	extra := int(le.Uint32(head[4:])) * 4
	reply := make([]byte, 32+extra)
	copy(reply, head)
	if _, err := io.ReadFull(x.r, reply[32:]); err != nil {
		return nil, err
	}
	return reply, nil
}

func (x *xRecorder) setKeysyms(first byte, count, perKey int, data []byte) {
	for i := range count {
		syms := make([]uint32, perKey)
		for j := range perKey {
			if off := (i*perKey + j) * 4; off+4 <= len(data) {
				syms[j] = le.Uint32(data[off:])
			}
		}
		x.keysyms[first+byte(i)] = syms
	}
}

// start creates a context that records every client's key and button events,
// and the requests that remap the keyboard, and enables it. From then on the
// connection only carries what it records, which each calls fn with, until
// the connection closes or fn returns false.
func (x *xRecorder) start(fn func(rawInput) bool) error {
	ctxID := x.idBase | 1
	// One client spec (every client) and one range, whose 24 bytes are the
	// core requests, core replies, extension requests and replies, delivered
	// events, device events, errors and the two client flags.
	req := make([]byte, 20+4+24)
	req[0], req[1] = x.record, recordCreateContext
	le.PutUint16(req[2:], uint16(len(req)/4))
	le.PutUint32(req[4:], ctxID)
	le.PutUint32(req[12:], 1)
	le.PutUint32(req[16:], 1)
	le.PutUint32(req[20:], recordAllClients)
	rng := req[24:]
	rng[0], rng[1] = xChangeKeyboardMapping, xChangeKeyboardMapping
	rng[18], rng[19] = xKeyPress, xButtonRelease
	if _, err := x.conn.Write(req); err != nil {
		return err
	}
	enable := make([]byte, 8)
	enable[0], enable[1] = x.record, recordEnableContext
	le.PutUint16(enable[2:], 2)
	le.PutUint32(enable[4:], ctxID)
	if _, err := x.conn.Write(enable); err != nil {
		return err
	}
	for {
		reply, err := x.readReply()
		if err != nil {
			return err
		}
		switch reply[0] {
		case 0:
			return fmt.Errorf("the display wouldn't record its input: error %d", reply[1])
		case 1:
		default:
			continue
		}
		// A client whose byte order isn't ours would need its data swapped;
		// nothing on the display is.
		if reply[9] != 0 {
			continue
		}
		data := reply[32:]
		switch reply[1] {
		case recordFromServer:
			for len(data) >= 32 {
				ev := data[:32]
				data = data[32:]
				kind := ev[0] & 0x7f
				if kind < xKeyPress || kind > xButtonRelease {
					continue
				}
				in := rawInput{kind: kind, detail: ev[1], x: int(int16(le.Uint16(ev[20:]))), y: int(int16(le.Uint16(ev[22:])))}
				if !fn(in) {
					return nil
				}
			}
		case recordFromClient:
			for len(data) >= 8 {
				size := int(le.Uint16(data[2:])) * 4
				if size < 8 || size > len(data) {
					break
				}
				if data[0] == xChangeKeyboardMapping {
					x.setKeysyms(data[4], int(data[1]), int(data[5]), data[8:size])
				}
				data = data[size:]
			}
		}
	}
}

func pad4(n int) int { return (n + 3) &^ 3 }

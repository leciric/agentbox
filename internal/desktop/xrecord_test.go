package desktop

import (
	"bufio"
	"net"
	"testing"
	"time"
)

func TestPad4(t *testing.T) {
	for _, c := range []struct{ n, want int }{
		{0, 0}, {1, 4}, {2, 4}, {3, 4}, {4, 4}, {5, 8}, {6, 8}, {8, 8},
	} {
		if got := pad4(c.n); got != c.want {
			t.Errorf("pad4(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

func TestSetKeysyms(t *testing.T) {
	x := &xRecorder{keysyms: map[byte][]uint32{}}
	// Two keycodes, two keysyms each.
	data := make([]byte, 16)
	le.PutUint32(data[0:], 'a')
	le.PutUint32(data[4:], 'A')
	le.PutUint32(data[8:], '1')
	le.PutUint32(data[12:], '!')
	x.setKeysyms(38, 2, 2, data)
	if got := x.keysyms[38]; len(got) != 2 || got[0] != 'a' || got[1] != 'A' {
		t.Errorf("keysyms[38] = %v, want [a A]", got)
	}
	if got := x.keysyms[39]; len(got) != 2 || got[0] != '1' || got[1] != '!' {
		t.Errorf("keysyms[39] = %v, want [1 !]", got)
	}
	// Truncated data leaves the trailing syms zero rather than panicking.
	x.setKeysyms(50, 1, 4, data[:4])
	if got := x.keysyms[50]; len(got) != 4 || got[0] != 'a' || got[1] != 0 {
		t.Errorf("keysyms[50] with truncated data = %v, want [a 0 0 0]", got)
	}
}

// xServer is a fake X server behind a net.Pipe, so setup/roundTrip/start can
// be driven end to end without a real display.
type xServer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func newXServer(t *testing.T) (*xRecorder, *xServer) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	x := &xRecorder{conn: client, r: bufio.NewReader(client), keysyms: map[byte][]uint32{}}
	return x, &xServer{t: t, conn: server, r: bufio.NewReader(server)}
}

// readRequest reads one request's fixed 4-byte header plus its declared body,
// for requests shaped like the core protocol's (opcode, data, length in u16).
func (s *xServer) readRequest(n int) []byte {
	s.t.Helper()
	buf := make([]byte, n)
	if _, err := readFull(s.r, buf); err != nil {
		s.t.Fatalf("reading request: %v", err)
	}
	return buf
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// serveSetup answers the connection setup, the QueryExtension for RECORD and
// the GetKeyboardMapping that follows, as a real display would.
func (s *xServer) serveSetup(recordOpcode byte) {
	s.readRequest(12) // the setup request: byte order, protocol version, no auth.
	body := make([]byte, 32)
	le.PutUint32(body[4:], 0x400) // idBase
	body[26], body[27] = 8, 10    // minKey, maxKey: two keycodes, 8 and 9… up to 10.
	head := []byte{1, 0, 0, 0, 0, 0, byte(len(body) / 4), 0}
	s.conn.Write(head)
	s.conn.Write(body)

	qe := s.readRequest(8 + pad4(len("RECORD")))
	_ = qe
	reply := make([]byte, 32)
	reply[0] = 1
	reply[8] = 1 // present
	reply[9] = recordOpcode
	s.conn.Write(reply)

	s.readRequest(8) // GetKeyboardMapping
	count := int(body[27]) - int(body[26]) + 1
	gkm := make([]byte, 32+count*4)
	gkm[0] = 1
	gkm[1] = 1 // one keysym per key
	le.PutUint32(gkm[4:], uint32(count))
	for i := range count {
		le.PutUint32(gkm[32+i*4:], uint32('a'+i))
	}
	s.conn.Write(gkm)
}

func TestXRecorderSetup(t *testing.T) {
	x, srv := newXServer(t)
	done := make(chan error, 1)
	go func() { done <- x.setup() }()
	srv.serveSetup(42)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("setup() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup() didn't return")
	}
	if x.idBase != 0x400 {
		t.Errorf("idBase = %#x, want 0x400", x.idBase)
	}
	if x.minKey != 8 || x.maxKey != 10 {
		t.Errorf("keys = [%d, %d], want [8, 10]", x.minKey, x.maxKey)
	}
	if x.record != 42 {
		t.Errorf("record opcode = %d, want 42", x.record)
	}
	if got := x.keysyms[8]; len(got) != 1 || got[0] != 'a' {
		t.Errorf("keysyms[8] = %v, want [a]", got)
	}
}

func TestXRecorderSetupRefused(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	x := &xRecorder{conn: client, r: bufio.NewReader(client), keysyms: map[byte][]uint32{}}
	done := make(chan error, 1)
	go func() { done <- x.setup() }()

	buf := make([]byte, 12)
	readFull(bufio.NewReader(server), buf)
	reason := "go away"
	body := make([]byte, pad4(len(reason)))
	copy(body, reason)
	head := []byte{0, byte(len(reason)), 0, 0, 0, 0, byte(len(body) / 4), 0}
	server.Write(head)
	server.Write(body)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("setup() with a refusal: want an error")
		}
		if got := err.Error(); got != "the display refused the connection: go away" {
			t.Errorf("setup() error = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup() didn't return")
	}
}

func TestXRecorderRoundTripSkipsEvents(t *testing.T) {
	x, srv := newXServer(t)
	done := make(chan struct {
		reply []byte
		err   error
	}, 1)
	go func() {
		reply, err := x.roundTrip([]byte{99, 0, 1, 0})
		done <- struct {
			reply []byte
			err   error
		}{reply, err}
	}()
	srv.readRequest(4)
	// An unrelated event arrives first (kind 2, KeyPress): neither error (0) nor reply (1).
	event := make([]byte, 32)
	event[0] = xKeyPress
	srv.conn.Write(event)
	// Then the real reply.
	reply := make([]byte, 32)
	reply[0] = 1
	reply[9] = 7
	srv.conn.Write(reply)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("roundTrip() = %v", r.err)
		}
		if r.reply[9] != 7 {
			t.Errorf("roundTrip() skipped straight to the reply's byte 9 = %d, want 7", r.reply[9])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("roundTrip() didn't return")
	}
}

func TestXRecorderClose(t *testing.T) {
	x, srv := newXServer(t)
	if err := x.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
	// The pipe's other end sees the close too: a further write fails.
	if _, err := srv.conn.Write([]byte{0}); err == nil {
		t.Error("the server side could still write after Close()")
	}
}

// TestXRecorderStartDecodesEvents drives start() through a context create,
// context enable, a server event, a client's keyboard remap, and another
// server event, checking that only the well-formed key/button events reach
// fn and that the remap updates the keysyms mid-stream, which is what a
// recording actually depends on to label keys correctly.
func TestXRecorderStartDecodesEvents(t *testing.T) {
	x, srv := newXServer(t)
	x.idBase = 0x400
	x.record = 42

	var got []rawInput
	done := make(chan error, 1)
	go func() {
		done <- x.start(func(in rawInput) bool {
			got = append(got, in)
			return len(got) < 2
		})
	}()

	// CreateContext, then EnableContext: both just consumed.
	srv.readRequest(20 + 4 + 24)
	srv.readRequest(8)

	// One RECORD data reply carrying a server event: a KeyPress at (10, 20)
	// followed by a byte the core protocol doesn't define, which is skipped.
	reply := make([]byte, 32+64)
	reply[0] = 1
	reply[1] = recordFromServer
	le.PutUint32(reply[4:], 64/4) // length is in 4-byte units of the data past the 32-byte header
	ev := reply[32:64]
	ev[0] = xKeyPress
	ev[1] = 38 // keycode
	le.PutUint16(ev[20:], 10)
	le.PutUint16(ev[22:], 20)
	junk := reply[64:96]
	junk[0] = 0xff // outside xKeyPress…xButtonRelease: dropped, not passed to fn
	srv.conn.Write(reply)

	// A RECORD data reply carrying a client's ChangeKeyboardMapping request,
	// remapping keycode 38 to a new keysym.
	remapData := make([]byte, 8+4)
	remapData[0] = xChangeKeyboardMapping
	remapData[1] = 1 // one keycode
	le.PutUint16(remapData[2:], uint16(len(remapData)/4))
	remapData[4] = 38 // firstKeycode
	remapData[5] = 1  // keysyms per keycode
	le.PutUint32(remapData[8:], 'Z')
	creply := make([]byte, 32+len(remapData))
	creply[0] = 1
	creply[1] = recordFromClient
	le.PutUint32(creply[4:], uint32(len(remapData)/4))
	copy(creply[32:], remapData)
	srv.conn.Write(creply)

	// A second server event, so fn's second call (which stops the loop) sees
	// the remap already applied.
	reply2 := make([]byte, 32+32)
	reply2[0] = 1
	reply2[1] = recordFromServer
	le.PutUint32(reply2[4:], 32/4)
	ev2 := reply2[32:64]
	ev2[0] = xButtonPress
	ev2[1] = 38
	srv.conn.Write(reply2)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("start() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start() didn't return")
	}

	if len(got) != 2 {
		t.Fatalf("fn was called %d times, want 2: %+v", len(got), got)
	}
	if got[0].kind != xKeyPress || got[0].detail != 38 || got[0].x != 10 || got[0].y != 20 {
		t.Errorf("first event = %+v, want a KeyPress at (10, 20) on keycode 38", got[0])
	}
	if got[1].kind != xButtonPress || got[1].detail != 38 {
		t.Errorf("second event = %+v, want a ButtonPress on keycode 38", got[1])
	}
	if syms := x.keysyms[38]; len(syms) != 1 || syms[0] != 'Z' {
		t.Errorf("keysyms[38] after the remap = %v, want [Z]", syms)
	}
}

func TestXRecorderStartRefused(t *testing.T) {
	x, srv := newXServer(t)
	x.idBase, x.record = 0x400, 42
	done := make(chan error, 1)
	go func() { done <- x.start(func(rawInput) bool { return true }) }()
	srv.readRequest(20 + 4 + 24)
	srv.readRequest(8)
	reply := make([]byte, 32)
	reply[0], reply[1] = 0, 3
	srv.conn.Write(reply)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("start() with a refusal: want an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start() didn't return")
	}
}

func TestXRecorderRoundTripError(t *testing.T) {
	x, srv := newXServer(t)
	done := make(chan error, 1)
	go func() {
		_, err := x.roundTrip([]byte{99, 0, 1, 0})
		done <- err
	}()
	srv.readRequest(4)
	reply := make([]byte, 32)
	reply[0], reply[1] = 0, 5 // an X error, code 5
	srv.conn.Write(reply)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("roundTrip() with an error reply: want an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("roundTrip() didn't return")
	}
}

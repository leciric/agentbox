package devicestream

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestMain runs as a stand-in for ffmpeg when FAKE_FFMPEG is set: it takes
// the -s WxH of EncoderArgs, then writes an FLV stream with a configuration
// and, for each frame in, one tag whose payload is the frame's first byte,
// the first a keyframe.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_FFMPEG") == "1" {
		fakeFFmpeg()
		return
	}
	os.Exit(m.Run())
}

func fakeFFmpeg() {
	var w, h int
	for i, arg := range os.Args {
		if arg == "-s" {
			_, _ = fmt.Sscanf(os.Args[i+1], "%dx%d", &w, &h)
		}
	}
	out := bufio.NewWriter(os.Stdout)
	_, _ = out.Write([]byte{'F', 'L', 'V', 1, 1, 0, 0, 0, 9, 0, 0, 0, 0})
	_, _ = out.Write(flvTag(9, []byte{0x17, 0, 0, 0, 0, 1, 0x42, 0xc0, 0x1f, byte(w / 16), byte(h / 16)}))
	_ = out.Flush()
	frame := make([]byte, w*h*3)
	for n := 0; ; n++ {
		if _, err := io.ReadFull(os.Stdin, frame); err != nil {
			return
		}
		kind := byte(0x27)
		if n == 0 {
			kind = 0x17
		}
		_, _ = out.Write(flvTag(9, []byte{kind, 1, 0, 0, 0, 0, 0, 0, 1, frame[0]}))
		_ = out.Flush()
	}
}

func useFakeFFmpeg(t *testing.T) {
	t.Helper()
	old := FFmpeg
	FFmpeg = os.Args[0]
	t.Setenv("FAKE_FFMPEG", "1")
	t.Cleanup(func() { FFmpeg = old })
}

// fakeDevice sends the frames given to its frames channel, and keeps the
// input it gets.
type fakeDevice struct {
	frames chan Frame
	got    chan Input
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{frames: make(chan Frame, 10), got: make(chan Input, 10)}
}

func (d *fakeDevice) Platform() string { return "test" }

func (d *fakeDevice) Screen(ctx context.Context, fn func(Frame) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f := <-d.frames:
			if err := fn(f); err != nil {
				return err
			}
		}
	}
}

func (d *fakeDevice) Input(_ context.Context, in Input) error {
	d.got <- in
	return nil
}

func frameOf(w, h int, first byte) Frame {
	p := make([]byte, w*h*3)
	p[0] = first
	return Frame{Width: w, Height: h, Pixels: p, At: time.Now()}
}

func serveFake(t *testing.T, dev Device) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Serve(w, r, dev, t.Logf)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	conn.SetReadLimit(maxTag)
	return conn
}

func read(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary {
		t.Fatalf("a %v message from the server", typ)
	}
	m, err := DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func send(t *testing.T, conn *websocket.Conn, msg string) {
	t.Helper()
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(msg)); err != nil {
		t.Fatal(err)
	}
}

func TestServe(t *testing.T) {
	useFakeFFmpeg(t)
	dev := newFakeDevice()
	conn := serveFake(t, dev)

	// The screen as it is: a config, then a keyframe.
	dev.frames <- frameOf(32, 64, 7)
	m := read(t, conn)
	if m.Config == nil || m.Config.Platform != "test" || m.Config.Codec != "avc1.42c01f" || m.Config.Width != 32 || m.Config.Height != 64 {
		t.Fatalf("first message = %+v, want the config", m)
	}
	if m = read(t, conn); m.Packet == nil || !m.Packet.Key || m.Packet.Data[len(m.Packet.Data)-1] != 7 {
		t.Fatalf("second message = %+v, want the keyframe", m.Packet)
	}
	// A change: a frame that isn't a keyframe.
	dev.frames <- frameOf(32, 64, 8)
	if m = read(t, conn); m.Packet == nil || m.Packet.Key || m.Packet.Data[len(m.Packet.Data)-1] != 8 {
		t.Fatalf("after a change = %+v", m.Packet)
	}

	// Input goes to the device; what it can't read is skipped.
	send(t, conn, `nonsense`)
	send(t, conn, `{"type":"touch","action":"down","x":0.5,"y":0.25}`)
	if in := <-dev.got; in.Type != InputTouch || in.Action != "down" || in.X != 0.5 || in.Y != 0.25 {
		t.Errorf("input = %+v", in)
	}

	// A keyframe, asked for with nothing new on the screen: the encoder
	// starts over on the last frame.
	send(t, conn, `{"type":"keyframe"}`)
	if m = read(t, conn); m.Config == nil {
		t.Fatalf("after asking for a keyframe = %+v, want a config", m)
	}
	if m = read(t, conn); m.Packet == nil || !m.Packet.Key || m.Packet.Data[len(m.Packet.Data)-1] != 8 {
		t.Fatalf("after asking for a keyframe = %+v, want the last frame as a keyframe", m.Packet)
	}

	// The device turns: a new size starts over.
	dev.frames <- frameOf(64, 32, 9)
	if m = read(t, conn); m.Config == nil || m.Config.Width != 64 || m.Config.Height != 32 {
		t.Fatalf("after turning = %+v, want a 64x32 config", m.Config)
	}
	if m = read(t, conn); m.Packet == nil || !m.Packet.Key {
		t.Fatalf("after turning = %+v, want a keyframe", m.Packet)
	}

	// An odd size is cut to even, which H.264's 4:2:0 needs.
	dev.frames <- frameOf(33, 65, 1)
	if m = read(t, conn); m.Config == nil || m.Config.Width != 32 || m.Config.Height != 64 {
		t.Fatalf("an odd size = %+v, want a 32x64 config", m.Config)
	}
}

func TestServeSaysWhyItEnds(t *testing.T) {
	old := FFmpeg
	FFmpeg = "/nonexistent/ffmpeg"
	t.Cleanup(func() { FFmpeg = old })
	dev := newFakeDevice()
	conn := serveFake(t, dev)
	dev.frames <- frameOf(32, 64, 7)
	if m := read(t, conn); !strings.Contains(m.Err, "no ffmpeg") {
		t.Errorf("status = %+v, want it to say there's no ffmpeg", m)
	}
}

func TestRelay(t *testing.T) {
	useFakeFFmpeg(t)
	dev := newFakeDevice()
	// The stream server, reached over a connection as the daemon reaches it
	// through the agent's proxy device.
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Serve(w, r, dev, t.Logf)
	}))
	t.Cleanup(stream.Close)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := net.Dial("tcp", strings.TrimPrefix(stream.URL, "http://"))
		if err != nil {
			t.Error(err)
			return
		}
		device, err := Dial(r.Context(), conn)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = device.CloseNow() }()
		viewer, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = viewer.CloseNow() }()
		_ = Relay(r.Context(), viewer, device)
	}))
	t.Cleanup(daemon.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(daemon.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	dev.frames <- frameOf(32, 64, 7)
	if m := read(t, conn); m.Config == nil {
		t.Fatalf("through the relay: %+v, want a config", m)
	}
	if m := read(t, conn); m.Packet == nil || !m.Packet.Key {
		t.Fatalf("through the relay: %+v, want a keyframe", m)
	}
	send(t, conn, `{"type":"button","button":"home"}`)
	if in := <-dev.got; in.Type != InputButton || in.Button != "home" {
		t.Errorf("input through the relay = %+v", in)
	}
}

func TestDialSaysWhyTheServerRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the emulator isn't running", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(context.Background(), conn); err == nil || err.Error() != "the emulator isn't running" {
		t.Errorf("Dial = %v", err)
	}
}

// TestEncoderWithFFmpeg encodes with this machine's ffmpeg, when it has
// libx264: the first frame is a keyframe with its configuration first, and
// each frame in comes out as one frame, as soon as it is in.
func TestEncoderWithFFmpeg(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := exec.LookPath(FFmpeg); err != nil {
		t.Skip("no ffmpeg")
	}
	if err := ffmpegHasX264(ctx); err != nil {
		t.Skip(err)
	}
	enc, err := StartEncoder(ctx, 64, 128)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	pixels := make([]byte, 64*128*3)
	if err := enc.Encode(pixels, time.Now()); err != nil {
		t.Fatal(err)
	}
	avcC, _, err := enc.Next()
	if err != nil || avcC == nil {
		t.Fatalf("first out: %v, %v; want the configuration", avcC, err)
	}
	if codec, _ := CodecString(avcC); !strings.HasPrefix(codec, "avc1.42") {
		t.Errorf("codec = %s, want Baseline (avc1.42…)", codec)
	}
	_, p, err := enc.Next()
	if err != nil || !p.Key {
		t.Fatalf("first frame: %+v, %v; want a keyframe", p, err)
	}
	if n := binary.BigEndian.Uint32(p.Data); int(n) > len(p.Data)-4 {
		t.Errorf("the frame isn't AVCC: its first NAL unit is %d bytes of %d", n, len(p.Data))
	}
	// Not held back until another frame comes: it is out on its own.
	for i := range pixels {
		pixels[i] = byte(i)
	}
	if err := enc.Encode(pixels, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, p, err = enc.Next(); err != nil || p.Key {
		t.Fatalf("second frame: %+v, %v; want a frame that isn't a keyframe", p.Key, err)
	}
}

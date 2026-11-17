package emulator

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"agentbox/internal/devicestream"
)

func TestParseDiscovery(t *testing.T) {
	ep := ParseDiscovery([]byte("grpc.port=8554\ngrpc.token=s3cret\navd.id=agentbox\ncmdline=\"emulator\" \"-avd\"\nnonsense\n"))
	if ep.Port != 8554 || ep.Token != "s3cret" || ep.AVD != "agentbox" {
		t.Errorf("ParseDiscovery = %+v", ep)
	}
}

func TestFind(t *testing.T) {
	dead, live := t.TempDir(), t.TempDir()
	// A file left by an emulator that has gone: pid 0 is nobody's.
	if err := os.WriteFile(filepath.Join(dead, "pid_0.ini"), []byte("grpc.port=1\navd.id=agentbox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Find([]string{dead}, ""); err == nil {
		t.Error("found an emulator that isn't running")
	}
	file := filepath.Join(live, fmt.Sprintf("pid_%d.ini", os.Getpid()))
	if err := os.WriteFile(file, []byte("grpc.port=8554\ngrpc.token=t\navd.id=agentbox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ep, err := Find([]string{"/nonexistent", dead, live}, "agentbox")
	if err != nil || ep.Port != 8554 || ep.Token != "t" || ep.PID != os.Getpid() {
		t.Errorf("Find = %+v, %v", ep, err)
	}
	if _, err := Find([]string{live}, "another"); err == nil {
		t.Error("found another AVD's emulator")
	}
}

func TestRunningDirs(t *testing.T) {
	env := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000", "HOME": "/home/u"}
	dirs := RunningDirs(func(k string) string { return env[k] }, 1000)
	want := []string{"/run/user/1000/avd/running", "/run/user/1000/avd/running", "/home/u/.android/avd/running"}
	if strings.Join(dirs, ",") != strings.Join(want, ",") {
		t.Errorf("RunningDirs = %v", dirs)
	}
}

// image is an Image message.
func image(w, h int, rotation Rotation, pixels []byte) []byte {
	var rot, format, b []byte
	rot = protowire.AppendTag(rot, 1, protowire.VarintType)
	rot = protowire.AppendVarint(rot, uint64(rotation))
	format = protowire.AppendTag(format, 2, protowire.BytesType)
	format = protowire.AppendBytes(format, rot)
	format = protowire.AppendTag(format, 3, protowire.VarintType)
	format = protowire.AppendVarint(format, uint64(w))
	format = protowire.AppendTag(format, 4, protowire.VarintType)
	format = protowire.AppendVarint(format, uint64(h))
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendBytes(b, format)
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	b = protowire.AppendBytes(b, pixels)
	b = protowire.AppendTag(b, 5, protowire.VarintType)
	b = protowire.AppendVarint(b, 3)
	return b
}

func TestParseImage(t *testing.T) {
	img, err := parseImage(image(2, 1, Landscape, []byte{1, 2, 3, 4, 5, 6}))
	if err != nil || img.Width != 2 || img.Height != 1 || img.Rotation != Landscape || img.Seq != 3 || len(img.Pixels) != 6 {
		t.Errorf("parseImage = %+v, %v", img, err)
	}
	if _, err := parseImage(image(2, 2, Portrait, []byte{1, 2, 3})); err == nil {
		t.Error("took an image with too few pixels")
	}
	if _, err := parseImage(image(0, 0, Portrait, nil)); err == nil {
		t.Error("took an empty image")
	}
	if _, err := parseImage([]byte{0x0a, 0x10}); err == nil {
		t.Error("took a truncated message")
	}
}

// call is one call the fake emulator got.
type call struct {
	method string
	msg    []byte
	auth   string
}

// fakeEmulator serves EmulatorController over h2c: each call is recorded, and
// answered by reply (one message each) or, for streamScreenshot, by screens.
type fakeEmulator struct {
	mu      sync.Mutex
	calls   []call
	screens [][]byte
	status  []byte
	fail    string
}

func (f *fakeEmulator) serve(t *testing.T) *Client {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		method := strings.TrimPrefix(r.URL.Path, service)
		var msg []byte
		if len(body) >= 5 {
			msg = body[5:]
		}
		f.mu.Lock()
		f.calls = append(f.calls, call{method, msg, r.Header.Get("Authorization")})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		if f.fail != "" {
			w.Header().Set("Grpc-Status", "16")
			w.Header().Set("Grpc-Message", f.fail)
			return
		}
		write := func(m []byte) {
			_, _ = w.Write(binary.BigEndian.AppendUint32([]byte{0}, uint32(len(m))))
			_, _ = w.Write(m)
			w.(http.Flusher).Flush()
		}
		switch method {
		case "streamScreenshot":
			for _, s := range f.screens {
				write(s)
			}
		case "getStatus":
			write(f.status)
		default:
			write(nil) // Empty
		}
		w.Header().Set("Grpc-Status", "0")
	})
	srv := httptest.NewUnstartedServer(handler)
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &protocols
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	var ep Endpoint
	_, _ = fmt.Sscan(port, &ep.Port)
	ep.Token = "tok"
	c := New(ep)
	t.Cleanup(c.Close)
	return c
}

func (f *fakeEmulator) took() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

// hardwareStatus is an EmulatorStatus with a hardwareConfig of a w x h screen.
func hardwareStatus(w, h int) []byte {
	var list []byte
	for _, kv := range [][2]string{{"hw.lcd.width", fmt.Sprint(w)}, {"hw.lcd.height", fmt.Sprint(h)}, {"hw.cpu.arch", "x86_64"}} {
		var entry []byte
		entry = protowire.AppendTag(entry, 1, protowire.BytesType)
		entry = protowire.AppendString(entry, kv[0])
		entry = protowire.AppendTag(entry, 2, protowire.BytesType)
		entry = protowire.AppendString(entry, kv[1])
		list = protowire.AppendTag(list, 1, protowire.BytesType)
		list = protowire.AppendBytes(list, entry)
	}
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, "37.1.11")
	b = protowire.AppendTag(b, 5, protowire.BytesType)
	return protowire.AppendBytes(b, list)
}

func TestStreamScreen(t *testing.T) {
	f := &fakeEmulator{screens: [][]byte{image(2, 1, Portrait, make([]byte, 6)), image(1, 2, Landscape, make([]byte, 6))}}
	c := f.serve(t)
	var got []Image
	err := c.StreamScreen(context.Background(), 1600, 1600, func(img Image) error {
		got = append(got, img)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("StreamScreen ended with %v, want that the emulator stopped sending", err)
	}
	if len(got) != 2 || got[1].Rotation != Landscape {
		t.Errorf("StreamScreen got %+v", got)
	}
	calls := f.took()
	if calls[0].method != "streamScreenshot" || calls[0].auth != "Bearer tok" {
		t.Errorf("call = %+v", calls[0])
	}
	if !strings.Contains(string(calls[0].msg), string(imageFormat(1600, 1600))) {
		t.Errorf("asked for % x, want % x (RGB888, 1600x1600)", calls[0].msg, imageFormat(1600, 1600))
	}
}

func TestCallFails(t *testing.T) {
	f := &fakeEmulator{fail: "bad token"}
	c := f.serve(t)
	if err := c.SendKey(context.Background(), KeyPress, "GoHome"); err == nil || !strings.Contains(err.Error(), "not allowed (bad token)") {
		t.Errorf("SendKey = %v", err)
	}
}

func TestScreenSize(t *testing.T) {
	f := &fakeEmulator{status: hardwareStatus(1080, 2400)}
	c := f.serve(t)
	w, h, err := c.ScreenSize(context.Background())
	if err != nil || w != 1080 || h != 2400 {
		t.Errorf("ScreenSize = %d, %d, %v", w, h, err)
	}
}

func TestPanel(t *testing.T) {
	// The top left of the screen as shown, turned each way, on a 1000x2000
	// panel: a landscape screen's top left is the panel's top right.
	for _, c := range []struct {
		r      Rotation
		px, py int
	}{
		{Portrait, 100, 400},
		{Landscape, 800, 200},
		{ReversePortrait, 900, 1600},
		{ReverseLandscape, 200, 1800},
	} {
		if px, py := panel(0.1, 0.2, c.r, 1000, 2000); px != c.px || py != c.py {
			t.Errorf("panel(0.1, 0.2) turned %d = %d, %d, want %d, %d", c.r, px, py, c.px, c.py)
		}
	}
}

// fields reads a message's varint and string fields, by number.
func fieldsOf(t *testing.T, b []byte) map[protowire.Number][]any {
	t.Helper()
	out := map[protowire.Number][]any{}
	if err := fields(b, func(num protowire.Number, typ protowire.Type, v uint64, bytes []byte) {
		if typ == protowire.VarintType {
			out[num] = append(out[num], int64(v))
		} else {
			out[num] = append(out[num], bytes)
		}
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDeviceInput(t *testing.T) {
	f := &fakeEmulator{status: hardwareStatus(1000, 2000), screens: [][]byte{image(2, 1, Landscape, make([]byte, 6))}}
	d := NewDevice(f.serve(t))
	ctx := context.Background()
	// The screen, turned, tells the device how to map touches.
	_ = d.Screen(ctx, func(devicestream.Frame) error { return nil })
	for _, in := range []devicestream.Input{
		{Type: devicestream.InputTouch, Action: "down", X: 0.1, Y: 0.2, ID: 1},
		{Type: devicestream.InputTouch, Action: "up", X: 0.1, Y: 0.2, ID: 1},
		{Type: devicestream.InputKey, Action: "down", Key: "Enter"},
		{Type: devicestream.InputText, Text: "hi"},
		{Type: devicestream.InputText, Text: "olá"},
		{Type: devicestream.InputButton, Button: "back"},
		{Type: devicestream.InputButton, Button: "rotate"},
		{Type: devicestream.InputButton, Button: "nonexistent"},
		{Type: devicestream.InputWheel, X: 0.5, Y: 0.5, DY: 1},
	} {
		if err := d.Input(ctx, in); err != nil {
			t.Fatalf("Input(%+v) = %v", in, err)
		}
	}
	var methods []string
	calls := f.took()
	for _, c := range calls {
		methods = append(methods, c.method)
	}
	want := "getStatus streamScreenshot sendTouch sendTouch sendKey sendKey setClipboard sendKey sendKey sendKey sendKey setPhysicalModel sendMouse injectWheel"
	if strings.Join(methods, " ") != want {
		t.Fatalf("calls = %s\nwant    %s", strings.Join(methods, " "), want)
	}

	down, up := fieldsOf(t, fieldsOf(t, calls[2].msg)[1][0].([]byte)), fieldsOf(t, fieldsOf(t, calls[3].msg)[1][0].([]byte))
	if down[1][0] != int64(800) || down[2][0] != int64(200) || down[3][0] != int64(1) || down[4][0] != int64(1) {
		t.Errorf("touch down = %v, want 800, 200 on the panel, finger 1, pressed", down)
	}
	if up[4][0] != int64(0) {
		t.Errorf("touch up = %v, want no pressure", up)
	}
	if key := fieldsOf(t, calls[4].msg); string(key[4][0].([]byte)) != "Enter" || key[2][0] != int64(KeyDown) {
		t.Errorf("key = %v, want Enter down", key)
	}
	if text := fieldsOf(t, calls[5].msg); string(text[5][0].([]byte)) != "hi" {
		t.Errorf("text = %v, want hi typed", text)
	}
	if clip := fieldsOf(t, calls[6].msg); string(clip[1][0].([]byte)) != "olá" {
		t.Errorf("clipboard = %v, want the text that isn't ASCII", clip)
	}
	if paste := fieldsOf(t, calls[8].msg); string(paste[4][0].([]byte)) != "v" {
		t.Errorf("paste = %v, want Control-v", paste)
	}
	if back := fieldsOf(t, calls[10].msg); string(back[4][0].([]byte)) != "GoBack" {
		t.Errorf("back = %v", back)
	}
	// Turned from landscape, rotate turns it upright.
	rot := fieldsOf(t, calls[11].msg)
	values := fieldsOf(t, rot[3][0].([]byte))[1][0].([]byte)
	if rot[1][0] != int64(1) || math.Float32frombits(binary.LittleEndian.Uint32(values[8:])) != 0 {
		t.Errorf("rotate = %v (z % x), want ROTATION to 0", rot, values[8:])
	}
	if wheel := fieldsOf(t, calls[13].msg); wheel[2][0] != int64(-120) {
		t.Errorf("wheel = %v, want dy -120: scrolling down", wheel)
	}
}

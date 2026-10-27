package iossim

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"agentbox/internal/api"
	"agentbox/internal/devicestream"
)

// testHelper is the helper's API on an httptest server, with a client that
// reaches it as the daemon would.
type testHelper struct {
	xcrun   *fakeXcrun
	simhost *fakeSimhost
	server  *Server
	client  *Client
	root    string
	url     string
}

func newTestHelper(t *testing.T) *testHelper {
	t.Helper()
	h := &testHelper{xcrun: &fakeXcrun{}, simhost: newFakeSimhost(), root: t.TempDir()}
	h.server = NewServer("secret", "1.2.3", Simctl{Run: h.xcrun.run}, []string{h.root}, h.simhost.launch)
	ts := httptest.NewServer(h.server.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { h.server.hosts.stop(testUDID) })
	h.url = ts.URL
	u, _ := url.Parse(ts.URL)
	port, _ := strconv.Atoi(u.Port())
	file := StateFile(t.TempDir())
	if err := writeState(file, State{Port: port, Token: "secret", Host: u.Hostname(), Version: "1.2.3"}); err != nil {
		t.Fatal(err)
	}
	h.client = NewClient(file)
	return h
}

func TestTheHelperWantsItsToken(t *testing.T) {
	h := newTestHelper(t)
	for _, auth := range []string{"", "Bearer wrong", "secret"} {
		req, _ := http.NewRequest(http.MethodGet, h.url+"/v1/devices", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization %q: %d, want 401", auth, resp.StatusCode)
		}
	}
	if len(h.xcrun.ran()) != 0 {
		t.Errorf("ran %v without the token", h.xcrun.ran())
	}
}

func TestDevicesAreTheAvailableIOSOnesBootedFirst(t *testing.T) {
	h := newTestHelper(t)
	devices, err := h.client.Devices(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range devices {
		names = append(names, d.Name+" "+d.Runtime+" "+d.State)
	}
	want := []string{"iPhone 17 Pro iOS 26.0 Booted", "iPad Air iOS 26.0 Shutdown", "iPhone 16 iOS 18.4 Shutdown"}
	if strings.Join(names, ", ") != strings.Join(want, ", ") {
		t.Errorf("devices = %v, want %v", names, want)
	}
}

func TestHealthSaysWhenXcodeIsMissing(t *testing.T) {
	h := newTestHelper(t)
	got, err := h.client.Health(testContext(t))
	if err != nil || got.Xcode != "Xcode 26.1" || got.Problem != "" || got.Version != "1.2.3" {
		t.Fatalf("health = %+v, %v", got, err)
	}
	h2 := newTestHelper(t)
	h2.xcrun.noXcode = true
	got, err = h2.client.Health(testContext(t))
	if err != nil || !strings.Contains(got.Problem, "install Xcode") {
		t.Errorf("health without Xcode = %+v, %v", got, err)
	}
}

func TestBootAndShutdownRunSimctlOnTheUDID(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	if err := h.client.Boot(ctx, testUDID); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Shutdown(ctx, testUDID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"xcrun simctl boot " + testUDID, "xcrun simctl bootstatus " + testUDID + " -b", "xcrun simctl shutdown " + testUDID} {
		if !h.xcrun.ranLike(want) {
			t.Errorf("didn't run %q: %v", want, h.xcrun.ran())
		}
	}
	h.xcrun.failBoot = errors.New("xcrun simctl: Unable to boot device in current state: Booted")
	if err := h.client.Boot(ctx, testUDID); err != nil {
		t.Errorf("booting a booted simulator: %v", err)
	}
	if err := h.client.Boot(ctx, "not-a-udid; rm -rf /"); err == nil {
		t.Error("booted something that isn't a UDID")
	}
}

func TestInstallTakesOnlyAppsInTheRoots(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	app := appDir(t, h.root, "pawly/agent-01/build/Pawly.app")
	if err := h.client.Install(ctx, testUDID, app); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(app)
	if !h.xcrun.ranLike("xcrun simctl install " + testUDID + " " + real) {
		t.Errorf("didn't install %s: %v", real, h.xcrun.ran())
	}
	outside := appDir(t, t.TempDir(), "Evil.app")
	link := filepath.Join(h.root, "pawly/agent-01/Link.app")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	notApp := appDir(t, h.root, "pawly/agent-01/build/Pawly")
	for path, want := range map[string]int{outside: 403, link: 403, notApp: 400, "relative/Pawly.app": 400, filepath.Join(h.root, "missing.app"): 400} {
		err := h.client.Install(ctx, testUDID, path)
		var se *api.StatusError
		if !errors.As(err, &se) || se.Code != want {
			t.Errorf("install %s: %v, want %d", path, err, want)
		}
	}
}

func TestLaunchAndOpenURLCheckTheirArguments(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	out, err := h.client.Launch(ctx, testUDID, api.IOSLaunchRequest{BundleID: "com.example.app", Args: []string{"--demo"}})
	if err != nil || out.Output != "com.example.app: 4242" {
		t.Fatalf("launch: %+v, %v", out, err)
	}
	if !h.xcrun.ranLike("xcrun simctl launch --terminate-running-process " + testUDID + " com.example.app --demo") {
		t.Errorf("launch ran %v", h.xcrun.ran())
	}
	if _, err := h.client.Launch(ctx, testUDID, api.IOSLaunchRequest{BundleID: "com.example.app", Terminate: true}); err != nil || !h.xcrun.ranLike("xcrun simctl terminate "+testUDID+" com.example.app") {
		t.Errorf("terminate: %v, %v", err, h.xcrun.ran())
	}
	if _, err := h.client.Launch(ctx, testUDID, api.IOSLaunchRequest{BundleID: "--help"}); err == nil {
		t.Error("launched a flag")
	}
	if err := h.client.OpenURL(ctx, testUDID, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "-h", "no scheme"} {
		if err := h.client.OpenURL(ctx, testUDID, bad); err == nil {
			t.Errorf("opened %q", bad)
		}
	}
}

func TestInputGoesToSimhost(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	done := make(chan error, 1)
	go func() { done <- h.client.Input(ctx, testUDID, api.IOSInput{Type: "tap", X: 100, Y: 200, Pixels: true}) }()
	// A tap in pixels waits for simhost to say how big the screen is.
	h.simhost.waitLaunched(t)
	h.simhost.screen(t, 1000, 2000)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	down, up := h.simhost.next(t), h.simhost.next(t)
	if down != (op{Op: "touch", Phase: "down", X: 0.1, Y: 0.1}) || up.Phase != "up" {
		t.Errorf("simhost got %+v then %+v", down, up)
	}
	// Text a US keyboard can't type is pasted.
	if err := h.client.Input(ctx, testUDID, api.IOSInput{Type: "text", Text: "café"}); err != nil {
		t.Fatal(err)
	}
	if h.xcrun.pasted != "café" {
		t.Errorf("pasted %q", h.xcrun.pasted)
	}
	if o := h.simhost.next(t); o.Usage != 0xe3 || !o.Down {
		t.Errorf("paste began with %+v, want ⌘ down", o)
	}
	h.simhost.mu.Lock()
	launched := h.simhost.launched
	h.simhost.mu.Unlock()
	if launched != 1 {
		t.Errorf("simhost launched %d times, want once", launched)
	}
}

func TestScreenshotIsSimctlsPNG(t *testing.T) {
	h := newTestHelper(t)
	png, err := h.client.Screenshot(testContext(t), testUDID)
	if err != nil || string(png) != "\x89PNG fake" {
		t.Fatalf("screenshot = %q, %v", png, err)
	}
}

func readMessage(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	typ, data, err := conn.Read(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	return typ, data
}

func decode(t *testing.T, conn *websocket.Conn) devicestream.Message {
	t.Helper()
	typ, data := readMessage(t, conn)
	if typ != websocket.MessageBinary {
		t.Fatalf("a %s message: %s", typ, data)
	}
	m, err := devicestream.DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const testConfig = `{"codec":"avc1.4d0028","width":884,"height":1920,"description":"AU0AKP/hAAA=","hardware":true}`

func TestViewStreamsDeviceStreamsWireFormatFromAKeyframe(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	conn, err := h.client.View(ctx, testUDID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if o := h.simhost.next(t); o != (op{Op: "stream", On: true}) {
		t.Fatalf("the first viewer sent %+v, want the stream on", o)
	}
	h.simhost.write(t, kindConfig, []byte(testConfig))
	h.simhost.write(t, kindFrame, frame(false, 1, "late"))
	h.simhost.write(t, kindFrame, frame(true, 1_761_000_000_000_000, "key"))
	h.simhost.write(t, kindFrame, frame(false, 1_761_000_000_016_000, "delta"))

	m := decode(t, conn)
	if c := m.Config; c == nil || c.V != devicestream.Version || c.Platform != "ios" || c.Codec != "avc1.4d0028" || c.Width != 884 || c.Height != 1920 || len(c.Description) != 8 {
		t.Fatalf("first message %+v, want the config", m)
	}
	// The frame before the keyframe is no use to it, so it doesn't get it.
	if p := decode(t, conn).Packet; p == nil || !p.Key || string(p.Data) != "key" || p.At.UnixMicro() != 1_761_000_000_000_000 {
		t.Fatalf("got %+v, want the keyframe", p)
	}
	if p := decode(t, conn).Packet; p == nil || p.Key || string(p.Data) != "delta" {
		t.Fatalf("got %+v, want the next frame", p)
	}

	// A second viewer gets the config it missed, and asks for a keyframe.
	second, err := h.client.View(ctx, testUDID)
	if err != nil {
		t.Fatal(err)
	}
	if m := decode(t, second); m.Config == nil {
		t.Errorf("the second viewer's first message is %+v", m)
	}
	if o := h.simhost.next(t); o.Op != "keyframe" {
		t.Errorf("a second viewer sent %+v, want a keyframe", o)
	}
	_ = second.CloseNow()

	// The viewer's input, as DeviceStreamView sends it.
	for _, in := range []string{
		`{"type":"touch","action":"down","x":0.25,"y":0.75,"id":0}`,
		`{"type":"key","action":"down","key":"Enter"}`,
		`{"type":"button","button":"back"}`,
		`{"type":"keyframe"}`,
	} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(in)); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []op{{Op: "touch", Phase: "down", X: 0.25, Y: 0.75}, {Op: "key", Usage: 0x28, Down: true}, {Op: "keyframe"}} {
		if o := h.simhost.next(t); o != want {
			t.Errorf("simhost got %+v, want %+v", o, want)
		}
	}

	// An input that failed doesn't end the stream; simhost stopping does, saying why.
	h.simhost.write(t, kindEvent, []byte(`{"error":"dtuhidd didn't answer"}`))
	h.simhost.write(t, kindFrame, frame(false, 3, "still"))
	if p := decode(t, conn).Packet; p == nil || string(p.Data) != "still" {
		t.Fatalf("after an input error, got %+v", p)
	}
	h.server.hosts.stop(testUDID)
	if m := decode(t, conn); !strings.Contains(m.Err, "dtuhidd didn't answer") {
		t.Errorf("simhost stopping said %+v", m)
	}
}

func TestTheLastViewerLeavingStopsTheStream(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	conn, err := h.client.View(ctx, testUDID)
	if err != nil {
		t.Fatal(err)
	}
	if o := h.simhost.next(t); !o.On {
		t.Fatalf("got %+v", o)
	}
	_ = conn.CloseNow()
	if o := h.simhost.next(t); o != (op{Op: "stream"}) {
		t.Errorf("the last viewer leaving sent %+v, want the stream off", o)
	}
}

func TestABadConfigFromSimhostStopsIt(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	conn, err := h.client.View(ctx, testUDID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	h.simhost.next(t)
	h.simhost.write(t, kindConfig, []byte(`{"codec":""}`))
	if m := decode(t, conn); !strings.Contains(m.Err, "config") {
		t.Errorf("got %+v, want why it stopped", m)
	}
}

func TestShutdownStopsSimhost(t *testing.T) {
	h := newTestHelper(t)
	ctx := testContext(t)
	if err := h.client.Input(ctx, testUDID, api.IOSInput{Type: "key", Key: "a"}); err != nil {
		t.Fatal(err)
	}
	h.simhost.mu.Lock()
	stopped := h.simhost.stopped
	h.simhost.mu.Unlock()
	if err := h.client.Shutdown(ctx, testUDID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("shutting the simulator down left its simhost running")
	}
}

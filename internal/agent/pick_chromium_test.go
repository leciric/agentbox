package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The picker against a real Chromium, which CI doesn't have: set
// AGENTBOX_CHROMIUM to one (/usr/bin/chromium) to run these.

const pickPage = `<!doctype html><html><head><title>Pick me</title><style>
body { margin: 0; height: 3000px; font-family: sans-serif; }
#go { position: absolute; top: 600px; left: 100px; width: 120px; height: 40px; background: rgb(255, 0, 0); color: white; border: 0; border-radius: 6px; }
</style></head><body>
<main><section class="card hero"><button id="go" class="btn primary" onclick="document.title = 'clicked'">Save changes</button></section></main>
<script>scrollTo(0, 500)</script>
</body></html>`

func startChromium(t *testing.T) (*http.Client, []BrowserPage) {
	t.Helper()
	bin := os.Getenv("AGENTBOX_CHROMIUM")
	if bin == "" {
		t.Skip("AGENTBOX_CHROMIUM isn't set")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(pickPage)) }))
	t.Cleanup(site.Close)
	// Not t.TempDir: Chromium's helpers still write the profile for a moment
	// after it is killed, which fails TempDir's cleanup.
	profile, err := os.MkdirTemp("", "pick-chromium-")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--headless=new", "--no-sandbox", "--remote-debugging-port=0", "--user-data-dir="+profile, "--window-size=800,600", "--hide-scrollbars", site.URL)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		time.Sleep(200 * time.Millisecond)
		_ = os.RemoveAll(profile)
	})
	var port string
	for deadline := time.Now().Add(15 * time.Second); port == "" && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")); err == nil {
			port = strings.SplitN(string(b), "\n", 2)[0]
		}
	}
	if port == "" {
		t.Fatal("Chromium didn't say its DevTools port")
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		resp, err := http.Get("http://127.0.0.1:" + port + "/json/list")
		if err != nil {
			continue
		}
		var targets []struct{ ID, Type, URL, Title, WebSocketDebuggerURL string }
		_ = json.NewDecoder(resp.Body).Decode(&targets)
		_ = resp.Body.Close()
		for _, tg := range targets {
			if tg.Type == "page" && tg.Title == "Pick me" {
				return http.DefaultClient, []BrowserPage{{ID: tg.ID, URL: tg.URL, webSocket: tg.WebSocketDebuggerURL}}
			}
		}
	}
	t.Fatal("the page didn't load")
	return nil, nil
}

// input moves, clicks or presses keys on the page, as the user's VNC
// pointer and keyboard would.
func input(t *testing.T, page BrowserPage, steps ...map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocketDial(ctx, page.webSocket, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	s := &devtoolsSession{conn: conn}
	for _, step := range steps {
		method := "Input.dispatchMouseEvent"
		if _, ok := step["key"]; ok {
			method = "Input.dispatchKeyEvent"
		}
		if err := s.call(ctx, method, step, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func pickAsync(ctx context.Context, client *http.Client, pages []BrowserPage) chan any {
	done := make(chan any, 1)
	go func() {
		el, err := pickElement(ctx, client, pages, "Click an element")
		switch {
		case err != nil:
			done <- err
		case el == nil:
			done <- nil
		default:
			done <- el
		}
	}()
	time.Sleep(time.Second) // the overlay is on the page
	return done
}

func TestPickElementInChromium(t *testing.T) {
	client, pages := startChromium(t)
	done := pickAsync(context.Background(), client, pages)
	// The button is at 100,600 in the document, scrolled by 500: 160,120 is its middle.
	input(t, pages[0],
		map[string]any{"type": "mouseMoved", "x": 160, "y": 120},
		map[string]any{"type": "mousePressed", "x": 160, "y": 120, "button": "left", "clickCount": 1},
		map[string]any{"type": "mouseReleased", "x": 160, "y": 120, "button": "left", "clickCount": 1},
	)
	var got any
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("nothing picked")
	}
	switch v := got.(type) {
	case error:
		t.Fatal(v)
	case nil:
		t.Fatal("cancelled")
	}
	b, _ := json.MarshalIndent(got, "", "  ")
	out := string(b)
	for _, want := range []string{`"selector": "#go"`, `"tag": "button"`, `"text": "Save changes"`, `"background-color"`, `rgb(255, 0, 0)`, `"y": 100`, `"title": "Pick me"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the pick has no %s:\n%.1500s", want, out)
		}
	}
	var parsed struct {
		Image struct{ MimeType, Data string }
	}
	_ = json.Unmarshal(b, &parsed)
	raw, err := base64.StdEncoding.DecodeString(parsed.Image.Data)
	if err != nil || parsed.Image.MimeType != "image/png" {
		t.Fatalf("no PNG crop: %v %s", err, parsed.Image.MimeType)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() < 120 || b.Dy() < 40 {
		t.Errorf("crop is %v, want the 120×40 button and its margin", b)
	}
	// The crop is of the button itself, left of its label: red, so the
	// overlay was off the page.
	r, g, bl, _ := img.At(cropMargin+6, img.Bounds().Dy()/2).RGBA()
	if fmt.Sprint(r>>8, g>>8, bl>>8) != "255 0 0" {
		t.Errorf("crop's inside is %d,%d,%d, want the red button", r>>8, g>>8, bl>>8)
	}
	if testing.Verbose() {
		_ = os.WriteFile(filepath.Join(os.TempDir(), "pick-crop.png"), raw, 0o644)
	}
	// The click picked; it never reached the page.
	title, _ := evalOnce(t, pages[0], "document.title")
	if title != `"Pick me"` {
		t.Errorf("the page saw the click: title %s", title)
	}
}

func TestPickElementCancels(t *testing.T) {
	client, pages := startChromium(t)
	done := pickAsync(context.Background(), client, pages)
	input(t, pages[0], map[string]any{"type": "rawKeyDown", "key": "Escape", "code": "Escape", "windowsVirtualKeyCode": 27})
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("Escape: got %v, want nothing picked", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Escape didn't end the picker")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done = pickAsync(ctx, client, pages)
	if n, _ := evalOnce(t, pages[0], "document.querySelectorAll('agentbox-picker').length"); n != "1" {
		t.Errorf("overlays on the page: %s, want 1", n)
	}
	cancel()
	<-done
	if n, _ := evalOnce(t, pages[0], "document.querySelectorAll('agentbox-picker').length"); n != "0" {
		t.Errorf("overlays left on the page after cancelling: %s", n)
	}
}

func evalOnce(t *testing.T, page BrowserPage, expr string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocketDial(ctx, page.webSocket, http.DefaultClient)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.CloseNow() }()
	v, err := (&devtoolsSession{conn: conn}).evaluate(ctx, 0, expr)
	return string(v), err
}

// A page that loads again while picking gets the overlay back.
func TestPickElementSurvivesNavigation(t *testing.T) {
	client, pages := startChromium(t)
	done := pickAsync(context.Background(), client, pages)
	_, _ = evalOnce(t, pages[0], "location.reload()")
	time.Sleep(1500 * time.Millisecond)
	input(t, pages[0],
		map[string]any{"type": "mouseMoved", "x": 160, "y": 120},
		map[string]any{"type": "mousePressed", "x": 160, "y": 120, "button": "left", "clickCount": 1},
		map[string]any{"type": "mouseReleased", "x": 160, "y": 120, "button": "left", "clickCount": 1},
	)
	select {
	case got := <-done:
		if b, _ := json.Marshal(got); !strings.Contains(string(b), `"selector":"#go"`) {
			t.Fatalf("after the reload, got %s", b)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing picked after the page reloaded")
	}
}

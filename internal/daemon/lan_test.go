package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// phoneClient is a phone's browser: it keeps the cookie pairing gives it.
type phoneClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func newPhone(t *testing.T, base string, transport http.RoundTripper) *phoneClient {
	jar, _ := cookiejar.New(nil)
	return &phoneClient{t: t, base: base, http: &http.Client{Jar: jar, Transport: transport, Timeout: 10 * time.Second}}
}

func (p *phoneClient) do(method, path, body string, header ...string) (int, string) {
	p.t.Helper()
	req, err := http.NewRequest(method, p.base+path, strings.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := p.http.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func secretOf(t *testing.T, pairing api.LANPairing) string {
	t.Helper()
	_, secret, ok := strings.Cut(pairing.URLs[0], "#pair=")
	if !ok || secret == "" {
		t.Fatalf("no secret in %q", pairing.URLs[0])
	}
	return secret
}

func TestPhoneOnTheLocalNetwork(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	d.srv.lan.mu.Lock()
	d.srv.lan.own = true
	d.srv.lan.addresses = func() []string { return []string{"127.0.0.1"} }
	d.srv.lan.mu.Unlock()

	st, err := d.client.LAN(ctx)
	if err != nil || st.Enabled || st.Listening || st.Port != 7780 {
		t.Fatalf("LAN() at first = %+v, %v; want off on 7780", st, err)
	}
	if _, err := d.client.PairLAN(ctx); err == nil {
		t.Error("PairLAN() while off succeeded")
	}
	if _, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{Port: new(80)}); err == nil {
		t.Error("a port below 1024 was taken")
	}
	port := freePort(t)
	on := true
	st, err = d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port})
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err != nil || !st.Enabled || !st.Listening || len(st.URLs) != 1 || st.URLs[0] != base+"/" {
		t.Fatalf("UpdateLAN(on) = %+v, %v", st, err)
	}
	phone := newPhone(t, base, nil)

	// Before the desktop app has installed the web app, the page says so.
	if code, body := phone.do("GET", "/", ""); code != http.StatusServiceUnavailable || !strings.Contains(body, "Open the AgentBox app") {
		t.Errorf("GET / with no web app = %d %q", code, body)
	}
	app := newPhone(t, "http://agentbox", d.client.HTTPClient().Transport)
	put := func(version, path, body string) int {
		code, _ := app.do(http.MethodPut, "/v1/lan/web/"+version+"/files/"+path, body)
		return code
	}
	if code := put("1.0.0", "a/%2e%2e/%2e%2e/evil", "x"); code != http.StatusBadRequest {
		t.Errorf("a file outside the web app = %d", code)
	}
	if code := put("1.0.0", "web.html", "<html><head><title>x</title></head></html>"); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code := put("1.0.0", "assets/web-abc.js", "console.log(1)"); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code, body := app.do(http.MethodPost, "/v1/lan/web/1.0.0", ""); code != http.StatusNoContent {
		t.Fatal(code, body)
	}
	if st, _ := d.client.LAN(ctx); st.WebVersion != "1.0.0" {
		t.Errorf("WebVersion = %q", st.WebVersion)
	}
	if code, body := phone.do("GET", "/some/where", ""); code != 200 || !strings.Contains(body, `<head><meta name="agentbox-lan" content="1">`) {
		t.Errorf("GET a page = %d %q", code, body)
	}
	if code, body := phone.do("GET", "/assets/web-abc.js", ""); code != 200 || body != "console.log(1)" {
		t.Errorf("GET an asset = %d %q", code, body)
	}

	// Nothing works unpaired.
	if code, _ := phone.do("GET", "/api/v1/projects", ""); code != http.StatusUnauthorized {
		t.Errorf("unpaired GET /api/v1/projects = %d", code)
	}
	if code, _ := phone.do("GET", "/lan/session", ""); code != http.StatusUnauthorized {
		t.Errorf("unpaired GET /lan/session = %d", code)
	}
	if code, _ := phone.do("POST", "/lan/pair", `{"secret":"guess","name":"Pixel"}`); code != http.StatusForbidden {
		t.Errorf("pairing with a guess = %d", code)
	}

	pairing, err := d.client.PairLAN(ctx)
	if err != nil || len(pairing.QR) < 21 || !strings.HasPrefix(pairing.URLs[0], base+"/#pair=") {
		t.Fatalf("PairLAN() = %+v, %v", pairing, err)
	}
	secret := secretOf(t, pairing)
	pair := `{"secret":"` + secret + `","name":"Pixel 8"}`
	// Another site's page can't pair, even with the secret, nor by saying
	// it was forwarded.
	if code, _ := phone.do("POST", "/lan/pair", pair, "Origin", "http://evil.example"); code != http.StatusForbidden {
		t.Errorf("pairing from another origin = %d", code)
	}
	if code, _ := phone.do("POST", "/lan/pair", pair, "Origin", "http://evil.example", "X-Forwarded-Host", "evil.example"); code != http.StatusForbidden {
		t.Errorf("pairing from another origin, forwarded = %d", code)
	}
	code, body := phone.do("POST", "/lan/pair", pair, "Origin", base)
	if code != 200 || !strings.Contains(body, `"name":"Pixel 8"`) {
		t.Fatalf("pairing = %d %q", code, body)
	}
	// The secret pairs once.
	if code, _ := newPhone(t, base, nil).do("POST", "/lan/pair", pair); code != http.StatusForbidden {
		t.Errorf("pairing twice with one secret = %d", code)
	}

	if code, body := phone.do("GET", "/api/v1/projects", ""); code != 200 || !strings.HasPrefix(body, "[") {
		t.Errorf("paired GET /api/v1/projects = %d %q", code, body)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/lan"},
		{"POST", "/api/v1/lan/pairings"},
		{"POST", "/api/v1/projects"},
		{"GET", "/api/v1/agents/p/agent-01/terminal"},
		{"GET", "/api/v1/projects/p/secrets"},
		{"POST", "/api/v1/shutdown"},
		{"DELETE", "/api/v1/agents/p/agent-01"},
	} {
		if code, _ := phone.do(c.method, c.path, "{}"); code != http.StatusForbidden {
			t.Errorf("a phone's %s %s = %d, want 403", c.method, c.path, code)
		}
	}
	st, _ = d.client.LAN(ctx)
	if len(st.Phones) != 1 || st.Phones[0].Name != "Pixel 8" || st.Phones[0].LastAddr != "127.0.0.1" {
		t.Fatalf("Phones = %+v", st.Phones)
	}

	// A phone's event stream ends when it's revoked, and its cookie stops
	// working.
	req, _ := http.NewRequest("GET", base+"/api/v1/events", nil)
	stream, err := (&http.Client{Jar: phone.http.Jar}).Do(req)
	if err != nil || stream.StatusCode != 200 {
		t.Fatalf("events = %v, %v", stream, err)
	}
	ended := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, bufio.NewReader(stream.Body))
		close(ended)
	}()
	waitFor(t, "the stream to be open", func() bool {
		d.srv.lan.mu.Lock()
		defer d.srv.lan.mu.Unlock()
		return len(d.srv.lan.open[st.Phones[0].ID]) == 1
	})
	if err := d.client.RemoveLANPhone(ctx, st.Phones[0].ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Error("revoking the phone didn't end its event stream")
	}
	_ = stream.Body.Close()
	if code, _ := phone.do("GET", "/api/v1/projects", ""); code != http.StatusUnauthorized {
		t.Errorf("a revoked phone's GET = %d", code)
	}

	// Off, nothing answers.
	off := false
	if _, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := http.Get(base + "/"); err == nil {
		t.Error("the port is still open once it's off")
	}
}

// In the Linux host's VM the supervisor opens the port on the host, and
// passes requests on over the daemon's socket, saying who they came from.
func TestPhoneThroughTheVMsHost(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	d.srv.lan.mu.Lock()
	d.srv.lan.own = false
	d.srv.lan.mu.Unlock()
	on := true
	st, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on})
	if err != nil || st.Listening || !strings.Contains(st.Error, "waiting for AgentBox's VM") {
		t.Fatalf("on with no report = %+v, %v", st, err)
	}
	if err := d.client.ReportLANHost(ctx, api.LANHostReport{Port: 7780, Listening: true, Addresses: []string{"192.168.1.5"}}); err != nil {
		t.Fatal(err)
	}
	st, _ = d.client.LAN(ctx)
	if !st.Listening || st.Error != "" || len(st.URLs) != 1 || st.URLs[0] != "http://192.168.1.5:7780/" {
		t.Fatalf("after the host's report = %+v", st)
	}
	pairing, err := d.client.PairLAN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	socket := d.client.HTTPClient().Transport
	phone := newPhone(t, "http://daemon"+lanNetPrefix, socket)
	code, _ := phone.do("POST", "/lan/pair", `{"secret":"`+secretOf(t, pairing)+`","name":"iPhone"}`,
		"X-Forwarded-For", "192.168.1.77", "X-Forwarded-Host", "192.168.1.5:7780", "Origin", "http://192.168.1.5:7780")
	if code != 200 {
		t.Fatalf("pairing through the socket = %d", code)
	}
	// The jar keys cookies by host and path: the socket's requests are all
	// to "daemon".
	if code, _ := phone.do("GET", "/api/v1/agents", ""); code != 200 {
		t.Errorf("paired GET through the socket = %d", code)
	}
	st, _ = d.client.LAN(ctx)
	if len(st.Phones) != 1 || st.Phones[0].LastAddr != "192.168.1.77" {
		t.Errorf("Phones = %+v", st.Phones)
	}
	var e api.Error
	code, body := phone.do("GET", "/api/v1/remote", "")
	if code != http.StatusForbidden || json.Unmarshal([]byte(body), &e) != nil || !strings.Contains(e.Error, "on your computer") {
		t.Errorf("GET /api/v1/remote = %d %q", code, body)
	}
}

func TestLANAllowed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/v1/agents", true},
		{"GET", "/v1/projects/p/chat", true},
		{"GET", "/v1/events", true},
		{"GET", "/v1/agents/p/a/chat/images/i.png", true},
		{"POST", "/v1/projects/p/chat/messages", true},
		{"POST", "/v1/agents/p/a/chat/messages", true},
		{"POST", "/v1/agents/p/a/chat/permissions/42", true},
		{"PUT", "/v1/agents/p/a/chat/options/model", true},
		{"POST", "/v1/projects/p/questions/q1/answer", true},
		{"POST", "/v1/agents/p/a/start", true},
		{"POST", "/v1/agents/p/a/stop", false},
		{"DELETE", "/v1/agents/p/a/chat", false},
		{"DELETE", "/v1/projects/p/chat", false},
		{"POST", "/v1/projects/p/questions/q1/credential", false},
		{"GET", "/v1/agents/p/a/terminal", false},
		{"GET", "/v1/agents/p/a/browser/view", false},
		{"GET", "/v1/projects/p/secrets", false},
		{"GET", "/v1/lan", false},
		{"GET", "/v1/remote", false},
		{"GET", "/v1/jobs/j/log", false},
		{"POST", "/v1/projects/p/chat/messages/../../../shutdown", false},
		{"PATCH", "/v1/settings", false},
	} {
		if got := lanAllowed(c.method, c.path); got != c.want {
			t.Errorf("lanAllowed(%s %s) = %t, want %t", c.method, c.path, got, c.want)
		}
	}
}

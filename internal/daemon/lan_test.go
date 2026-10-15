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
	"agentbox/internal/hostos"
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
	id := st.Phones[0].ID
	if strings.Join(st.Phones[0].Scopes, ",") != "read,chat,answer" {
		t.Errorf("a new phone's scopes = %v", st.Phones[0].Scopes)
	}

	// Scopes: merging needs one pairing doesn't give, and the phone can't
	// give it to itself.
	merge := func(confirmed bool) int {
		code, _ := phone.do("POST", "/api/v1/projects/p/pulls/7/merge", `{"method":"squash","confirmed":`+strconv.FormatBool(confirmed)+`}`)
		return code
	}
	if code := merge(true); code != http.StatusForbidden {
		t.Errorf("merging with no merge scope = %d", code)
	}
	if code, _ := phone.do("PATCH", "/api/v1/lan/phones/"+id, `{"scopes":["read","merge"]}`); code != http.StatusForbidden {
		t.Errorf("a phone changing its own scopes = %d", code)
	}
	if _, err := d.client.UpdateLANPhone(ctx, id, api.UpdateLANPhoneRequest{Scopes: []string{"read", "shell"}}); err == nil {
		t.Error("a scope that isn't one was taken")
	}
	got, err := d.client.UpdateLANPhone(ctx, id, api.UpdateLANPhoneRequest{Scopes: []string{"merge", "read"}})
	if err != nil || strings.Join(got.Scopes, ",") != "read,merge" {
		t.Fatalf("UpdateLANPhone = %+v, %v", got, err)
	}
	if code, body := phone.do("GET", "/lan/session", ""); code != 200 || !strings.Contains(body, `"scopes":["read","merge"]`) {
		t.Errorf("the session after the change = %d %q", code, body)
	}
	if code, _ := phone.do("POST", "/api/v1/projects/p/chat/messages", `{"text":"hi"}`); code != http.StatusForbidden {
		t.Errorf("chatting with no chat scope = %d", code)
	}
	if code := merge(false); code != http.StatusPreconditionRequired {
		t.Errorf("an unconfirmed merge = %d", code)
	}
	// Confirmed, it reaches the API, which has no project p.
	if code := merge(true); code == http.StatusForbidden || code == http.StatusPreconditionRequired || code < 400 {
		t.Errorf("a confirmed merge of a project that doesn't exist = %d", code)
	}
	// Every write is audited, newest first; reads aren't.
	audit, err := d.client.LANPhoneAudit(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range audit.Entries {
		lines = append(lines, e.Result+" "+e.Scope+" "+e.Route+" "+e.Addr)
	}
	want := []string{
		"failed merge POST /v1/projects/p/pulls/7/merge 127.0.0.1",
		"refused merge POST /v1/projects/p/pulls/7/merge 127.0.0.1",
		"refused chat POST /v1/projects/p/chat/messages 127.0.0.1",
		"refused  PATCH /v1/lan/phones/" + id + " 127.0.0.1",
		"refused merge POST /v1/projects/p/pulls/7/merge 127.0.0.1",
		"refused  DELETE /v1/agents/p/agent-01 127.0.0.1",
	}
	if len(lines) < len(want) || strings.Join(lines[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Errorf("audit =\n%s\nwant it to start\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if len(audit.Entries) != len(want)+3 {
		t.Errorf("audit has %d entries, want the %d writes", len(audit.Entries), len(want)+3)
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

// The Mac's vz VM runs under the same supervisor as the Linux host's: once it
// reports, the port it opened on the Mac is what phones reach.
func TestPhoneThroughMacVZ(t *testing.T) {
	t.Setenv(hostos.Env, "darwin")
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	port := freePort(t)
	on := true
	st, err := d.client.UpdateLAN(ctx, api.UpdateLANRequest{Enabled: &on, Port: &port})
	if err != nil || st.Listening || !strings.Contains(st.Error, "can't reach AgentBox's VM on a Mac") {
		t.Fatalf("on with no report = %+v, %v", st, err)
	}
	if err := d.client.ReportLANHost(ctx, api.LANHostReport{Port: port, Listening: true, Addresses: []string{"192.168.1.5"}}); err != nil {
		t.Fatal(err)
	}
	st, _ = d.client.LAN(ctx)
	want := "http://192.168.1.5:" + strconv.Itoa(port) + "/"
	if !st.Listening || st.Error != "" || len(st.URLs) != 1 || st.URLs[0] != want {
		t.Fatalf("after the supervisor's report = %+v", st)
	}
	if _, err := d.client.PairLAN(ctx); err != nil {
		t.Errorf("pairing after the supervisor's report: %v", err)
	}
}

func TestLANRoute(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		method, path, scope string
	}{
		{"GET", "/v1/agents", "read"},
		{"GET", "/v1/projects/p/chat", "read"},
		{"GET", "/v1/events", "read"},
		{"GET", "/v1/agents/p/a/chat/images/i.png", "read"},
		{"GET", "/v1/projects/p/chat/files/0123456789abcdef", "read"},
		{"GET", "/v1/projects/p/pulls", "read"},
		{"POST", "/v1/usage-stats/voice", "read"},
		{"POST", "/v1/projects/p/chat/messages", "chat"},
		{"POST", "/v1/agents/p/a/chat/messages", "chat"},
		{"POST", "/v1/projects/p/chat/cancel", "chat"},
		{"POST", "/v1/agents/p/a/chat/permissions/42", "chat"},
		{"PUT", "/v1/agents/p/a/chat/options/model", "chat"},
		{"POST", "/v1/projects/p/chat/reload", "chat"},
		{"POST", "/v1/agents/p/a/start", "chat"},
		{"POST", "/v1/projects/p/questions/q1/answer", "answer"},
		{"POST", "/v1/projects/p/pulls/12/merge", "merge"},
		// Neither scoped nor never: still refused.
		{"POST", "/v1/agents/p/a/stop", ""},
		{"DELETE", "/v1/agents/p/a/chat", ""},
		{"DELETE", "/v1/projects/p/chat", ""},
		{"POST", "/v1/projects/p/pulls/12/labels", ""},
		{"PATCH", "/v1/settings", ""},
		{"POST", "/v1/shutdown", ""},
		{"POST", "/v1/projects/p/chat/messages/../../../shutdown", ""},
	} {
		got := lanRouteOf(c.method, c.path)
		if got.never != "" || got.scope != c.scope {
			t.Errorf("lanRouteOf(%s %s) = %+v, want scope %q", c.method, c.path, got, c.scope)
		}
	}
}

// What no phone may do, whatever its scopes: each kind, read and written.
func TestLANNever(t *testing.T) {
	t.Parallel()
	for what, routes := range map[string][]string{
		"secrets and credentials": {
			"GET /v1/projects/p/secrets", "PUT /v1/projects/p/secrets/K", "DELETE /v1/agents/p/a/secrets/K",
			"GET /v1/projects/p/credentials", "GET /v1/agents/p/a/credential", "POST /v1/projects/p/questions/q1/credential",
			"GET /v1/auth", "POST /v1/auth/claude/login", "POST /v1/auth/github", "DELETE /v1/auth/cursor",
			"GET /v1/projects/p/browser-cookies", "PUT /v1/projects/p/browser-cookies",
		},
		"connectors": {
			"GET /v1/connectors", "POST /v1/connectors/scan", "POST /v1/connectors/import",
			"PUT /v1/projects/p/connectors/notion/override", "GET /v1/agents/p/a/connectors",
		},
		"terminals": {"GET /v1/agents/p/a/terminal"},
		"the browser and Android views": {
			"GET /v1/agents/p/a/browser/view", "GET /v1/agents/p/a/browser", "POST /v1/agents/p/a/browser/start",
			"GET /v1/agents/p/a/android/view", "POST /v1/agents/p/a/android/start",
		},
		"pairing and the phone, remote and tunnel settings": {
			"POST /v1/lan/pairings", "GET /v1/lan", "PATCH /v1/lan", "PATCH /v1/lan/phones/phone_1", "DELETE /v1/lan/phones/phone_1",
			"GET /v1/lan/phones/phone_1/audit", "GET /v1/remote", "PUT /v1/remote", "DELETE /v1/remote",
		},
		"creating or removing projects and agents": {
			"POST /v1/projects", "DELETE /v1/projects/p", "POST /v1/projects/p/retire",
			"POST /v1/agents", "DELETE /v1/agents/p/a", "POST /v1/agents/p/a/fork", "POST /v1/agents/p/a/recreate",
		},
		"job logs": {"GET /v1/jobs/j/log"},
	} {
		for _, route := range routes {
			method, path, _ := strings.Cut(route, " ")
			if got := lanRouteOf(method, path); got.never != what {
				t.Errorf("lanRouteOf(%s) = %+v, want never %q", route, got, what)
			}
		}
	}
}

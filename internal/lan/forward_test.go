package lan

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// A Forward opens phones' port on the host while the daemon has them on,
// passes every request to the daemon's /v1/lan/net/ and nowhere else, and
// reports what it opened, and what the firewall says.
func TestForward(t *testing.T) {
	var mu sync.Mutex
	status := api.LANStatus{Enabled: true, Port: 7780}
	var reports []api.LANHostReport
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/lan":
			_ = json.NewEncoder(w).Encode(status)
		case r.Method == "PUT" && r.URL.Path == "/v1/lan/host":
			var rep api.LANHostReport
			_ = json.NewDecoder(r.Body).Decode(&rep)
			reports = append(reports, rep)
		default:
			_, _ = io.WriteString(w, r.URL.Path+" from "+r.Header.Get("X-Forwarded-For"))
		}
	}))
	defer daemon.Close()
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", daemon.Listener.Addr().String())
	}
	f := NewForward(dial, t.Logf)
	var asked []int
	// The port is reported open before the firewall answers, which on
	// Windows (PowerShell) can take longer than anyone should wait.
	reportedFirst := false
	f.Firewall = func(_ context.Context, port int) string {
		asked = append(asked, port)
		mu.Lock()
		reportedFirst = len(reports) == 1 && reports[0].Listening && reports[0].Warning == ""
		mu.Unlock()
		return "allow port 7780"
	}
	var ln net.Listener
	f.Listen = func(port int) (net.Listener, error) {
		if port != 7780 {
			t.Errorf("listening on %d", port)
		}
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		return ln, err
	}
	f.Addresses = func() []string { return []string{"192.168.1.5"} }
	ctx := context.Background()
	f.tick(ctx)
	defer f.close()
	if ln == nil {
		t.Fatal("no port opened")
	}
	mu.Lock()
	if !reportedFirst {
		t.Error("the port wasn't reported open before the firewall was asked")
	}
	if len(reports) != 2 || !reports[1].Listening || reports[1].Port != 7780 || reports[1].Addresses[0] != "192.168.1.5" ||
		reports[1].Warning != "allow port 7780" {
		t.Errorf("reports = %+v", reports)
	}
	mu.Unlock()
	f.tick(ctx) // the firewall isn't asked again so soon
	if len(asked) != 1 || asked[0] != 7780 {
		t.Errorf("the firewall was asked about %v", asked)
	}
	resp, err := http.Get("http://" + ln.Addr().String() + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "/v1/lan/net/v1/agents from 127.0.0.1" {
		t.Errorf("the daemon got %q", body)
	}

	// Off, the port closes and the daemon hears so.
	mu.Lock()
	status.Enabled = false
	mu.Unlock()
	f.tick(ctx)
	if _, err := http.Get("http://" + ln.Addr().String() + "/"); err == nil {
		t.Error("the port is still open")
	}
	mu.Lock()
	defer mu.Unlock()
	if last := reports[len(reports)-1]; len(reports) != 4 || last.Listening {
		t.Errorf("reports = %+v", reports)
	}
}

// The local MCP port opens on the loopback address while the daemon has an
// app paired for it, passes /mcp on to api.MCPLocalPath and nothing else,
// stays open when phones are off, and closes when the daemon says so.
func TestForwardMCP(t *testing.T) {
	var mu sync.Mutex
	want := api.MCPLocalHost{Port: 7782, Open: true}
	var reports []api.MCPLocalHostReport
	var got []string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/lan":
			_ = json.NewEncoder(w).Encode(api.LANStatus{})
		case r.Method == "GET" && r.URL.Path == "/v1/remote/mcp/local/host":
			_ = json.NewEncoder(w).Encode(want)
		case r.Method == "PUT" && r.URL.Path == "/v1/remote/mcp/local/host":
			var rep api.MCPLocalHostReport
			_ = json.NewDecoder(r.Body).Decode(&rep)
			reports = append(reports, rep)
		default:
			got = append(got, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer daemon.Close()
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", daemon.Listener.Addr().String())
	}
	f := NewForward(dial, t.Logf)
	f.Listen = func(int) (net.Listener, error) {
		t.Error("phones' port was opened")
		return nil, net.ErrClosed
	}
	var ln net.Listener
	f.ListenLocal = func(port int) (net.Listener, error) {
		if port != 7782 {
			t.Errorf("listening on %d", port)
		}
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		return ln, err
	}
	ctx := context.Background()
	f.tick(ctx)
	defer f.close()
	if ln == nil {
		t.Fatal("no port opened")
	}
	f.tick(ctx) // phones are off: the MCP port stays
	for _, path := range []string{"/mcp", "/mcp?x=1", "/v1/agents", "/mcp/../v1/agents", "/"} {
		req, _ := http.NewRequest("POST", "http://"+ln.Addr().String()+path, nil)
		req.Header.Set("Authorization", "Bearer abx_m_x")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if wantOK := path == "/mcp" || path == "/mcp?x=1"; (resp.StatusCode == 200) != wantOK {
			t.Errorf("POST %s: %d", path, resp.StatusCode)
		}
	}
	mu.Lock()
	if len(got) != 2 || got[0] != "POST "+api.MCPLocalPath+" Bearer abx_m_x" || got[1] != got[0] {
		t.Errorf("the daemon got %q", got)
	}
	if len(reports) != 2 || !reports[0].Listening || reports[0].Port != 7782 || reports[0].Error != "" {
		t.Errorf("reports = %+v", reports)
	}
	want.Open = false
	mu.Unlock()

	f.tick(ctx)
	if _, err := http.Post("http://"+ln.Addr().String()+"/mcp", "application/json", nil); err == nil {
		t.Error("the port is still open")
	}
	f.tick(ctx) // and nothing more is reported once it's closed
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 3 || reports[2].Listening {
		t.Errorf("reports = %+v", reports)
	}
}

package chv

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

// The supervisor opens phones' port on the host while the daemon has them on,
// passes every request to the daemon's /v1/lan/net/ and nowhere else, and
// reports what it opened.
func TestLANForward(t *testing.T) {
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
	f := newLANForward(dial, func() bool { return true }, t.Logf)
	var ln net.Listener
	f.listen = func(port int) (net.Listener, error) {
		if port != 7780 {
			t.Errorf("listening on %d", port)
		}
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		return ln, err
	}
	f.addresses = func() []string { return []string{"192.168.1.5"} }
	ctx := context.Background()
	f.tick(ctx)
	defer f.close()
	if ln == nil {
		t.Fatal("no port opened")
	}
	mu.Lock()
	if len(reports) != 1 || !reports[0].Listening || reports[0].Port != 7780 || reports[0].Addresses[0] != "192.168.1.5" {
		t.Errorf("reports = %+v", reports)
	}
	mu.Unlock()
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
	if last := reports[len(reports)-1]; len(reports) != 2 || last.Listening {
		t.Errorf("reports = %+v", reports)
	}
}

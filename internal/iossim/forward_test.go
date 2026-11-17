package iossim

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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

// A forward serves the Mac's localhost:<port> and passes requests to the
// preview proxy, naming the agent in api.PreviewHeader and keeping the Host
// the app sent. The last agent to forward a port has it; stop gives it back.
func TestForwardsReachThePreviewProxy(t *testing.T) {
	h := newTestHelper(t)
	var gotHost, gotRoute string
	preview := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotRoute = r.Host, r.Header.Get(api.PreviewHeader)
	}))
	defer preview.Close()
	h.server.forwards = newForwards(strings.TrimPrefix(preview.URL, "http://"))
	t.Cleanup(func() {
		h.server.forwards.stop(ForwardRequest{Project: "pawly", Agent: "agent-01"})
		h.server.forwards.stop(ForwardRequest{Project: "pawly", Agent: "agent-02"})
	})
	port := freePort(t)
	ctx := t.Context()

	get := func() error {
		resp, err := http.Get("http://localhost:" + strconv.Itoa(port) + "/status")
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	if _, err := h.client.Forward(ctx, ForwardRequest{Port: port, Project: "pawly", Agent: "agent-01"}, false); err != nil {
		t.Fatal(err)
	}
	if err := get(); err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(port) + ".agent-01.pawly"
	if gotRoute != want || gotHost != "localhost:"+strconv.Itoa(port) {
		t.Errorf("the preview got route %q and Host %q, want %q and localhost:%d", gotRoute, gotHost, want, port)
	}

	list, err := h.client.Forward(ctx, ForwardRequest{Port: port, Project: "pawly", Agent: "agent-02"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Agent != "pawly/agent-02" {
		t.Errorf("after another agent forwarded the port, the forwards = %+v", list)
	}
	if err := get(); err != nil || gotRoute != strconv.Itoa(port)+".agent-02.pawly" {
		t.Errorf("the port still goes to %q (%v)", gotRoute, err)
	}

	// Another agent's stop leaves it; its own gives it back.
	if list, _ := h.client.Forward(ctx, ForwardRequest{Project: "pawly", Agent: "agent-01"}, true); len(list) != 1 {
		t.Errorf("agent-01's stop took agent-02's port: %+v", list)
	}
	if list, _ := h.client.Forward(ctx, ForwardRequest{Project: "pawly", Agent: "agent-02"}, true); len(list) != 0 {
		t.Errorf("after the stop, the forwards = %+v", list)
	}
	if get() == nil {
		t.Error("the port still answers after its forward stopped")
	}
}

func TestForwardRefuses(t *testing.T) {
	h := newTestHelper(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	// A Mac lets 127.0.0.1 be bound beside a server on every address.
	everywhere, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = everywhere.Close() }()
	for name, req := range map[string]ForwardRequest{
		"a low port":                    {Port: 80, Project: "pawly", Agent: "agent-01"},
		"a bad agent":                   {Port: freePort(t), Project: "pawly", Agent: "a.b"},
		"a taken port":                  {Port: taken.Addr().(*net.TCPAddr).Port, Project: "pawly", Agent: "agent-01"},
		"an empty agent":                {Port: freePort(t), Project: "pawly"},
		"a port taken on every address": {Port: everywhere.Addr().(*net.TCPAddr).Port, Project: "pawly", Agent: "agent-01"},
	} {
		if _, err := h.client.Forward(t.Context(), req, false); err == nil {
			t.Errorf("%s was forwarded", name)
		}
	}
}

// Stopping a forward closes its WebSockets too (Metro's reloads), which
// closing its server leaves open.
func TestForwardStopClosesUpgradedConnections(t *testing.T) {
	h := newTestHelper(t)
	preview := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, conn) // held until the other side closes
	}))
	defer preview.Close()
	h.server.forwards = newForwards(strings.TrimPrefix(preview.URL, "http://"))
	port := freePort(t)
	req := ForwardRequest{Port: port, Project: "pawly", Agent: "agent-01"}
	t.Cleanup(func() { h.server.forwards.stop(req) })
	if _, err := h.client.Forward(t.Context(), req, false); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("GET /hot HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("the upgrade = %v, %v", resp, err)
	}
	if _, err := h.client.Forward(t.Context(), req, true); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("the WebSocket stayed open after the forward stopped: %v", err)
	}
}

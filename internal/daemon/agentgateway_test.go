package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/gateway"
	"agentbox/internal/incus"
	"agentbox/internal/paths"
	"agentbox/internal/state"
)

// testGatewayToken is the one the store is told about, standing in for the one
// the daemon writes into a pod's Secret.
const testGatewayToken = "0123456789abcdef0123456789abcdef"

// testGatewayRef is the agent the tests' pods belong to.
var testGatewayRef = gateway.Ref{Project: "hello-stack", Agent: "agent-01"}

// The route the daemon serves and the path a pod dials have to be the same
// string. The constant moves into package gateway in a later change, and until
// then this is what keeps the two ends together.
func TestAgentGatewayPathIsTheOnePodsDial(t *testing.T) {
	t.Parallel()
	if agentGatewayPath != gateway.Path {
		t.Fatalf("the daemon serves %s and a pod dials %s", agentGatewayPath, gateway.Path)
	}
}

// A pod with no token, or with one this daemon never issued, is refused before
// its connection is upgraded.
func TestAgentGatewayRefusesWhatItCannotAuthenticate(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	h := httptest.NewServer(d.srv.gateway)
	t.Cleanup(h.Close)

	get := func(authorization string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, h.URL+agentGatewayPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}
	if code := get(""); code != http.StatusUnauthorized {
		t.Errorf("a request with no token = %d, want 401", code)
	}
	if code := get("Bearer " + testGatewayToken); code != http.StatusUnauthorized {
		t.Errorf("a token the store doesn't know = %d, want 401", code)
	}
}

// A pod that keeps trying with a token the store doesn't know never gets a
// session, however long it waits: nothing it sends reaches the daemon.
func TestAgentGatewayNeverUpgradesAnUnknownToken(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	h := httptest.NewServer(d.srv.gateway)
	t.Cleanup(h.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pod := &gateway.Client{URL: h.URL, Token: testGatewayToken, Handle: echoExec}
	go func() { _ = pod.Run(ctx) }()

	if err := d.srv.gateway.WaitFor(ctx, testGatewayRef, time.Second); !errors.Is(err, gateway.ErrNotConnected) {
		t.Errorf("WaitFor() with a token the store doesn't know = %v, want ErrNotConnected", err)
	}
	if refs := d.srv.gateway.SessionRefs(); len(refs) != 0 {
		t.Errorf("sessions after a refused token = %v, want none", refs)
	}
}

// A token the store knows is a pod the daemon can run things in: one exec, in
// and out, which is what every other operation is built on.
func TestAgentGatewayServesATokenItIssued(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := d.srv.store.SetAgentToken(ctx, testGatewayRef.Project, testGatewayRef.Agent, testGatewayToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	startPod(t, d, d.srv.gateway, testGatewayToken)

	if out, code := execOnce(t, d.srv); out != "cat: hello\n" || code != 0 {
		t.Errorf("exec = %q, exit %d, want %q and 0", out, code, "cat: hello\n")
	}
}

// The daemon's own API serves the gateway as well, which is the route a pod
// reaches over the socket or through the tailnet, rather than one of its own.
func TestAgentGatewayIsOnTheDaemonRoutes(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := d.srv.store.SetAgentToken(ctx, testGatewayRef.Project, testGatewayRef.Agent, testGatewayToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	startPod(t, d, d.srv.routes(), testGatewayToken)

	if out, code := execOnce(t, d.srv); out != "cat: hello\n" || code != 0 {
		t.Errorf("exec over routes() = %q, exit %d, want %q and 0", out, code, "cat: hello\n")
	}
}

// The local listener is off unless Config.GatewayAddr names an address: a
// cluster on this machine asks for one, and a cluster that reaches the daemon
// through the tailnet doesn't.
func TestTheLocalGatewayListenerIsOffUnlessAskedFor(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"", "off"} {
		srv := newTestServer(t, addr)
		srv.serveAgentGateway(context.Background())
		if listening, _ := gatewayListenerState(srv); listening != "" {
			t.Errorf("Config.GatewayAddr %q opened a listener on %s", addr, listening)
		}
	}
}

// The local listener serves the gateway alone, on the address it was given, and
// a pod connects to it as it would through the tailnet.
func TestTheLocalGatewayListenerServesPods(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv.serveAgentGateway(ctx)
	t.Cleanup(srv.closeAgentGateway)
	addr, reason := gatewayListenerState(srv)
	if addr == "" || reason != "" {
		t.Fatalf("the listener = %q, %q; want an address and no error", addr, reason)
	}
	if err := srv.store.SetAgentToken(ctx, testGatewayRef.Project, testGatewayRef.Agent, testGatewayToken, time.Time{}); err != nil {
		t.Fatal(err)
	}

	// Nothing else of the API is on this port: a pod's way in is the gateway's
	// own route and the in-agent API, and a route of the rest of the daemon is
	// not served here at all.
	resp, err := http.Get("http://" + addr + "/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the listener answered /v1/version with %d %q, want 404", resp.StatusCode, body)
	}

	pod := &gateway.Client{URL: "http://" + addr, Token: testGatewayToken, Handle: echoExec}
	go func() { _ = pod.Run(ctx) }()
	if err := srv.gateway.WaitFor(ctx, testGatewayRef, 10*time.Second); err != nil {
		t.Fatalf("the pod never connected to %s: %v", addr, err)
	}
	if out, code := execOnce(t, srv); out != "cat: hello\n" || code != 0 {
		t.Errorf("exec on the local listener = %q, exit %d, want %q and 0", out, code, "cat: hello\n")
	}
}

// Removing a token takes away the session that token opened: authentication
// happens once, at connect, so the reconciler's cleanup has to close it too.
func TestRevokingATokenEndsItsSession(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.store.SetAgentToken(ctx, a.Project, a.Name, testGatewayToken, time.Time{}); err != nil {
		t.Fatal(err)
	}
	startPod(t, d, d.srv.routes(), testGatewayToken)
	if !d.srv.gateway.Connected(testGatewayRef) {
		t.Fatal("the pod never connected")
	}

	// The path the reconciler takes when the pod is gone: it removes the token
	// and must not leave the session live.
	if err := d.srv.clearPod(ctx, a); err != nil {
		t.Fatalf("clearPod() = %v", err)
	}
	if d.srv.gateway.Connected(testGatewayRef) {
		t.Error("the session is still live after its token was removed")
	}
	if _, _, err := d.srv.store.AgentForToken(ctx, testGatewayToken); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the token still authenticates: %v", err)
	}
}

// The plain-HTTP listener serves the in-agent API only on loopback, or on
// another address when the insecure opt-in asks for it: /v1/self carries the
// user's credentials through the connector relay, and this listener has no TLS.
func TestTheSelfAPIIsServedOnlyWhereItIsAllowed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		addr      string
		insecure  bool
		serveSelf bool
	}{
		{"127.0.0.1:7782", false, true},
		{"[::1]:7782", false, true},
		{"localhost:7782", false, true},
		{"0.0.0.0:7782", false, false},
		{":7782", false, false},
		{"10.0.0.5:7782", false, false},
		{"0.0.0.0:7782", true, true},
	}
	for _, tc := range cases {
		srv := newTestServer(t, tc.addr, tc.insecure)
		if got := srv.serveSelfOn(tc.addr); got != tc.serveSelf {
			t.Errorf("serveSelfOn(%q, insecure=%v) = %v, want %v", tc.addr, tc.insecure, got, tc.serveSelf)
		}
		h := srv.gatewayListenerHandler(tc.serveSelf)
		// A served /v1/self asks for the pod's token (401); one that isn't
		// served never reaches the in-agent API at all (404).
		want := http.StatusNotFound
		if tc.serveSelf {
			want = http.StatusUnauthorized
		}
		if w := podSelf(h, http.MethodGet, "/v1/self", ""); w.Code != want {
			t.Errorf("GET /v1/self on %q (insecure=%v) = %d, want %d", tc.addr, tc.insecure, w.Code, want)
		}
	}
}

// A port the daemon can't have is recorded and logged rather than ending it:
// every agent that reaches the daemon another way still works.
func TestAFailedGatewayListenIsRecordedAndSurvived(t *testing.T) {
	t.Parallel()
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	srv := newTestServer(t, taken.Addr().String())
	var logged strings.Builder
	srv.cfg.Log = &logged
	srv.serveAgentGateway(context.Background())
	if listening, reason := gatewayListenerState(srv); listening != "" || reason == "" {
		t.Fatalf("the listener = %q, %q; want no address and the reason it isn't there", listening, reason)
	}
	if !strings.Contains(logged.String(), "agent gateway off") {
		t.Errorf("the failure isn't in the daemon's log: %q", logged.String())
	}
}

// newTestServer is a daemon that is never run, for the listener alone: it opens
// the real port on a temporary state.db and nothing else of Run's.
func newTestServer(t *testing.T, gatewayAddr string, allowInsecure ...bool) *Server {
	t.Helper()
	root := t.TempDir()
	srv, err := New(Config{
		Paths:                paths.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data")},
		Incus:                incus.Client{Bin: "false"},
		GatewayAddr:          gatewayAddr,
		GatewayAllowInsecure: len(allowInsecure) > 0 && allowInsecure[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.store.Close() })
	return srv
}

// gatewayListenerState is where the local listener listens, and why it doesn't.
func gatewayListenerState(srv *Server) (addr, reason string) {
	g := &srv.gatewayListener
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addr, g.err
}

// startPod connects a pod to h with token, as a cluster would, and waits for the
// daemon to have its session.
func startPod(t *testing.T, d testDaemon, h http.Handler, token string) {
	t.Helper()
	hs := httptest.NewServer(h)
	t.Cleanup(hs.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pod := &gateway.Client{URL: hs.URL, Token: token, Handle: echoExec}
	go func() { _ = pod.Run(ctx) }()
	if err := d.srv.gateway.WaitFor(ctx, testGatewayRef, 10*time.Second); err != nil {
		t.Fatalf("the pod never connected: %v", err)
	}
}

// echoExec is a pod: it answers an exec with its argv and the line it was sent,
// which is one round trip over the whole connection.
func echoExec(_ context.Context, h gateway.Header, st *gateway.Stream) error {
	kind, payload, err := st.Read()
	if err != nil {
		return err
	}
	if kind != gateway.FrameStdin {
		return fmt.Errorf("frame %d where stdin was expected", kind)
	}
	if err := st.Write(gateway.FrameStdout, append([]byte(strings.Join(h.Argv, " ")+": "), payload...)); err != nil {
		return err
	}
	return st.Exit(0)
}

// execOnce runs one command over the gateway, which is the daemon's side of a
// whole turn: the stream's header, a line in, and what came back.
func execOnce(t *testing.T, srv *Server) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := srv.gateway.Dial(ctx, testGatewayRef, gateway.Header{Op: gateway.OpExec, Argv: []string{"cat"}})
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Write(gateway.FrameStdin, []byte("hello\n")); err != nil {
		t.Fatalf("writing to the stream: %v", err)
	}
	var out strings.Builder
	for {
		kind, payload, err := st.Read()
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		switch kind {
		case gateway.FrameStdout, gateway.FrameStderr:
			out.Write(payload)
		case gateway.FrameExit:
			return out.String(), int(payload[0])
		}
	}
}

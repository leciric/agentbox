package connectors

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/connectors/connectorstest"
	"agentbox/internal/secrets"
	"agentbox/internal/state"
)

func newService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &Service{State: st, Secrets: secrets.Store{State: st, KeyPath: filepath.Join(dir, "secrets.key")}}
	t.Cleanup(s.Close)
	return s
}

// connected is a project connector to the fake, signed in.
func connected(t *testing.T, s *Service, fake *connectorstest.Server) state.Connector {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Set(ctx, "pawly", "", "notion", api.SetConnectorRequest{URL: fake.MCP()}); err != nil {
		t.Fatal(err)
	}
	res, _, err := s.Connect(ctx, "pawly", "", "notion")
	if err != nil {
		t.Fatal(err)
	}
	code, page, err := connectorstest.SignIn(res.AuthorizationURL)
	if err != nil || code != http.StatusOK || !strings.Contains(page, "Connected") {
		t.Fatalf("signing in: %d %v\n%s", code, err, page)
	}
	c, err := s.State.Connector(ctx, "pawly", "", "notion")
	if err != nil {
		t.Fatal(err)
	}
	if status, why := s.Status(ctx, c, ""); status != api.ConnectorConnected {
		t.Fatalf("after signing in, status = %s (%s)", status, why)
	}
	return c
}

// session is a relay to connector c, with its stdin and stdout.
type session struct {
	in  *io.PipeWriter
	out *bufio.Scanner
	err chan error
}

// relayFor is a relay to connector c through a stand-in for the daemon.
func relayFor(t *testing.T, s *Service, c state.Connector) *Relay {
	t.Helper()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fresh, err := s.State.Connector(r.Context(), c.Project, c.Agent, c.Name)
		if err != nil {
			RelayError(w, http.StatusNotFound, err.Error())
			return
		}
		s.Proxy(w, r, fresh, "")
	}))
	t.Cleanup(daemon.Close)
	return &Relay{HTTP: daemon.Client(), URL: daemon.URL}
}

func relayTo(t *testing.T, s *Service, c state.Connector) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	r := relayFor(t, s, c)
	sess := &session{in: inW, out: bufio.NewScanner(outR), err: make(chan error, 1)}
	go func() {
		sess.err <- r.Serve(context.Background(), inR, outW)
		_ = outW.Close()
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return sess
}

func (s *session) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (s *session) read(t *testing.T) map[string]any {
	t.Helper()
	got := make(chan map[string]any, 1)
	go func() {
		if !s.out.Scan() {
			got <- nil
			return
		}
		var m map[string]any
		_ = json.Unmarshal(s.out.Bytes(), &m)
		got <- m
	}()
	select {
	case m := <-got:
		if m == nil {
			t.Fatal("the relay's stdout ended")
		}
		return m
	case <-time.After(10 * time.Second):
		t.Fatal("nothing came out of the relay")
	}
	return nil
}

const (
	initializeMsg  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"2"}}}`
	initializedMsg = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	listMsg        = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
)

func callMsg(id int, query string) string {
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "notion-search", "arguments": map[string]any{"query": query}}})
	return string(raw)
}

func text(t *testing.T, m map[string]any) string {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("not a result: %v", m)
	}
	content := res["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

// The whole of it against a server shaped like Notion's: discovery,
// registration, PKCE, the browser coming back, and then an MCP session
// through the relay with the token added on the way — refreshed when the
// server revokes it, and given up on, with a reason, when it can't be.
func TestSignInAndRelayToANotionShapedServer(t *testing.T) {
	t.Parallel()
	fake := connectorstest.New()
	defer fake.Close()
	s := newService(t)
	c := connected(t, s, fake)
	if c.ClientID == "" || c.TokenAuth != "none" || c.Resource != fake.MCP() || c.Issuer != fake.URL || c.Granted != "default" {
		t.Errorf("the registration kept = %+v", c)
	}
	if strings.Contains(string(c.AccessToken), "at-") || strings.Contains(string(c.RefreshToken), "rt-") {
		t.Error("state.db holds a token in the clear")
	}

	sess := relayTo(t, s, c)
	sess.send(t, initializeMsg)
	if init := sess.read(t); init["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize answered %v", init)
	}
	sess.send(t, initializedMsg)
	sess.send(t, listMsg) // answered as an event stream
	tools := sess.read(t)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "notion-search" {
		t.Errorf("tools/list = %v", tools)
	}
	sess.send(t, callMsg(3, "roadmap"))
	if got := text(t, sess.read(t)); got != "found: roadmap" {
		t.Errorf("tools/call = %q", got)
	}

	// Revoked early: the relay's request is refused, refreshed and retried,
	// and the tool never knows.
	fake.Revoke()
	sess.send(t, callMsg(4, "after revoke"))
	if got := text(t, sess.read(t)); got != "found: after revoke" {
		t.Errorf("tools/call after a revoked token = %q", got)
	}
	fake.Lock()
	refreshes := fake.Refreshes
	for _, auth := range fake.Seen {
		if !strings.HasPrefix(auth, "Bearer at-") {
			t.Errorf("the server saw Authorization %q", auth)
		}
	}
	fake.Unlock()
	if refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes)
	}

	// Signed out for good: the tool gets a JSON-RPC error that says what to
	// do, and the connector says it needs connecting again.
	fake.RevokeAll()
	sess.send(t, callMsg(5, "gone"))
	failed := sess.read(t)
	msg, _ := failed["error"].(map[string]any)["message"].(string)
	if failed["id"].(float64) != 5 || !strings.Contains(msg, "connect it again") {
		t.Errorf("a call after the sign-in was revoked answered %v", failed)
	}
	c, _ = s.State.Connector(context.Background(), "pawly", "", "notion")
	if status, why := s.Status(context.Background(), c, ""); status != api.ConnectorError || !strings.Contains(why, "connect it again") {
		t.Errorf("status after revocation = %s (%s)", status, why)
	}

	// Connecting again reuses the registration when its port is free, and
	// clears the error.
	res, _, err := s.Connect(context.Background(), "pawly", "", "notion")
	if err != nil {
		t.Fatal(err)
	}
	if res.RedirectURI != c.RedirectURI {
		t.Errorf("connecting again registered a new redirect %s, had %s", res.RedirectURI, c.RedirectURI)
	}
	if _, _, err := connectorstest.SignIn(res.AuthorizationURL); err != nil {
		t.Fatal(err)
	}
	c2, _ := s.State.Connector(context.Background(), "pawly", "", "notion")
	if c2.ClientID != c.ClientID || c2.Error != "" || len(c2.AccessToken) == 0 {
		t.Errorf("after connecting again: client %s (was %s), error %q", c2.ClientID, c.ClientID, c2.Error)
	}

	// Stdin closing ends the relay and the session.
	_ = sess.in.Close()
	if err := <-sess.err; err != nil {
		t.Errorf("Serve() = %v", err)
	}
	fake.Lock()
	deleted := fake.Deleted
	fake.Unlock()
	if deleted != 1 {
		t.Errorf("sessions ended with DELETE = %d, want 1", deleted)
	}
}

// A token about to expire is refreshed before a request uses it, and the
// sweep refreshes one that will expire soon without any request at all.
func TestRefreshesBeforeExpiry(t *testing.T) {
	t.Parallel()
	fake := connectorstest.New()
	defer fake.Close()
	fake.TokenTTL = 5 * time.Minute // inside sweepAhead, outside refreshAhead
	s := newService(t)
	c := connected(t, s, fake)
	ctx := context.Background()

	if _, _, err := s.Header(ctx, c, "", false); err != nil {
		t.Fatal(err)
	}
	fake.Lock()
	if fake.Refreshes != 0 {
		t.Errorf("a token good for five minutes was refreshed before use")
	}
	fake.Unlock()

	if err := s.RefreshDue(ctx); err != nil {
		t.Fatal(err)
	}
	fake.Lock()
	if fake.Refreshes != 1 {
		t.Errorf("the sweep refreshed %d times, want 1", fake.Refreshes)
	}
	fake.Unlock()

	// One a minute from expiring is refreshed by the request itself, once,
	// however many requests find it so at the same time.
	c, _ = s.State.Connector(ctx, "pawly", "", "notion")
	c.ExpiresAt = time.Now().Add(time.Minute)
	if err := s.State.SetConnector(ctx, c); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, _, err := s.Header(ctx, c, "", false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	fake.Lock()
	if fake.Refreshes != 2 {
		t.Errorf("five requests at once refreshed %d times in all, want 2", fake.Refreshes)
	}
	fake.Unlock()
}

// Figma's registration endpoint refuses every client it hasn't approved:
// connecting says so, and says what to do instead.
func TestRefusedRegistrationPointsToTheSecretFallback(t *testing.T) {
	t.Parallel()
	fake := connectorstest.New()
	defer fake.Close()
	fake.RefuseRegistration = true
	s := newService(t)
	ctx := context.Background()
	if _, err := s.Set(ctx, "pawly", "", "figma", api.SetConnectorRequest{URL: fake.MCP()}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Connect(ctx, "pawly", "", "figma")
	if !errors.Is(err, ErrRegistration) || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "--secret") {
		t.Errorf("Connect() = %v", err)
	}
}

// A secret connector sends the secret's value, the agent's own before its
// project's, and says so when there is none.
func TestSecretConnectorSendsTheSecret(t *testing.T) {
	t.Parallel()
	s := newService(t)
	ctx := context.Background()
	c, err := s.Set(ctx, "pawly", "", "figma", api.SetConnectorRequest{URL: "https://mcp.figma.com/mcp", Secret: "FIGMA_TOKEN", Header: "X-Figma-Token"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth != api.ConnectorSecret || c.Scheme != "" {
		t.Errorf("a secret sent as X-Figma-Token = %+v", c)
	}
	if status, why := s.Status(ctx, c, ""); status != api.ConnectorError || !strings.Contains(why, "FIGMA_TOKEN") {
		t.Errorf("status with no secret = %s (%s)", status, why)
	}
	if _, _, err := s.Header(ctx, c, "agent-01", false); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Header() with no secret = %v", err)
	}
	if _, err := s.Secrets.Set(ctx, "pawly", "", "FIGMA_TOKEN", "figd_project"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Secrets.Set(ctx, "pawly", "agent-02", "FIGMA_TOKEN", "figd_agent"); err != nil {
		t.Fatal(err)
	}
	for agent, want := range map[string]string{"agent-01": "figd_project", "agent-02": "figd_agent"} {
		name, value, err := s.Header(ctx, c, agent, false)
		if err != nil || name != "X-Figma-Token" || value != want {
			t.Errorf("Header() for %s = %s: %s, %v; want %s", agent, name, value, err, want)
		}
	}

	bearer, err := s.Set(ctx, "pawly", "", "notion", api.SetConnectorRequest{URL: "https://mcp.notion.com/mcp", Secret: "FIGMA_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if name, value, _ := s.Header(ctx, bearer, "agent-01", false); name != "Authorization" || value != "Bearer figd_project" {
		t.Errorf("a secret sent by default = %s: %s", name, value)
	}
}

func TestSetRefusesWhatCantWork(t *testing.T) {
	t.Parallel()
	s := newService(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		req  api.SetConnectorRequest
		want string
	}{
		{"memory", api.SetConnectorRequest{URL: "https://x.test/mcp"}, "AgentBox gives every agent"},
		{"Notion", api.SetConnectorRequest{URL: "https://x.test/mcp"}, "invalid connector name"},
		{"notion", api.SetConnectorRequest{URL: "http://mcp.notion.com/mcp"}, "isn't https"},
		{"notion", api.SetConnectorRequest{URL: "https://user:pw@mcp.notion.com/mcp"}, "has a user"},
		{"notion", api.SetConnectorRequest{URL: "https://x.test/mcp", Auth: "basic"}, "unknown auth"},
		{"notion", api.SetConnectorRequest{URL: "https://x.test/mcp", Auth: "secret"}, "needs the secret's name"},
		{"notion", api.SetConnectorRequest{URL: "https://x.test/mcp", Secret: "TOKEN", Header: "Mcp-Session-Id"}, "isn't a header name"},
		{"notion", api.SetConnectorRequest{URL: "https://x.test/mcp", Auth: "oauth", Secret: "TOKEN"}, "are for a connector that sends a secret"},
	} {
		if _, err := s.Set(ctx, "pawly", "", tc.name, tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Set(%s, %+v) = %v, want %q", tc.name, tc.req, err, tc.want)
		}
	}
}

// Changing where a connector points forgets its sign-in, which was for the
// other server.
func TestChangingTheURLForgetsTheSignIn(t *testing.T) {
	t.Parallel()
	fake := connectorstest.New()
	defer fake.Close()
	s := newService(t)
	connected(t, s, fake)
	ctx := context.Background()
	off := false
	c, err := s.Set(ctx, "pawly", "", "notion", api.SetConnectorRequest{URL: fake.MCP(), Enabled: &off})
	if err != nil || len(c.AccessToken) == 0 || c.Enabled {
		t.Errorf("turning it off: %v, token kept %v, enabled %v", err, len(c.AccessToken) > 0, c.Enabled)
	}
	c, err = s.Set(ctx, "pawly", "", "notion", api.SetConnectorRequest{URL: fake.URL + "/other"})
	if err != nil || len(c.AccessToken) > 0 || c.ClientID != "" || c.Enabled {
		t.Errorf("after a new URL: %v, %+v", err, c)
	}
}

func TestDiscoveryChecksTheMetadataIsForTheServer(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+srv.URL+`/meta"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/meta", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"resource":"https://elsewhere.test/mcp","authorization_servers":["`+srv.URL+`"]}`)
	})
	_, err := OAuth{}.Discover(context.Background(), srv.URL+"/mcp")
	if err == nil || !strings.Contains(err.Error(), "is for https://elsewhere.test/mcp") {
		t.Errorf("Discover() = %v", err)
	}
}

// A server with no metadata at all is its own authorization server, with the
// 2025-03-26 spec's default endpoints; one that answers without a sign-in
// needs none.
func TestDiscoveryFallbacks(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	mux.HandleFunc("/open", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	d, err := OAuth{}.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if d.Server.AuthorizationEndpoint != srv.URL+"/authorize" || d.Server.RegistrationEndpoint != srv.URL+"/register" || d.Resource != srv.URL+"/mcp" {
		t.Errorf("legacy discovery = %+v", d)
	}
	if _, err := (OAuth{}).Discover(context.Background(), srv.URL+"/open"); !errors.Is(err, ErrNoAuth) {
		t.Errorf("a server that needs no sign-in: %v", err)
	}
}

func TestReadEvents(t *testing.T) {
	t.Parallel()
	stream := ": comment\n\nevent: message\ndata: {\"a\":\ndata: 1}\n\nevent: ping\ndata: skipped\n\ndata: {\"b\":2}\r\n\r\ndata: {\"c\":3}"
	var got []string
	readEvents(strings.NewReader(stream), func(d []byte) { got = append(got, string(d)) })
	want := []string{"{\"a\":\n1}", `{"b":2}`, `{"c":3}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("readEvents() = %q, want %q", got, want)
	}
}

func TestAuthParams(t *testing.T) {
	t.Parallel()
	got := authParams([]string{`Bearer resource_metadata="https://mcp.figma.com/.well-known/oauth-protected-resource",scope="mcp:connect",authorization_uri="https://api.figma.com/x"`})
	if got["resource_metadata"] != "https://mcp.figma.com/.well-known/oauth-protected-resource" || got["scope"] != "mcp:connect" {
		t.Errorf("authParams() = %v", got)
	}
}

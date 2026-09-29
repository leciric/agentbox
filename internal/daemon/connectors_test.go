package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/connectors"
	"agentbox/internal/connectors/connectorstest"
	"agentbox/internal/state"
)

// connectorsIncus is oneAgentIncus that also keeps the AI tools'
// configuration files the daemon writes into the agent, in $CAPTURE, so a test
// can read what the agent was given.
const connectorsIncus = `case "$1" in
  list) echo '[{"name": "ab-hello-stack-agent-01", "status": "Running", "state": {"network": {"eth0": {"addresses": [{"family": "inet", "address": "10.1.2.3"}]}}}}]' ;;
  query) echo '[]' ;;
  exec)
    for arg in "$@"; do
      case "$arg" in
        */.claude.json|*/config.toml|*/opencode.json) cat > "$CAPTURE/$(basename "$arg").tmp" && mv "$CAPTURE/$(basename "$arg").tmp" "$CAPTURE/$(basename "$arg")"; exit 0 ;;
      esac
    done ;;
esac`

// The whole of it, through the daemon: a connector added to a project over
// the API, signed in to in the browser against a server shaped like
// Notion's, given to the project's agent in all three AI tools' configuration
// without its token, and used from inside the agent through the relay on the
// agent's own socket — then disconnected and removed.
func TestConnectorEndToEnd(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	capture := filepath.Join(root, "capture")
	if err := os.MkdirAll(capture, 0o755); err != nil {
		t.Fatal(err)
	}
	d := startTestDaemon(t, root, connectorsIncus, testConfig{env: map[string]string{"CAPTURE": capture}})
	ctx := context.Background()
	a := addTestAgent(t, d)
	if err := d.srv.serveAgentAPI(a.Instance); err != nil {
		t.Fatal(err)
	}
	fake := connectorstest.New()
	defer fake.Close()

	events := make(chan api.Connector, 64)
	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go func() {
		_ = d.client.Events(eventsCtx, func(ev api.Event) error {
			var c api.Connector
			if ev.Type == api.EventConnector && json.Unmarshal(ev.Data, &c) == nil {
				events <- c
			}
			return nil
		})
	}()
	waitFor(t, "an event subscriber", func() bool { return d.srv.events.subscribers() > 0 })

	added, err := d.client.SetConnector(ctx, "hello-stack", "notion", api.SetConnectorRequest{URL: fake.MCP()})
	if err != nil {
		t.Fatal(err)
	}
	if added.Status != api.ConnectorDisconnected || added.Auth != api.ConnectorOAuth || !added.Enabled ||
		strings.Join(added.Agents, ",") != "hello-stack/agent-01" {
		t.Errorf("added = %+v", added)
	}

	// Given to the agent in every AI tool's configuration, as the relay.
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(capture, name))
		return string(b)
	}
	waitFor(t, "the agent's MCP configuration", func() bool {
		return strings.Contains(read(".claude.json"), "notion") && strings.Contains(read("config.toml"), "notion") && strings.Contains(read("opencode.json"), "notion")
	})
	var claude struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(".claude.json")), &claude); err != nil {
		t.Fatal(err)
	}
	if s := claude.MCPServers["notion"]; strings.Join(s.Args, " ") != "connector mcp notion" || claude.MCPServers["memory"].Command == "" {
		t.Errorf("Claude Code's mcpServers = %+v", claude.MCPServers)
	}
	if !strings.Contains(read("config.toml"), "[mcp_servers.notion]") {
		t.Errorf("Codex's config.toml:\n%s", read("config.toml"))
	}

	res, err := d.client.ConnectConnector(ctx, "hello-stack", "notion")
	if err != nil {
		t.Fatal(err)
	}
	if res.Connector.Status != api.ConnectorConnecting || !strings.HasPrefix(res.RedirectURI, "http://127.0.0.1:") ||
		!strings.HasPrefix(res.AuthorizationURL, fake.URL+"/authorize?") {
		t.Errorf("connect = %+v", res)
	}
	if code, page, err := connectorstest.SignIn(res.AuthorizationURL); err != nil || code != http.StatusOK || !strings.Contains(page, "signed in to notion") {
		t.Fatalf("the browser: %d %v\n%s", code, err, page)
	}
	for timeout := time.After(10 * time.Second); ; {
		select {
		case c := <-events:
			if c.Name != "notion" || c.Status != api.ConnectorConnected {
				continue
			}
			if c.Issuer != fake.URL || c.Scopes != "default" || c.ConnectedAt == nil {
				t.Errorf("connected = %+v", c)
			}
		case <-timeout:
			t.Fatal("no event said notion was connected")
		}
		break
	}

	// No token went into the agent: not in its configuration, nor anywhere
	// else the daemon wrote.
	fake.Lock()
	fake.Seen = nil
	fake.Unlock()
	for _, f := range []string{".claude.json", "config.toml", "opencode.json"} {
		if strings.Contains(read(f), "at-") || strings.Contains(read(f), "rt-") {
			t.Errorf("%s holds a token:\n%s", f, read(f))
		}
	}

	// Inside the agent: what it has, and an MCP session through the relay.
	inAgent := api.NewClient(d.srv.agentSocketPath(a.Instance))
	mine, err := inAgent.SelfConnectors(ctx)
	if err != nil || len(mine) != 1 || mine[0].Name != "notion" || mine[0].Status != api.ConnectorConnected {
		t.Errorf("SelfConnectors() = %+v, %v", mine, err)
	}
	sess := startRelay(t, inAgent, "notion")
	sess.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"codex","version":"1"}}}`)
	if m := sess.read(t); m["result"] == nil {
		t.Fatalf("initialize = %v", m)
	}
	sess.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	sess.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if m := sess.read(t); !strings.Contains(mustJSON(m), "notion-search") {
		t.Errorf("tools/list = %v", m)
	}
	sess.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"notion-search","arguments":{"query":"Q3 roadmap"}}}`)
	if m := sess.read(t); !strings.Contains(mustJSON(m), "found: Q3 roadmap") {
		t.Errorf("tools/call = %v", m)
	}
	fake.Lock()
	for _, auth := range fake.Seen {
		if !strings.HasPrefix(auth, "Bearer at-") {
			t.Errorf("the server saw Authorization %q", auth)
		}
	}
	fake.Unlock()

	// Another agent's own connector isn't this one's to reach.
	other := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", AI: "none",
		Branch: "agentbox/agent-02", Status: state.AgentReady, CreatedAt: time.Now()}
	if err := d.srv.store.AddAgent(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.SetConnector(ctx, "hello-stack/agent-02", "linear", api.SetConnectorRequest{URL: fake.MCP()}); err != nil {
		t.Fatal(err)
	}
	stranger := startRelay(t, inAgent, "linear")
	stranger.send(`{"jsonrpc":"2.0","id":9,"method":"initialize","params":{}}`)
	if m := stranger.read(t); !strings.Contains(mustJSON(m), `has no connector \"linear\"`) {
		t.Errorf("reaching another agent's connector = %v", m)
	}
	list, err := d.client.Connectors(ctx, "hello-stack/agent-02")
	if err != nil || len(list) != 2 || list[0].Name != "linear" || list[0].Scope != "agent" || list[1].Name != "notion" {
		t.Errorf("agent-02's connectors = %+v, %v", list, err)
	}

	// Disconnected: the next call says how to connect again.
	if c, err := d.client.DisconnectConnector(ctx, "hello-stack", "notion"); err != nil || c.Status != api.ConnectorDisconnected {
		t.Errorf("disconnect = %+v, %v", c, err)
	}
	sess.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"notion-search","arguments":{"query":"x"}}}`)
	if m := sess.read(t); !strings.Contains(mustJSON(m), "agentbox connector connect hello-stack notion") {
		t.Errorf("a call after disconnecting = %v", m)
	}

	// Removed: gone from the agent's configuration too.
	if err := d.client.RemoveConnector(ctx, "hello-stack", "notion"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.Connector(ctx, "hello-stack", "notion"); !api.IsNotFound(err) {
		t.Errorf("a removed connector: %v", err)
	}
	waitFor(t, "notion to leave the agent's configuration", func() bool {
		return !strings.Contains(read(".claude.json"), "notion") && !strings.Contains(read("config.toml"), "notion")
	})
}

// A server that won't register AgentBox — Figma's — makes connect fail with
// the way out; the lead has no connectors of its own.
func TestConnectorRefusals(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	addTestAgent(t, d)
	fake := connectorstest.New()
	defer fake.Close()
	fake.RefuseRegistration = true
	if _, err := d.client.SetConnector(ctx, "hello-stack", "figma", api.SetConnectorRequest{URL: fake.MCP()}); err != nil {
		t.Fatal(err)
	}
	_, err := d.client.ConnectConnector(ctx, "hello-stack", "figma")
	if err == nil || !strings.Contains(err.Error(), "won't register AgentBox") || !strings.Contains(err.Error(), "--secret") {
		t.Errorf("connecting to a server that refuses registration: %v", err)
	}
	if _, err := d.client.SetConnector(ctx, "hello-stack", "figma", api.SetConnectorRequest{URL: fake.MCP(), Secret: "FIGMA_TOKEN", Header: "X-Figma-Token"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.ConnectConnector(ctx, "hello-stack", "figma"); err == nil || !strings.Contains(err.Error(), "nothing to sign in to") {
		t.Errorf("connecting a secret connector: %v", err)
	}
	if _, err := d.client.Connectors(ctx, "hello-stack/"+api.LeadName); err == nil {
		t.Error("the lead has connectors of its own")
	}
	if _, err := d.client.Connectors(ctx, "nope"); !api.IsNotFound(err) {
		t.Errorf("an unknown project's connectors: %v", err)
	}
}

type relaySession struct {
	in  *io.PipeWriter
	out *bufio.Scanner
}

// startRelay is `agentbox connector mcp <name>` inside the agent.
func startRelay(t *testing.T, inAgent *api.Client, name string) *relaySession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	r := &connectors.Relay{HTTP: inAgent.HTTPClient(), URL: inAgent.SelfConnectorURL(name)}
	done := make(chan struct{})
	go func() {
		_ = r.Serve(context.Background(), inR, outW)
		_ = outW.Close()
		close(done)
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		<-done
	})
	return &relaySession{in: inW, out: bufio.NewScanner(outR)}
}

func (s *relaySession) send(line string) { _, _ = io.WriteString(s.in, line+"\n") }

func (s *relaySession) read(t *testing.T) map[string]any {
	t.Helper()
	got := make(chan map[string]any, 1)
	go func() {
		var m map[string]any
		if s.out.Scan() {
			_ = json.Unmarshal(s.out.Bytes(), &m)
		}
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

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

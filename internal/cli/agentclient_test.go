package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
)

// podMaster is the master as a Kubernetes pod reaches it: an HTTP server that
// wants the pod's token as a bearer token on every request, the in-agent
// commands' relays included, so a request that skips it is caught here.
type podMaster struct {
	srv *httptest.Server
	mux *http.ServeMux

	mu   sync.Mutex
	seen []string
	bad  []string
}

func newPodMaster(t *testing.T, token string) *podMaster {
	t.Helper()
	m := &podMaster{mux: http.NewServeMux()}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		m.mu.Lock()
		m.seen = append(m.seen, r.Method+" "+r.URL.Path)
		if got == "Bearer "+token {
			m.mu.Unlock()
			m.mux.ServeHTTP(w, r)
			return
		}
		m.bad = append(m.bad, r.Method+" "+r.URL.Path+": Authorization "+got)
		m.mu.Unlock()
		http.Error(w, "the pod's token, as a bearer token, is what this wants", http.StatusUnauthorized)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// use is the environment a pod has: where the master is and where its token
// is, and no in-agent socket at all.
func (m *podMaster) use(t *testing.T, token string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", "")
	t.Setenv("AGENTBOX_MASTER_URL", m.srv.URL)
	t.Setenv("AGENTBOX_TOKEN_FILE", path)
}

// check says every request the master saw carried the pod's token.
func (m *podMaster) check(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bad) > 0 {
		t.Errorf("requests without the pod's token: %v", m.bad)
	}
}

func (m *podMaster) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.seen...)
}

// A connector's MCP relay inside a pod: reached over the master with the pod's
// token, answering initialize and tools/list as it does over the socket.
func TestConnectorMCPOverTheMaster(t *testing.T) {
	isolate(t)
	const token = "pod-token-0123"
	master := newPodMaster(t, token)
	master.mux.HandleFunc("POST /v1/self/connectors/notion/mcp", connectorMCPHandler)
	master.use(t, token)

	out, err := run(t, strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n")+"\n", "connector", "mcp", "notion")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, `"protocolVersion":"2025-06-18"`, "notion-search", "Search the workspace")
	master.check(t)
	relayed := 0
	for _, r := range master.requests() {
		if r == "POST /v1/self/connectors/notion/mcp" {
			relayed++
		}
	}
	if relayed < 2 {
		t.Errorf("the relay sent %d MCP messages to the master, want at least initialize and tools/list (%v)", relayed, master.requests())
	}

	// connector tools and connector call speak to the same relay.
	if out, err := run(t, "", "connector", "tools", "notion"); err != nil {
		t.Errorf("connector tools over the master: %v\n%s", err, out)
	} else {
		mustContain(t, out, "notion-search", "Search the workspace")
	}
	if out, err := run(t, "", "connector", "call", "notion", "notion-search", `{"query":"from a pod"}`); err != nil || strings.TrimSpace(out) != "found: from a pod" {
		t.Errorf("connector call over the master = %q, %v", out, err)
	}
	master.check(t)
}

// whoami in a pod asks the master which agent it is.
func TestWhoamiOverTheMaster(t *testing.T) {
	isolate(t)
	const token = "pod-token-0123"
	master := newPodMaster(t, token)
	master.mux.HandleFunc("GET /v1/self", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.Self{
			Ref:      "hello-stack/agent-01",
			Project:  "hello-stack",
			Agent:    "agent-01",
			Branch:   "agentbox/agent-01",
			Worktree: "/workspace",
			IP:       "10.244.0.7",
		})
	})
	master.use(t, token)

	out, err := run(t, "", "whoami")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "hello-stack/agent-01", "agentbox/agent-01", "/workspace", "10.244.0.7")
	master.check(t)
}

// connector list with no argument in a pod lists what the agent is given.
func TestConnectorListOverTheMaster(t *testing.T) {
	isolate(t)
	const token = "pod-token-0123"
	master := newPodMaster(t, token)
	master.mux.HandleFunc("GET /v1/self/connectors", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]api.SelfConnector{
			{Name: "notion", URL: "https://mcp.notion.com/mcp", Status: api.ConnectorConnected},
			{Name: "figma", URL: "https://mcp.figma.com/mcp", Status: api.ConnectorDisconnected},
		})
	})
	master.use(t, token)

	out, err := run(t, "", "connector", "list")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "NAME", "notion", "https://mcp.notion.com/mcp", "connected")
	master.check(t)
}

// The socket seam wins over the pod variables: a test that says "not an agent"
// is believed even where the machine really is one.
func TestTheAgentSocketSeamWinsOverTheMaster(t *testing.T) {
	isolate(t)
	const token = "pod-token-0123"
	master := newPodMaster(t, token)
	master.mux.HandleFunc("GET /v1/self", func(w http.ResponseWriter, r *http.Request) {
		t.Error("whoami asked the master, and the socket seam said not an agent")
	})
	master.use(t, token)
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))

	if _, err := run(t, "", "whoami"); err == nil || !strings.Contains(err.Error(), "not inside an AgentBox agent") {
		t.Errorf("whoami with the seam pointing nowhere: got %v, want 'not inside an AgentBox agent'", err)
	}
	if seen := master.requests(); len(seen) > 0 {
		t.Errorf("the master was asked anyway: %v", seen)
	}
}

// With no pod environment and no socket, whoami says what it always said: this
// isn't an agent.
func TestWhoamiWithoutAPodOrAnAgent(t *testing.T) {
	isolate(t)
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	t.Setenv("AGENTBOX_MASTER_URL", "")
	t.Setenv("AGENTBOX_TOKEN_FILE", "")
	if _, err := run(t, "", "whoami"); err == nil || !strings.Contains(err.Error(), "not inside an AgentBox agent") {
		t.Errorf("whoami with neither an agent nor a pod: got %v", err)
	}
}

// A pod whose token file isn't there says so, naming the file and agentd: it is
// not the same as this not being an agent, and must not be read as one.
func TestAPodWithoutItsTokenFile(t *testing.T) {
	isolate(t)
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", "")
	t.Setenv("AGENTBOX_MASTER_URL", "http://127.0.0.1:1")
	path := filepath.Join(t.TempDir(), "token")
	t.Setenv("AGENTBOX_TOKEN_FILE", path)

	if _, err := run(t, "", "whoami"); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "agentd") {
		t.Errorf("whoami with no token file: got %v, want the path and agentd named", err)
	}
	if _, err := run(t, "", "connector", "mcp", "notion"); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("the relay with no token file: got %v, want the path named", err)
	}
}

// An empty token is nothing to send, and says so rather than asking the master
// with no credentials.
func TestAPodWithAnEmptyToken(t *testing.T) {
	isolate(t)
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", "")
	t.Setenv("AGENTBOX_MASTER_URL", "http://127.0.0.1:1")
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("\n \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTBOX_TOKEN_FILE", path)

	if _, err := run(t, "", "whoami"); err == nil || !strings.Contains(err.Error(), "is empty") || !strings.Contains(err.Error(), path) {
		t.Errorf("whoami with an empty token file: got %v, want an empty-token error naming the file", err)
	}
}

// The hooks never fail an agent's turn: one outside an agent says nothing at
// all, and one whose pod environment is broken says one line and succeeds.
func TestHeavyHooksNeverFailATurn(t *testing.T) {
	isolate(t)
	call := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"go test ./..."}}`

	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	t.Setenv("AGENTBOX_MASTER_URL", "")
	if out, err := run(t, call, "heavy-hook"); err != nil || out != "" {
		t.Errorf("heavy-hook outside an agent: %q, %v; want silence and success", out, err)
	}

	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", "")
	t.Setenv("AGENTBOX_MASTER_URL", "http://127.0.0.1:1")
	path := filepath.Join(t.TempDir(), "token")
	t.Setenv("AGENTBOX_TOKEN_FILE", path)
	out, err := run(t, call, "heavy-hook")
	if err != nil {
		t.Errorf("heavy-hook with no token file failed the turn: %v", err)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n") + 1; out == "" || n != 1 || !strings.Contains(out, path) {
		t.Errorf("heavy-hook with no token file said %q, want one line naming %s", out, path)
	}

	if out, err := run(t, "", "heavy-join", "key", "1234"); err != nil || out == "" || !strings.Contains(out, path) {
		t.Errorf("heavy-join with no token file: %q, %v; want one line and success", out, err)
	}
}

package cli_test

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/connectors/connectorstest"
	"agentbox/internal/testutil"
)

// TestConnectorCommands walks the command line against a server shaped like
// Notion's: add, connect — with a browser that signs in at once — list,
// disconnect and remove, and a secret connector beside it.
func TestConnectorCommands(t *testing.T) {
	isolate(t)
	// The browser connect opens: it follows the sign-in to the end, as a
	// user who is already signed in and allows it would.
	bin := t.TempDir()
	browser := "#!/bin/sh\nexec curl -sfL -o /dev/null \"$1\"\n"
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte(browser), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	startDaemon(t)
	fake := connectorstest.New()
	defer fake.Close()
	repo := testutil.FixtureRepo(t, "hello-stack")
	if _, err := run(t, "", "add", repo); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "connector", "add", "hello-stack", "notion", "--url", fake.MCP())
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "notion is oauth for hello-stack", "agentbox connector connect hello-stack notion")

	out, err = run(t, "", "connector", "connect", "hello-stack", "notion")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Sign in to notion at:", fake.URL+"/authorize?", "Connected notion")

	out, err = run(t, "", "connector", "add", "hello-stack", "figma", "--url", "https://mcp.figma.com/mcp", "--secret", "FIGMA_TOKEN", "--header", "X-Figma-Token", "--disabled")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "figma is sending $FIGMA_TOKEN as X-Figma-Token")

	out, err = run(t, "", "connector", "list", "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "notion", "connected", "figma", "FIGMA_TOKEN, which isn't set", "(off)")
	if strings.Contains(out, "at-") {
		t.Errorf("the list shows a token:\n%s", out)
	}

	if out, err = run(t, "", "connector", "disconnect", "hello-stack", "notion"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Disconnected notion of hello-stack")
	if out, _ = run(t, "", "connector", "list", "hello-stack"); !strings.Contains(out, "disconnected") {
		t.Errorf("after disconnecting:\n%s", out)
	}
	if _, err := run(t, "", "connector", "connect", "hello-stack", "figma"); err == nil || !strings.Contains(err.Error(), "nothing to sign in to") {
		t.Errorf("connecting a secret connector: %v", err)
	}
	if out, err = run(t, "", "connector", "rm", "hello-stack", "notion"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Removed notion from hello-stack")
	// Not in an agent — even when the tests run inside one.
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	if _, err := run(t, "", "connector", "mcp", "figma"); err == nil || !strings.Contains(err.Error(), "runs inside an agent") {
		t.Errorf("the relay outside an agent: %v", err)
	}
}

// fakeConnectorSocket is an agent's socket with one connector, notion, whose
// relay answers like a small MCP server: one tool, which fails on "boom".
func fakeConnectorSocket(t *testing.T) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/self/connectors/notion/mcp", func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		var result any
		switch m.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "notion-search", "description": "Search the workspace",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}}}
		case "tools/call":
			q := m.Params.Arguments["query"]
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "found: " + q}}, "isError": q == "boom"}
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket
}

// Inside an agent, a connector connected while it worked is reached from the
// shell: its tools, and a call to one.
func TestConnectorToolsAndCall(t *testing.T) {
	isolate(t)
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", fakeConnectorSocket(t))
	out, err := run(t, "", "connector", "tools", "notion")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "notion-search", "Search the workspace", `arguments: {"properties":{"query"`)
	if out, err = run(t, "", "connector", "tools", "notion", "--json"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `"name": "notion-search"`, `"inputSchema"`)

	if out, err = run(t, "", "connector", "call", "notion", "notion-search", `{"query":"onboarding spec"}`); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "found: onboarding spec" {
		t.Errorf("call = %q", out)
	}
	if out, err = run(t, `{"query":"from stdin"}`, "connector", "call", "notion", "notion-search", "-"); err != nil || !strings.Contains(out, "found: from stdin") {
		t.Errorf("call with arguments from stdin = %q, %v", out, err)
	}
	if out, err = run(t, "", "connector", "call", "notion", "notion-search", `{"query":"boom"}`); err == nil || !strings.Contains(out, "found: boom") {
		t.Errorf("a tool answering with an error = %q, %v: want its text and a failure", out, err)
	}
	if _, err = run(t, "", "connector", "call", "notion", "notion-search", `["query"]`); err == nil || !strings.Contains(err.Error(), "aren't a JSON object") {
		t.Errorf("arguments that aren't an object: %v", err)
	}

	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	if _, err := run(t, "", "connector", "call", "notion", "notion-search"); err == nil || !strings.Contains(err.Error(), "runs inside an agent") {
		t.Errorf("call outside an agent: %v", err)
	}
}

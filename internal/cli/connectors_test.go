package cli_test

import (
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

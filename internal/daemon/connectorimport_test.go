package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/connectors/connectorstest"
)

// Importing the user's AI tools' MCP servers, through the daemon: a scan
// shows what can be imported without a header's value, and an import adds
// the picked ones with the auth each needs — OAuth for a server that asks,
// the configuration's header as a secret, or one entered by the user.
func TestImportConnectors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	capture := filepath.Join(root, "capture")
	if err := os.MkdirAll(capture, 0o755); err != nil {
		t.Fatal(err)
	}
	d := startTestDaemon(t, root, connectorsIncus, testConfig{env: map[string]string{"CAPTURE": capture}})
	ctx := context.Background()
	addTestAgent(t, d)
	fake := connectorstest.New()
	defer fake.Close()

	home := filepath.Join(root, "home")
	write := func(rel, content string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude.json", mustJSON(map[string]any{"mcpServers": map[string]any{
		"notion": map[string]any{"type": "http", "url": fake.MCP()},
		"keyed":  map[string]any{"type": "http", "url": fake.MCP() + "?keyed", "headers": map[string]string{"X-Api-Key": "lit-value"}},
	}}))
	write(".cursor/mcp.json", mustJSON(map[string]any{"mcpServers": map[string]any{
		"Notion":  map[string]any{"url": fake.MCP() + "/"},
		"withvar": map[string]any{"url": fake.MCP() + "?var", "headers": map[string]string{"Authorization": "Bearer ${TOK}"}},
		"cmd":     map[string]any{"command": "npx", "args": []string{"server", "--key", "cmd-secret"}},
	}}))
	write(".codex/config.toml", "[mcp_servers.broken\n")
	d.srv.cfg.SkillHomes = []string{home}

	scan, err := d.client.ScanConnectors(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(scan)
	for _, secret := range []string{"lit-value", "cmd-secret"} {
		if strings.Contains(raw, secret) {
			t.Errorf("the scan shows %s: %s", secret, raw)
		}
	}
	by := map[string]api.ConnectorCandidate{}
	for _, c := range scan.Candidates {
		by[c.Name] = c
	}
	if len(scan.Candidates) != 4 || len(by["notion"].Sources) != 2 || by["cmd"].Problem != api.ConnectorProblemStdio ||
		by["keyed"].Header != "X-Api-Key" || by["keyed"].Secret != "KEYED_TOKEN" || !by["withvar"].NeedsValue || by["withvar"].Secret != "TOK" {
		t.Errorf("candidates = %s", raw)
	}
	if len(scan.Unreadable) != 1 || scan.Unreadable[0].Tool != "codex" {
		t.Errorf("unreadable = %+v", scan.Unreadable)
	}

	res, err := d.client.ImportConnectors(ctx, api.ImportConnectorsRequest{Project: "hello-stack", Servers: []api.ImportConnector{
		{URL: by["notion"].URL}, {URL: by["keyed"].URL}, {URL: by["withvar"].URL}, {URL: "https://gone.example.com/mcp"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	added := map[string]api.Connector{}
	for _, c := range res.Added {
		added[c.Name] = c
	}
	if len(res.Added) != 2 || added["notion"].Auth != api.ConnectorOAuth || added["notion"].Status != api.ConnectorDisconnected ||
		added["keyed"].Auth != api.ConnectorSecret || added["keyed"].Header != "X-Api-Key" || added["keyed"].Status != api.ConnectorConnected {
		t.Errorf("added = %s", mustJSON(res.Added))
	}
	if len(res.Failed) != 2 || !strings.Contains(res.Failed[0].Error, "$TOK") || !strings.Contains(res.Failed[1].Error, "scan again") {
		t.Errorf("failed = %+v", res.Failed)
	}
	if v, err := d.srv.secrets().Resolve(ctx, "hello-stack", "", "KEYED_TOKEN"); err != nil || v != "lit-value" {
		t.Errorf("KEYED_TOKEN = %q, %v", v, err)
	}

	// Entered by the user, the variable's value is the project's secret.
	res, err = d.client.ImportConnectors(ctx, api.ImportConnectorsRequest{Project: "hello-stack", Servers: []api.ImportConnector{{URL: by["withvar"].URL, Value: "tok-1"}}})
	if err != nil || len(res.Added) != 1 || res.Added[0].Secret != "TOK" || res.Added[0].Scheme != "Bearer" {
		t.Fatalf("withvar = %+v, %v", res, err)
	}
	if v, _ := d.srv.secrets().Resolve(ctx, "hello-stack", "", "TOK"); v != "tok-1" {
		t.Errorf("TOK = %q", v)
	}

	// What is a connector already is marked, and not imported twice.
	scan, err = d.client.ScanConnectors(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range scan.Candidates {
		if c.Problem == "" && c.Existing != c.Name {
			t.Errorf("%s isn't marked as there: %+v", c.Name, c)
		}
	}
	res, err = d.client.ImportConnectors(ctx, api.ImportConnectorsRequest{Project: "hello-stack", Servers: []api.ImportConnector{{URL: by["notion"].URL}}})
	if err != nil || len(res.Added) != 0 || len(res.Failed) != 1 {
		t.Errorf("importing notion again = %+v, %v", res, err)
	}

	// AgentBox-wide, the header's value is kept with the connector, and no
	// project gets it as a secret.
	res, err = d.client.ImportConnectors(ctx, api.ImportConnectorsRequest{Servers: []api.ImportConnector{{URL: by["keyed"].URL, Name: "keyed-wide"}}})
	if err != nil || len(res.Added) != 1 || res.Added[0].Scope != api.ConnectorWide || res.Added[0].Status != api.ConnectorConnected {
		t.Fatalf("wide keyed = %s, %v", mustJSON(res), err)
	}
	if _, err := d.client.ImportConnectors(ctx, api.ImportConnectorsRequest{Project: "nope", Servers: []api.ImportConnector{{URL: by["keyed"].URL}}}); err == nil {
		t.Error("an import into no project worked")
	}
}

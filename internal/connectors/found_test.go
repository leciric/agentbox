package connectors

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// testdata/home has a configuration of every tool, with every kind of server
// they can have; testdata/broken has malformed ones.

func findIn(t *testing.T, homes ...string) ([]Found, []Unreadable) {
	t.Helper()
	return FindServers(homes)
}

func foundNamed(t *testing.T, found []Found, tool, name string) Found {
	t.Helper()
	for _, f := range found {
		if f.Tool == tool && f.Name == name {
			return f
		}
	}
	t.Fatalf("%s's %s wasn't found", tool, name)
	return Found{}
}

func TestFindServersReadsEveryTool(t *testing.T) {
	found, unreadable := findIn(t, "testdata/home")
	if len(unreadable) != 0 {
		t.Fatalf("unreadable: %+v", unreadable)
	}
	for _, tc := range []struct {
		tool, name string
		want       Found
	}{
		// Claude Code: user scope, and a project's.
		{ToolClaudeCode, "linear", Found{Transport: api.TransportHTTP, URL: "https://mcp.linear.app/mcp"}},
		{ToolClaudeCode, "Sentry Prod", Found{Transport: api.TransportHTTP, URL: "https://mcp.sentry.dev/mcp/", Header: "Authorization", Scheme: "Bearer", Value: "${SENTRY_TOKEN}", Variable: "SENTRY_TOKEN", Unresolved: true}},
		{ToolClaudeCode, "github", Found{Transport: api.TransportStdio, Command: "npx"}},
		{ToolClaudeCode, "old-sse", Found{Transport: api.TransportSSE, URL: "https://sse.example.com/sse"}},
		{ToolClaudeCode, "acme", Found{Project: "/home/ana/pawly", Transport: api.TransportHTTP, URL: "https://mcp.acme.io/mcp", Header: "X-Api-Key", Value: "acme-literal-key"}},
		{ToolClaudeDesktop, "filesystem", Found{Transport: api.TransportStdio, Command: "npx"}},
		// Codex: headers from the shell's variables, and a command with a key in it.
		{ToolCodex, "linear", Found{Transport: api.TransportHTTP, URL: "https://mcp.linear.app/mcp/"}},
		{ToolCodex, "figma", Found{Transport: api.TransportHTTP, URL: "https://mcp.figma.com/mcp", Header: "X-Figma-Token", Value: "${FIGMA_TOKEN}", Variable: "FIGMA_TOKEN", Unresolved: true}},
		{ToolCodex, "stripe", Found{Transport: api.TransportHTTP, URL: "https://mcp.stripe.com", Header: "Authorization", Scheme: "Bearer", Value: "${STRIPE_KEY}", Variable: "STRIPE_KEY", Unresolved: true}},
		{ToolCodex, "docs", Found{Transport: api.TransportStdio, Command: "uvx"}},
		// OpenCode: JSON with comments and trailing commas.
		{ToolOpenCode, "notion", Found{Transport: api.TransportHTTP, URL: "https://mcp.notion.com/mcp", Header: "Authorization", Scheme: "Bearer", Value: "{env:NOTION_TOKEN}", Variable: "NOTION_TOKEN", Unresolved: true}},
		{ToolOpenCode, "local-db", Found{Transport: api.TransportStdio, Command: "bun"}},
		{ToolOpenCode, "dev", Found{Transport: api.TransportHTTP, URL: "http://localhost:3000/mcp"}},
		{ToolCursor, "LINEAR", Found{Transport: api.TransportHTTP, URL: "HTTPS://MCP.LINEAR.APP/mcp"}},
		{ToolCursor, "templated", Found{Transport: api.TransportHTTP, URL: "https://${env:HOST}/mcp"}},
		// Gemini CLI: httpUrl is streamable HTTP, url SSE unless typed http.
		{ToolGemini, "context7", Found{Transport: api.TransportHTTP, URL: "https://mcp.context7.com/mcp", Header: "CONTEXT7_API_KEY", Value: "ctx7-literal"}},
		{ToolGemini, "legacy", Found{Transport: api.TransportSSE, URL: "https://legacy.example.com/sse"}},
		{ToolGemini, "typed", Found{Transport: api.TransportHTTP, URL: "https://typed.example.com/mcp"}},
	} {
		got := foundNamed(t, found, tc.tool, tc.name)
		got.Tool, got.Path, got.Name = "", "", ""
		if got != tc.want {
			t.Errorf("%s's %s:\n got %+v\nwant %+v", tc.tool, tc.name, got, tc.want)
		}
	}
	for _, f := range found {
		if f.Name == "not-an-object" {
			t.Error("a server that isn't an object was read")
		}
		if strings.Contains(f.Command, "ghp_") || strings.Contains(f.Command, "abc") {
			t.Errorf("a command's arguments were kept: %q", f.Command)
		}
	}
	if f := foundNamed(t, found, ToolClaudeDesktop, "filesystem"); !strings.Contains(f.Path, filepath.Join("Application Support", "Claude")) {
		t.Errorf("Claude Desktop's path is %s", f.Path)
	}
}

func TestFindServersReportsMalformedConfigs(t *testing.T) {
	found, unreadable := findIn(t, "testdata/broken", "testdata/home", "testdata/home", "testdata/nobody")
	tools := map[string]bool{}
	for _, u := range unreadable {
		tools[u.Tool] = true
		if u.Err == nil || !strings.HasPrefix(u.Path, "testdata/broken") {
			t.Errorf("unreadable %+v", u)
		}
	}
	for _, tool := range []string{ToolClaudeCode, ToolCodex, ToolOpenCode, ToolCursor} {
		if !tools[tool] {
			t.Errorf("%s's malformed configuration wasn't reported", tool)
		}
	}
	// The good home is still read, once, though it was given twice.
	n := 0
	for _, f := range found {
		if f.Tool == ToolGemini && f.Name == "context7" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("context7 found %d times", n)
	}
}

func TestCandidates(t *testing.T) {
	found, _ := findIn(t, "testdata/home")
	have := []api.Connector{{Name: "stripe", URL: "https://MCP.stripe.com/"}, {Name: "typed", URL: "https://elsewhere.example.com/mcp"}}
	got := Candidates(found, have, CheckURL)
	by := map[string]Candidate{}
	for _, c := range got {
		if _, dup := by[c.Name]; dup {
			t.Errorf("two candidates named %s", c.Name)
		}
		by[c.Name] = c
	}

	// The same server in three tools, written three ways, is one.
	linear := by["linear"]
	if len(linear.Sources) != 3 || linear.URL != "https://mcp.linear.app/mcp" || linear.Problem != "" {
		t.Errorf("linear: %+v", linear)
	}
	if by["stripe"].Existing != "stripe" {
		t.Errorf("stripe is a connector already: %+v", by["stripe"])
	}
	// A name a connector has gets another.
	if c := by["typed-2"]; c.URL != "https://typed.example.com/mcp" {
		t.Errorf("typed-2: %+v (all: %v)", c, names(got))
	}
	if c := by["playwright-mcp"]; c.URL == "" {
		t.Errorf("a reserved name wasn't changed: %v", names(got))
	}
	if c := by["sentry-prod"]; !c.NeedsValue || c.Secret != "SENTRY_TOKEN" || c.Header != "Authorization" || c.Scheme != "Bearer" || c.Value != "" {
		t.Errorf("sentry-prod: %+v", c)
	}
	if c := by["acme"]; c.NeedsValue || c.Secret != "ACME_TOKEN" || c.Value != "acme-literal-key" {
		t.Errorf("acme: %+v", c)
	}
	for name, problem := range map[string]string{
		"github": api.ConnectorProblemStdio, "docs": api.ConnectorProblemStdio, "filesystem": api.ConnectorProblemStdio,
		"old-sse": api.ConnectorProblemSSE, "legacy": api.ConnectorProblemSSE,
		"dev": api.ConnectorProblemURL, "templated": api.ConnectorProblemVariable,
	} {
		if by[name].Problem != problem {
			t.Errorf("%s: problem %q, want %q", name, by[name].Problem, problem)
		}
	}
	// What can be imported comes first, then what is there already, then
	// what can't be.
	rank := func(c Candidate) int {
		switch {
		case c.Problem != "":
			return 2
		case c.Existing != "":
			return 1
		}
		return 0
	}
	for i := 1; i < len(got); i++ {
		if rank(got[i-1]) > rank(got[i]) {
			t.Errorf("out of order: %v", names(got))
		}
	}
	// A header's value never reaches the API.
	data, err := json.Marshal(got[0].ConnectorCandidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		b, _ := json.Marshal(c.ConnectorCandidate)
		data = append(data, b...)
	}
	for _, secret := range []string{"acme-literal-key", "ctx7-literal", "ghp_secret"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("%s is in the API's candidates", secret)
		}
	}
}

func names(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestPickHeader(t *testing.T) {
	for _, tc := range []struct {
		headers                         map[string]string
		header, scheme, value, variable string
		unresolved                      bool
	}{
		{nil, "", "", "", "", false},
		{map[string]string{"authorization": "Token abc", "X-Api-Key": "k"}, "authorization", "Token", "abc", "", false},
		{map[string]string{"Authorization": "rawtoken"}, "Authorization", "", "rawtoken", "", false},
		{map[string]string{"X-Trace": "1", "X-Api-Key": "${KEY:-dev}"}, "X-Api-Key", "", "${KEY:-dev}", "KEY", true},
		{map[string]string{"Foo": "bar"}, "Foo", "", "bar", "", false},
		{map[string]string{"Authorization": "Bearer pre-${env:T}"}, "Authorization", "Bearer", "pre-${env:T}", "T", true},
	} {
		h, s, v, vr, u := pickHeader(tc.headers)
		if h != tc.header || s != tc.scheme || v != tc.value || vr != tc.variable || u != tc.unresolved {
			t.Errorf("pickHeader(%v) = %q %q %q %q %v", tc.headers, h, s, v, vr, u)
		}
	}
}

func TestStripJSONC(t *testing.T) {
	in := `{
  // a comment
  "url": "https://a.example.com/mcp", /* another */
  "note": "not // a comment, nor /* this */, and a \"quote\",",
  "list": [1, 2,],
}`
	var out struct {
		URL  string `json:"url"`
		Note string `json:"note"`
		List []int  `json:"list"`
	}
	if err := json.Unmarshal(stripJSONC([]byte(in)), &out); err != nil {
		t.Fatal(err)
	}
	if out.URL != "https://a.example.com/mcp" || out.Note != `not // a comment, nor /* this */, and a "quote",` || len(out.List) != 2 {
		t.Errorf("%+v", out)
	}
	// Unterminated, it doesn't panic.
	for _, s := range []string{`"abc`, `/* x`, `// x`, `"a\`} {
		_ = stripJSONC([]byte(s))
	}
}

func TestNames(t *testing.T) {
	taken := map[string]bool{"notion": true, "notion-2": true}
	if n := uniqueName("notion", taken); n != "notion-3" {
		t.Errorf("uniqueName = %s", n)
	}
	long := strings.Repeat("a", 40)
	if n := uniqueName(long, map[string]bool{long: true}); len(n) != 40 || !strings.HasSuffix(n, "-2") {
		t.Errorf("uniqueName(long) = %s", n)
	}
	for in, want := range map[string]string{"My Server!": "my-server", "": "acme", "memory": "memory-mcp", "__x__": "x"} {
		if got := slugName(in, "https://mcp.acme.io/mcp"); got != want || ValidateName(got) != nil {
			t.Errorf("slugName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, tc := range [][3]string{{"", "my-server", "MY_SERVER_TOKEN"}, {"lower", "x", "X_TOKEN"}, {"API_KEY", "x", "API_KEY"}, {"", "1x", "MCP_1X_TOKEN"}} {
		if got := secretName(tc[0], tc[1]); got != tc[2] {
			t.Errorf("secretName(%q, %q) = %s, want %s", tc[0], tc[1], got, tc[2])
		}
	}
}

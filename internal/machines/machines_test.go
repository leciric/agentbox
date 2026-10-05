package machines

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/mcp"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Memory != DefaultMemory || len(c.Ports) != len(DefaultPorts) || c.DockerInside || len(c.Env()) != 0 {
		t.Errorf("config = %+v", c)
	}
}

func TestLoadReadsTheConfigAndEnvFiles(t *testing.T) {
	wt := t.TempDir()
	write(t, filepath.Join(wt, ConfigFile), `{"docker_inside":true,"memory":"8g","ports":[3000],"env_files":[".env",".env.local","missing"]}`)
	write(t, filepath.Join(wt, ".env"), "# a comment\nA=1\nexport B = 'two words'\nC=\"line\\nbreak\" \nD=x # trailing\n\n")
	write(t, filepath.Join(wt, ".env.local"), "A=overridden\n")
	c, err := Load(wt)
	if err != nil {
		t.Fatal(err)
	}
	if !c.DockerInside || c.Memory != "8g" || len(c.Ports) != 1 {
		t.Errorf("config = %+v", c)
	}
	want := map[string]string{"A": "overridden", "B": "two words", "C": "line\nbreak", "D": "x"}
	for k, v := range want {
		if c.Env()[k] != v {
			t.Errorf("%s = %q, want %q", k, c.Env()[k], v)
		}
	}
}

func TestLoadRefusesABadEnvFile(t *testing.T) {
	wt := t.TempDir()
	write(t, filepath.Join(wt, ".env"), "not a variable\n")
	if _, err := Load(wt); err == nil || !strings.Contains(err.Error(), ".env:1") {
		t.Errorf("err = %v", err)
	}
	write(t, filepath.Join(wt, ConfigFile), `{`)
	if _, err := Load(wt); err == nil {
		t.Error("a broken machine.json loaded")
	}
}

func TestLoadTakesTheMainCheckoutsConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	main := filepath.Join(root, "main")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q", "-b", "main")
	git(main, "commit", "-q", "--allow-empty", "-m", "x")
	linked := filepath.Join(root, "linked")
	git(main, "worktree", "add", "-q", "-b", "feature", linked)
	write(t, filepath.Join(main, ConfigFile), `{"memory":"6g"}`)

	c, err := Load(linked)
	if err != nil || c.Memory != "6g" {
		t.Errorf("config = %+v, %v", c, err)
	}
	wt, err := Worktree(filepath.Join(linked))
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Worktree: wt, Tool: "codex", ID: "s1"}
	it := s.media(context.Background(), "screenshot", "a caption")
	if it.Worktree != wt || it.Repo != "main" || it.Branch != "feature" || it.Tool != "codex" || it.Caption != "a caption" {
		t.Errorf("item = %+v", it)
	}
}

func TestHashChangesWithTheConfig(t *testing.T) {
	a := Config{Memory: "4g", Ports: []int{3000}, env: map[string]string{"K": "1"}}
	b := a
	b.env = map[string]string{"K": "2"}
	c := a
	c.DockerInside = true
	if a.Hash("i") == b.Hash("i") || a.Hash("i") == c.Hash("i") || a.Hash("i") == a.Hash("j") {
		t.Error("two configurations have one hash")
	}
	if b := a; a.Hash("i") != b.Hash("i") {
		t.Error("the hash isn't stable")
	}
}

func TestWorktreeRefusesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Worktree(home); err == nil {
		t.Error("the home directory is a worktree")
	}
	if _, err := Worktree("/"); err == nil {
		t.Error("/ is a worktree")
	}
	dir := filepath.Join(home, "project")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if wt, err := Worktree(dir); err != nil || wt != dir {
		t.Errorf("worktree = %q, %v", wt, err)
	}
}

func TestInstallClaude(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{"numStartups": 3, "mcpServers": {"other": {"command": "x"}}}`)
	tg := Target{"claude", path}
	for i, want := range []bool{true, false} {
		changed, err := Install(tg, "/bin/agentbox")
		if err != nil || changed != want {
			t.Fatalf("install %d: changed %v, %v", i, changed, err)
		}
	}
	var doc struct {
		NumStartups int `json:"numStartups"`
		MCPServers  map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	m := doc.MCPServers[ServerName]
	if doc.NumStartups != 3 || doc.MCPServers["other"].Command != "x" || m.Command != "/bin/agentbox" ||
		strings.Join(m.Args, " ") != "machines mcp --tool claude" {
		t.Errorf("config = %s", data)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode())
	}
	if changed, err := Install(tg, "/elsewhere/agentbox"); err != nil || !changed {
		t.Errorf("a moved binary isn't updated: %v, %v", changed, err)
	}
	for i, want := range []bool{true, false} {
		changed, err := Uninstall(tg)
		if err != nil || changed != want {
			t.Fatalf("uninstall %d: changed %v, %v", i, changed, err)
		}
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), `"`+ServerName+`"`) || !strings.Contains(string(data), `"other"`) {
		t.Errorf("after uninstall: %s", data)
	}
}

func TestInstallClaudeWithoutAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if changed, err := Install(Target{"claude", path}, "/bin/agentbox"); err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	if changed, err := Uninstall(Target{"codex", filepath.Join(t.TempDir(), "config.toml")}); err != nil || changed {
		t.Errorf("uninstalling from no file: %v, %v", changed, err)
	}
}

func TestInstallCodex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	before := "model = \"o3\"\n\n[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.machine]\ncommand = \"old\"\n\n[mcp_servers.machine.env]\nA = \"1\"\n\n[profiles.fast]\nmodel = \"mini\"\n"
	write(t, path, before)
	tg := Target{"codex", path}
	for i, want := range []bool{true, false} {
		changed, err := Install(tg, "/bin/agentbox")
		if err != nil || changed != want {
			t.Fatalf("install %d: changed %v, %v", i, changed, err)
		}
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Count(got, "[mcp_servers.machine]") != 1 || strings.Contains(got, "old") || strings.Contains(got, "machine.env") ||
		!strings.Contains(got, `args = ["machines", "mcp", "--tool", "codex"]`) ||
		!strings.Contains(got, "[profiles.fast]\nmodel = \"mini\"") || !strings.Contains(got, "[mcp_servers.other]") {
		t.Errorf("config.toml =\n%s", got)
	}
	if changed, err := Uninstall(tg); err != nil || !changed {
		t.Fatalf("uninstall: %v, %v", changed, err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "mcp_servers.machine") || !strings.HasPrefix(string(data), "model = \"o3\"") {
		t.Errorf("after uninstall:\n%s", data)
	}
}

func TestIsOurTable(t *testing.T) {
	for h, want := range map[string]bool{
		"[mcp_servers.machine]":         true,
		"[mcp_servers.\"machine\"]":     true,
		"[mcp_servers.machine.env]":     true,
		"[mcp_servers.machine] # ours":  true,
		"[mcp_servers.machines]":        false,
		"[mcp_servers.other.machine]":   false,
		"[[mcp_servers.machine_other]]": false,
	} {
		if isOurTable(h) != want {
			t.Errorf("isOurTable(%q) = %v", h, !want)
		}
	}
}

func TestWithDefaults(t *testing.T) {
	got, err := withDefaults(json.RawMessage(`{"level":"error"}`), map[string]any{"level": "info", "static": false})
	if err != nil || !strings.Contains(string(got), `"level":"error"`) || !strings.Contains(string(got), `"static":false`) {
		t.Errorf("got %s, %v", got, err)
	}
	if got, _ := withDefaults(nil, map[string]any{"a": 1}); string(got) != `{"a":1}` {
		t.Errorf("got %s", got)
	}
	if got, _ := withDefaults(json.RawMessage(`{"x":1}`), nil); string(got) != `{"x":1}` {
		t.Errorf("got %s", got)
	}
}

// TestMCPClient talks to this repository's own MCP server, standing in for
// Playwright's.
func TestMCPClient(t *testing.T) {
	srv := &mcp.Server{Name: "fake", Version: "1", Tools: []mcp.Tool{
		{Name: "echo", Schema: object(nil, map[string]any{}), Run: func(args json.RawMessage) (string, error) {
			return "got " + string(args), nil
		}},
		{Name: "fail", Schema: object(nil, map[string]any{}), Run: func(json.RawMessage) (string, error) {
			return "", io.ErrUnexpectedEOF
		}},
	}}
	toServer, fromClient := io.Pipe()
	fromServer, toClient := io.Pipe()
	go func() {
		_ = srv.Serve(toServer, toClient)
		_ = toClient.Close()
	}()
	c := newClient(fromClient, fromServer)
	if err := c.initialize(); err != nil {
		t.Fatal(err)
	}
	content, err := c.callTool("echo", json.RawMessage(`{"a":1}`))
	if err != nil || len(content) != 1 || content[0].Text != `got {"a":1}` {
		t.Errorf("echo = %+v, %v", content, err)
	}
	if _, err := c.callTool("fail", nil); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("fail = %v", err)
	}
	c.close()
	if _, err := c.callTool("echo", nil); err == nil {
		t.Error("a closed client answered")
	}
}

func TestTail(t *testing.T) {
	if tail("a\nb\n") != "a\nb" {
		t.Errorf("tail = %q", tail("a\nb\n"))
	}
	long := strings.Repeat("line\n", tailLines+10)
	got := tail(long)
	if !strings.HasPrefix(got, "[…]\n") || strings.Count(got, "line") != tailLines {
		t.Errorf("tail of %d lines kept %d", tailLines+10, strings.Count(got, "line"))
	}
	if got := tail(strings.Repeat("x", tailBytes+5)); len(got) != tailBytes+len("[…]\n") {
		t.Errorf("tail kept %d bytes", len(got))
	}
}

func TestDescribe(t *testing.T) {
	if got := describe(Status{Backend: "podman"}); !strings.Contains(got, "isn't running") {
		t.Error(got)
	}
	got := describe(Status{Backend: "docker", Name: "n", Worktree: "/w", Exists: true, Running: true, Memory: "4g",
		DockerInside: true, Ports: map[int]string{5173: "127.0.0.1:2", 3000: "127.0.0.1:1"}})
	if !strings.Contains(got, "Docker inside") || !strings.Contains(got, "3000→http://127.0.0.1:1 5173→http://127.0.0.1:2") {
		t.Error(got)
	}
}

// TestSessionTools runs the tools that need no display against the fake
// runtime, which runs what is exec'd in the machine right here.
func TestSessionTools(t *testing.T) {
	f := newFake(t)
	wt := t.TempDir()
	write(t, filepath.Join(wt, ".env"), "GREETING=hi\n")
	ctx := context.Background()
	serves := 0
	s := &Session{Backend: f.d, Worktree: wt, Serve: func(context.Context) (string, error) {
		serves++
		return "http://127.0.0.1:7790/", nil
	}}
	tools := map[string]mcp.Tool{}
	for _, tool := range s.Tools(ctx) {
		tools[tool.Name] = tool
		if tool.Description == "" || len(tool.Description) > 200 {
			t.Errorf("%s's description: %q", tool.Name, tool.Description)
		}
	}
	for _, name := range []string{"machine_start", "machine_stop", "machine_status", "run", "preview_url", "view_url",
		"screenshot", "record_start", "record_stop", "click", "type", "key", "scroll", "browser_navigate", "browser_snapshot"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("no %s tool", name)
		}
	}
	call := func(name, args string) (string, error) {
		t.Helper()
		tool := tools[name]
		if tool.Wait != nil {
			return tool.Wait(ctx, json.RawMessage(args))
		}
		return tool.Run(json.RawMessage(args))
	}

	if _, err := call("run", `{"command":"true"}`); err == nil || !strings.Contains(err.Error(), "machine_start") {
		t.Errorf("run without a machine: %v", err)
	}
	if _, err := call("view_url", `{}`); err == nil || !strings.Contains(err.Error(), "machine_start") || serves != 0 {
		t.Errorf("view_url without a machine: %v, serve started %d times", err, serves)
	}
	cfg, _ := Load(wt)
	f.made(t, wt, cfg.Hash(ImageTag()), true)
	if out, err := call("machine_start", `{}`); err != nil || !strings.Contains(out, "3000→http://127.0.0.1:49153") {
		t.Fatalf("machine_start = %q, %v", out, err)
	}
	if out, err := call("run", `{"command":"pwd; echo out; echo err >&2"}`); err != nil ||
		out != wt+"\nout\nerr\n[exit 0]" {
		t.Errorf("run = %q, %v", out, err)
	}
	if out, err := call("run", `{"command":"exit 3"}`); err != nil || !strings.HasSuffix(out, "[exit 3]") {
		t.Errorf("run = %q, %v", out, err)
	}
	if out, err := call("run", `{"command":"sleep 5","timeout":1}`); err != nil || !strings.Contains(out, "stopped after 1s") {
		t.Errorf("run = %q, %v", out, err)
	}
	if _, err := call("run", `{"job":"../x"}`); err == nil {
		t.Error("a path was a job")
	}
	if _, err := call("run", `{}`); err == nil {
		t.Error("run without a command")
	}
	if out, err := call("preview_url", `{"port":3000}`); err != nil || out != "http://127.0.0.1:49153" {
		t.Errorf("preview_url = %q, %v", out, err)
	}
	if _, err := call("preview_url", `{"port":9999}`); err == nil || !strings.Contains(err.Error(), ConfigFile) {
		t.Errorf("an unpublished port: %v", err)
	}
	if out, err := call("view_url", `{}`); err != nil || out != "http://127.0.0.1:7790/#machine="+containerName(wt) || serves != 1 {
		t.Errorf("view_url = %q, %v", out, err)
	}
	if out, err := call("machine_status", `{}`); err != nil || !strings.Contains(out, "Running") {
		t.Errorf("machine_status = %q, %v", out, err)
	}
	if _, err := call("machine_stop", `{}`); err != nil {
		t.Error(err)
	}
	if len(commandsLike(f.log(t), "stop -t 5 "+containerName(wt))) != 1 {
		t.Errorf("not stopped:\n%s", strings.Join(f.log(t), "\n"))
	}
}

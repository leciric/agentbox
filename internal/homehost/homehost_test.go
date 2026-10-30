package homehost

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// attach connects a Host to a Hub over a pipe, as the helper's WebSocket does.
func attach(t *testing.T, h *Host) (*Hub, chan bool, func()) {
	t.Helper()
	hub := &Hub{}
	changes := make(chan bool, 4)
	hub.OnChange = func(c bool) { changes <- c }
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = hub.Attach(ctx, a) }()
	go func() { _ = h.Serve(ctx, b) }()
	if !<-changes {
		t.Fatal("the helper didn't attach")
	}
	return hub, changes, cancel
}

// fakeTool is a "tool" that echoes its input, says something on stderr, and
// says where it runs.
func fakeTool(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode")
	script := "#!/bin/sh\necho \"cwd $(pwd) $1 $HOST_ENV ${LOGIN_ENV:-none}\"\necho oops >&2\ncat\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSpawnCarriesTheAdaptersStdio(t *testing.T) {
	tool := fakeTool(t)
	dir := filepath.Join(t.TempDir(), "home-chat")
	h := &Host{Version: "test", Dir: dir, Self: "/bin/agentbox", Socket: "/run/sock",
		Find: func() map[string]Tool { return map[string]Tool{"opencode": {Path: tool, Login: true}} }}
	hub, _, stop := attach(t, h)
	defer stop()

	r := hub.Runner()
	info, err := r.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Dir != dir || info.Tools["opencode"].Path != tool || info.Version != "test" {
		t.Fatalf("probe = %+v", info)
	}

	p, err := r.Spawn(context.Background(), Spec{
		Tool: "opencode", Files: map[string]string{"AGENTS.md": "the brief"},
		Env: []string{"HOST_ENV=set"}, LoginEnv: []string{"LOGIN_ENV=agentbox"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cwd != dir || len(p.MCP) != 1 || p.MCP[0].Command != "/bin/agentbox" || p.MCP[0].Env[0] != SocketEnv+"=/run/sock" {
		t.Fatalf("proc = cwd %q, mcp %+v", p.Cwd, p.MCP)
	}
	out := bufio.NewReader(p.Stdout)
	line, _ := out.ReadString('\n')
	// The host has its own login, so AgentBox's isn't passed.
	if want := "cwd " + dir + " acp set none\n"; line != want {
		t.Fatalf("first line = %q, want %q", line, want)
	}
	if _, err := p.Stdin.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	if line, _ := out.ReadString('\n'); line != "ping\n" {
		t.Fatalf("echo = %q", line)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "AGENTS.md")); err != nil || string(b) != "the brief" {
		t.Fatalf("brief = %q, %v", b, err)
	}
	p.Stop()
	if err := p.Wait(); err != nil {
		t.Fatalf("wait = %v", err)
	}
	if !strings.Contains(p.Stderr(), "oops") {
		t.Fatalf("stderr = %q", p.Stderr())
	}
}

func TestSpawnUsesAgentBoxLoginWithoutTheHosts(t *testing.T) {
	tool := fakeTool(t)
	h := &Host{Dir: t.TempDir(), Find: func() map[string]Tool { return map[string]Tool{"opencode": {Path: tool}} }}
	hub, _, stop := attach(t, h)
	defer stop()
	p, err := hub.Runner().Spawn(context.Background(), Spec{Tool: "opencode", LoginEnv: []string{"LOGIN_ENV=agentbox"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(p.Stdout).ReadString('\n')
	if !strings.HasSuffix(line, " agentbox\n") {
		t.Fatalf("first line = %q", line)
	}
	p.Stop()
}

func TestSpawnRefusesAMissingTool(t *testing.T) {
	h := &Host{Dir: t.TempDir(), Find: func() map[string]Tool { return map[string]Tool{"claude": {}} }}
	hub, _, stop := attach(t, h)
	defer stop()
	_, err := hub.Runner().Spawn(context.Background(), Spec{Tool: "claude"}, nil)
	if err == nil || !strings.Contains(err.Error(), "Claude Code isn't installed on this computer") {
		t.Fatalf("err = %v", err)
	}
}

func TestFilesStayInTheChatsFolder(t *testing.T) {
	h := &Host{Dir: t.TempDir()}
	for _, rel := range []string{"../escape", "/etc/passwd", ".."} {
		if err := h.writeFiles(map[string]string{rel: "x"}); err == nil {
			t.Errorf("%q was written", rel)
		}
	}
	if err := h.writeFiles(map[string]string{".claude/settings.json": "{}"}); err != nil {
		t.Fatal(err)
	}
}

func TestHelperGoingEndsItsProcesses(t *testing.T) {
	tool := fakeTool(t)
	h := &Host{Dir: t.TempDir(), Find: func() map[string]Tool { return map[string]Tool{"opencode": {Path: tool, Login: true}} }}
	hub, changes, stop := attach(t, h)
	p, err := hub.Runner().Spawn(context.Background(), Spec{Tool: "opencode"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	select {
	case c := <-changes:
		if c {
			t.Fatal("attached again")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hub didn't notice the helper go")
	}
	if hub.Runner() != nil {
		t.Fatal("a runner is still attached")
	}
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a process whose helper went ended cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait didn't return after the helper went")
	}
}

func TestHasLogin(t *testing.T) {
	home := t.TempDir()
	none := func(string) string { return "" }
	if hasLogin("claude", home, none) && !keychainHas("Claude Code-credentials") {
		t.Fatal("an empty home has a Claude Code login")
	}
	if !hasLogin("claude", home, func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "k"
		}
		return ""
	}) {
		t.Fatal("an API key isn't a login")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if hasLogin("codex", home, none) {
		t.Fatal("codex without auth.json has a login")
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasLogin("codex", home, none) {
		t.Fatal("codex's auth.json isn't a login")
	}
}

// PATH comes from the user's login shell, whatever its profile prints.
func TestLoginShellPath(t *testing.T) {
	shell := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\necho 'Welcome back!'\nprintf '\\n@@PATH@@/opt/one:/usr/bin@@PATH@@\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/usr/bin:/bin")
	got := loginShellPath()
	if len(got) == 0 || got[0] != "/opt/one" {
		t.Fatalf("loginShellPath() = %q", got)
	}
	t.Setenv("HOME", "/home/someone")
	WidenPath()
	path := os.Getenv("PATH")
	if !strings.HasPrefix(path, "/opt/one:") || !strings.Contains(path, "/home/someone/.local/bin") || strings.Count(path, "/opt/one") != 1 {
		t.Fatalf("WidenPath() = %q", path)
	}
}

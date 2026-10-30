package daemon

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/credentials"
	"agentbox/internal/homehost"
)

// The Home chat's adapter starts on the user's computer through the app's
// helper when it is connected and has the tool, and in the VM otherwise,
// saying why.
func TestHomeChatRunsOnTheHost(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if err := (credentials.Store{Dir: d.paths.Credentials()}).SaveClaudeToken("", "agentbox-token"); err != nil {
		t.Fatal(err)
	}
	m := d.srv.manager(nil)
	a, err := m.EnsureHome(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// No helper: the VM, and nothing said, as for the CLI or the phone.
	if proc, where := d.srv.launchHome(ctx, m, a, func(string) {}); proc != nil || where != (placed{}) {
		t.Fatalf("with no helper launchHome = %v, %+v", proc, where)
	}

	// A host with an adapter that says what it was started with.
	bin := t.TempDir()
	adapter := "#!/bin/sh\necho \"$(pwd) token=${CLAUDE_CODE_OAUTH_TOKEN:-none} exe=$CLAUDE_CODE_EXECUTABLE\"\ncat\n"
	if err := os.WriteFile(filepath.Join(bin, "claude-agent-acp"), []byte(adapter), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tools := map[string]homehost.Tool{"claude": {}}
	dir := filepath.Join(t.TempDir(), "home-chat")
	h := &homehost.Host{Dir: dir, Self: "/host/agentbox", Socket: "/host/sock", Find: func() map[string]homehost.Tool { return tools }}
	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, err := homehost.Dial(hctx, d.paths.Socket(), homehost.Path)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = h.Serve(hctx, conn) }()
	deadline := time.Now().Add(5 * time.Second)
	for d.srv.homeHost.Runner() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the helper never attached")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Claude Code isn't installed there.
	proc, where := d.srv.launchHome(ctx, m, a, func(string) {})
	if proc != nil || !strings.Contains(where.notice, "Claude Code isn't installed on your computer") || where.moved {
		t.Fatalf("without Claude Code launchHome = %v, %+v", proc, where)
	}

	// It is, with no login of its own: AgentBox's account, and the brief and
	// settings in the chat's folder there.
	tools["claude"] = homehost.Tool{Path: "/host/bin/claude"}
	proc, _ = d.srv.launchHome(ctx, m, a, func(string) {})
	if proc == nil {
		t.Fatal("launchHome didn't start on the host")
	}
	defer proc.Stop()
	if proc.Cwd != dir || len(proc.MCPServers) != 1 || proc.MCPServers[0].Command != "/host/agentbox" ||
		strings.Join(proc.MCPServers[0].Args, " ") != "home-host mcp" {
		t.Fatalf("proc = cwd %q, mcp %+v", proc.Cwd, proc.MCPServers)
	}
	if !strings.Contains(proc.Notice, "runs on your computer") || !strings.Contains(proc.Notice, "AgentBox's Claude Code account") || !proc.Moved {
		t.Errorf("notice = %q, moved %v", proc.Notice, proc.Moved)
	}
	line, _ := bufio.NewReader(proc.Stdout).ReadString('\n')
	if want := dir + " token=agentbox-token exe=/host/bin/claude\n"; line != want {
		t.Errorf("the adapter said %q, want %q", line, want)
	}
	brief, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || !strings.Contains(string(brief), "a shell on the user's own computer") {
		t.Errorf("CLAUDE.md = %q, %v", brief, err)
	}
	settings, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(settings), `"defaultMode": "default"`) {
		t.Errorf("settings.json = %s, %v", settings, err)
	}

	// The app closing puts it back in the VM, and says so.
	cancel()
	deadline = time.Now().Add(5 * time.Second)
	for d.srv.homeHost.Runner() != nil {
		if time.Now().After(deadline) {
			t.Fatal("the helper never went")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if proc, where := d.srv.launchHome(ctx, m, a, func(string) {}); proc != nil || !strings.Contains(where.notice, "app isn't open") || !where.moved {
		t.Fatalf("after the app closed launchHome = %v, %+v", proc, where)
	}
}

// The MCP server the host's tool starts carries its stdio to `agentbox mcp`
// here, pointed at the Home chat's own API.
func TestHomeHostMCPRunsHere(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	bin := filepath.Join(t.TempDir(), "agentbox")
	script := "#!/bin/sh\necho \"$1 $AGENTBOX_CHAT $AGENTBOX_SOCKET\"\ncat\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d.srv.cfg.Binary = bin
	conn, err := homehost.Dial(context.Background(), d.paths.Socket(), homehost.MCPPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	out := bufio.NewReader(conn)
	line, _ := out.ReadString('\n')
	if want := "mcp home " + d.srv.leadSocketPath("_home") + "\n"; line != want {
		t.Fatalf("the MCP server started as %q, want %q", line, want)
	}
	if _, err := conn.Write([]byte("{\"jsonrpc\":\"2.0\"}\n")); err != nil {
		t.Fatal(err)
	}
	if line, _ := out.ReadString('\n'); line != "{\"jsonrpc\":\"2.0\"}\n" {
		t.Fatalf("echo = %q", line)
	}
}

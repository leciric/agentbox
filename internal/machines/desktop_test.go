package machines

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/desktop"
	"agentbox/internal/mcp"
)

// fakeDesktopEnv makes this test binary the machine's desktop server: the
// fake runtime execs it in the "machine", standing in for `agentbox machines
// desktop-mcp`.
const fakeDesktopEnv = "MACHINES_TEST_FAKE_DESKTOP"

func TestMain(m *testing.M) {
	if os.Getenv(fakeDesktopEnv) == "1" {
		srv := &mcp.Server{Name: "fake-desktop", Version: "1", Tools: []mcp.Tool{
			{Name: "screenshot", RunContent: func(json.RawMessage) ([]mcp.Content, error) {
				return []mcp.Content{mcp.Image([]byte("jpg"), "image/jpeg"), mcp.Text("The screenshot is 640×400: give coordinates in its pixels.")}, nil
			}},
			{Name: "click", RunContent: func(args json.RawMessage) ([]mcp.Content, error) {
				return []mcp.Content{mcp.Text("clicked " + string(args))}, nil
			}},
			{Name: "windows", Run: func(json.RawMessage) (string, error) { return "- 0x1 — Terminal", nil }},
		}}
		_ = srv.Serve(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestDesktopToolsAreRelayed starts the machine on the fake runtime and calls
// the desktop tools, which reach the desktop server run in it.
func TestDesktopToolsAreRelayed(t *testing.T) {
	t.Setenv(fakeDesktopEnv, "1")
	saved := desktopServer
	desktopServer.command = []string{os.Args[0], "-test.run=^$"}
	t.Cleanup(func() { desktopServer = saved })

	f := newFake(t)
	wt := t.TempDir()
	ctx := context.Background()
	s := &Session{Backend: f.d, Worktree: wt}
	tools := map[string]mcp.Tool{}
	for _, tool := range s.Tools(ctx) {
		tools[tool.Name] = tool
	}
	// An agent's own descriptions and schemas.
	for _, d := range desktop.Tools(ctx) {
		got, ok := tools[d.Name]
		switch {
		case !ok:
			t.Errorf("no %s tool", d.Name)
		case d.Name == "screenshot":
			if !strings.HasPrefix(got.Description, d.Description) {
				t.Errorf("screenshot's description: %q", got.Description)
			}
		case got.Description != d.Description:
			t.Errorf("%s's description is %q, not an agent's", d.Name, got.Description)
		}
	}

	if _, err := tools["click"].RunContent(json.RawMessage(`{"x":1,"y":2}`)); err == nil || !strings.Contains(err.Error(), "machine_start") {
		t.Errorf("click without a machine: %v", err)
	}
	cfg, _ := Load(wt)
	f.made(t, wt, cfg.Hash(ImageTag()), true)
	if _, err := tools["machine_start"].Wait(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	bin, _ := LinuxAgentBinary()
	if got := commandsLike(f.log(t), "cp "); len(got) != 1 || got[0] != "cp "+bin+" "+containerName(wt)+":"+AgentBinary {
		t.Errorf("agentbox copied into the machine with %q", got)
	}

	content, err := tools["click"].RunContent(json.RawMessage(`{"x":1,"y":2}`))
	if err != nil || len(content) != 1 || content[0].Text != `clicked {"x":1,"y":2}` {
		t.Errorf("click = %+v, %v", content, err)
	}
	content, err = tools["windows"].RunContent(nil)
	if err != nil || len(content) != 1 || !strings.Contains(content[0].Text, "Terminal") {
		t.Errorf("windows = %+v, %v", content, err)
	}
	// The relayed image and its note, then what saving it said.
	content, err = tools["screenshot"].RunContent(json.RawMessage(`{"caption":"c"}`))
	if err != nil || len(content) != 3 || content[0].Type != "image" || !strings.Contains(content[1].Text, "640×400") {
		t.Errorf("screenshot = %+v, %v", content, err)
	}
	if n := len(commandsLike(f.log(t), "exec -i -u 1000:1000 -e HOME="+machineHome+" -e DISPLAY=:99 -w "+wt+" "+containerName(wt)+" "+os.Args[0])); n != 1 {
		t.Errorf("the desktop server started %d times, not once", n)
	}

	// A server that stopped is started again.
	_ = s.clients[desktopServer.what].cmd.Process.Kill()
	if content, err := tools["click"].RunContent(json.RawMessage(`{}`)); err != nil || len(content) != 1 {
		t.Errorf("click after the server stopped = %+v, %v", content, err)
	}
	if n := len(commandsLike(f.log(t), "exec -i -u 1000:1000 -e HOME="+machineHome+" -e DISPLAY=:99 -w "+wt+" "+containerName(wt)+" "+os.Args[0])); n != 2 {
		t.Errorf("the desktop server started %d times, not twice", n)
	}
	s.closeClients()
}

func TestBrowserToolsAreOptIn(t *testing.T) {
	wt := t.TempDir()
	s := &Session{Backend: newFake(t).d, Worktree: wt}
	has := func() bool {
		for _, tool := range s.Tools(context.Background()) {
			if tool.Name == "browser_navigate" {
				return true
			}
		}
		return false
	}
	if has() || strings.Contains(s.Instructions(), "browser_") {
		t.Error("browser tools without browser_tools")
	}
	if !strings.Contains(s.Instructions(), "desktop tools") {
		t.Errorf("instructions: %q", s.Instructions())
	}
	write(t, filepath.Join(wt, ConfigFile), `{"browser_tools": true}`)
	if !has() || !strings.Contains(s.Instructions(), "browser_") {
		t.Error("no browser tools with browser_tools")
	}
}

package machines

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"agentbox/internal/desktop"
	"agentbox/internal/hostvm"
	"agentbox/internal/mcp"
)

// The desktop tools are AgentBox's own, the ones an agent gets from
// `agentbox desktop mcp`: a real pointer and keyboard on the machine's
// display, so what they do shows in the live view and in recordings, and works
// in any window — the browser, the terminal, a file dialog, the app's own. The
// agentbox Start copies into the machine serves them there (`agentbox
// machines desktop-mcp`), and they are relayed, with an agent's descriptions.

// AgentBinary is where Start puts agentbox in the machine.
const AgentBinary = "/usr/local/bin/agentbox"

var desktopServer = server{"the desktop's tools", []string{AgentBinary, "machines", "desktop-mcp"}}

// LinuxAgentBinary is the agentbox to copy into a machine: this one on Linux,
// and on a Mac the Linux build the app keeps next to it, which its VM runs too.
func LinuxAgentBinary() (string, error) {
	if runtime.GOOS != "linux" {
		return hostvm.FindLinuxBinary()
	}
	if p := os.Getenv("AGENTBOX_LINUX_BINARY"); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// desktopTools are the desktop's tools but screenshot, which the session has
// its own of, relayed to the machine.
func (s *Session) desktopTools(ctx context.Context) []mcp.Tool {
	var tools []mcp.Tool
	for _, t := range desktop.Tools(ctx) {
		if t.Name == "screenshot" {
			continue
		}
		name := t.Name
		t.Run, t.Wait = nil, nil
		t.RunContent = func(args json.RawMessage) ([]mcp.Content, error) { return s.relay(ctx, desktopServer, name, args) }
		tools = append(tools, t)
	}
	return tools
}

// desktopDescription is the desktop's own description of a tool.
func desktopDescription(name string) string {
	for _, t := range desktop.Tools(context.Background()) {
		if t.Name == name {
			return t.Description
		}
	}
	return ""
}

// Instructions are the server's, which say how its tools go together.
func (s *Session) Instructions() string {
	text := "This worktree's desktop machine: a Linux container with the worktree at the same path and a display " +
		"the user watches. machine_start starts it, run runs commands in it. Operate the app with the desktop tools " +
		"(click, type, key, scroll, drag, mouse_move): they are a real pointer and keyboard on the display, so the user " +
		"sees them in the live view and recordings, and they work in every window, the browser, the terminal, file " +
		"dialogs and the app's own. Look with screenshot. record_start and record_stop film a walkthrough."
	if s.browserTools() {
		text += " The browser_* tools read the page's DOM, its console and requests; operate the page with the desktop tools."
	}
	return text
}

// browserTools reports whether the worktree turned Playwright's tools on.
func (s *Session) browserTools() bool {
	if s.Worktree == "" {
		return false
	}
	c, err := s.load()
	return err == nil && c.BrowserTools
}

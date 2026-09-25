package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"agentbox/internal/image"
	"agentbox/internal/state"
)

// ChatAdapter runs an AI tool for the app's chat: an Agent Client Protocol
// adapter, installed in agents with mise.
type ChatAdapter struct {
	Tool    string // the AI tool it runs
	Command string
	// Args are what the command needs to speak ACP rather than do its usual
	// job. Claude Code and Codex have adapters of their own and need none;
	// OpenCode speaks ACP itself, behind its `acp` subcommand.
	Args []string
	// Package is mise's name for the adapter, with the version AgentBox uses:
	// the one the base image pins (internal/image/tools.txt).
	Package string
}

var ChatAdapters = map[string]ChatAdapter{
	"claude":   {Tool: "Claude Code", Command: "claude-agent-acp", Package: image.Pin("npm:@agentclientprotocol/claude-agent-acp")},
	"codex":    {Tool: "Codex", Command: "codex-acp", Package: image.Pin("npm:@agentclientprotocol/codex-acp")},
	"opencode": {Tool: "OpenCode", Command: "opencode", Args: []string{"acp"}, Package: image.Pin("npm:opencode-ai")},
}

// installTimeout bounds installing an adapter in an agent whose machine doesn't have it.
const installTimeout = 5 * time.Minute

// The AI tool a project's lead runs on the host. Agents get theirs from the
// base image; the lead has no machine, so AgentBox fetches it once per host.
const (
	ChatTool = "claude"
	// ChatToolVersion is a release channel the installer understands. "stable"
	// is about a week behind "latest" and skips releases with major regressions.
	ChatToolVersion = "stable"
	installerURL    = "https://claude.ai/install.sh"
)

// LeadChatCommand prepares the command that runs a project's lead: its ACP
// adapter, on the host, in a private HOME with only AgentBox's Claude Code
// login, reading the lead's detached worktree. Tools missing from this machine
// are installed first, which status reports.
func (m *Manager) LeadChatCommand(ctx context.Context, a state.Agent, status func(detail string)) (*exec.Cmd, error) {
	if !a.IsLead() {
		return nil, fmt.Errorf("%s isn't a project's chat", a.Ref())
	}
	tools, err := m.hostTools(ctx, status)
	if err != nil {
		return nil, err
	}
	status("Starting " + ChatAdapters["claude"].Tool)
	home := m.Paths.LeadHome(a.Project)
	cmd := exec.CommandContext(ctx, tools.Adapter)
	cmd.Dir = a.Worktree
	// A fresh environment, not the user's: the lead gets AgentBox's login and
	// nothing else that could point Claude Code at the host's own state. The
	// exception is toolEnv, without which the tools can't find themselves.
	env := []string{
		"HOME=" + home,
		"PATH=" + tools.Bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"LANG=C.UTF-8",
	}
	env = append(env, toolEnv()...)
	if term := os.Getenv("TERM"); term != "" {
		env = append(env, "TERM="+term)
	}
	token, err := m.Creds.ClaudeToken(a.ClaudeAccount)
	if err != nil {
		return nil, err
	}
	if token != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+token)
	}
	cmd.Env = env
	return cmd, nil
}

// ChatCommand prepares the command that runs an agent's ACP adapter, as the
// agent's user in its worktree, speaking over the command's stdin and stdout.
// An agent whose machine lacks the adapter's version gets it installed first,
// which status reports.
func (m *Manager) ChatCommand(ctx context.Context, a state.Agent, status func(detail string)) (*exec.Cmd, error) {
	adapter, ok := ChatAdapters[a.AI]
	if !ok {
		return nil, fmt.Errorf("%s runs no AI tool to chat with", a.Ref())
	}
	if err := m.requireRunning(ctx, a); err != nil {
		return nil, err
	}
	pkg := shellQuote(adapter.Package)
	if m.Incus.UserExec(ctx, a.Instance, m.User.Name, "mise where "+pkg, nil, io.Discard, io.Discard) != nil {
		status("Installing the " + adapter.Tool + " adapter in this agent")
		install, cancel := context.WithTimeout(ctx, installTimeout)
		defer cancel()
		var out bytes.Buffer
		if err := m.Incus.UserExec(install, a.Instance, m.User.Name, "MISE_YES=1 mise install "+pkg, nil, &out, &out); err != nil {
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			return nil, fmt.Errorf("installing %s in %s: %w: %s", adapter.Package, a.Ref(), err, strings.Join(lines[max(len(lines)-3, 0):], " "))
		}
	}
	status("Starting " + adapter.Tool)
	// mise exec runs the version AgentBox pins, whatever the agent's own mise configuration says.
	command := shellQuote(adapter.Command)
	for _, arg := range adapter.Args {
		command += " " + shellQuote(arg)
	}
	script := fmt.Sprintf("cd %s && exec mise exec %s -- %s", shellQuote(a.Worktree), pkg, command)
	return m.Incus.Command(ctx, "exec", a.Instance, "-T", "--", "runuser", "-l", m.User.Name, "-c", script), nil
}

// interfaceFor checks the interface asked for a new agent: the chat unless it
// says otherwise, and the command line for an agent without an AI tool.
func interfaceFor(ai, iface string) (string, error) {
	switch {
	case iface != "" && iface != state.InterfaceChat && iface != state.InterfaceCLI:
		return "", fmt.Errorf("unknown interface %q: use chat or cli", iface)
	case ai == "none" && iface == state.InterfaceChat:
		return "", errors.New("an agent without an AI tool has nothing to chat with: use claude, codex or opencode")
	case ai == "none", iface == state.InterfaceCLI:
		return state.InterfaceCLI, nil
	}
	return state.InterfaceChat, nil
}

// SetInterface switches between the chat and the AI tool's command line.
// Switching to the command line starts the tool in the agent's tmux session
// while the agent runs; stopping the chat's session is the caller's part.
func (m *Manager) SetInterface(ctx context.Context, a state.Agent, iface string) (state.Agent, error) {
	adapter, ok := ChatAdapters[a.AI]
	if !ok {
		return a, fmt.Errorf("%s runs no AI tool: it has only a shell", a.Ref())
	}
	if iface != state.InterfaceChat && iface != state.InterfaceCLI {
		return a, fmt.Errorf("unknown interface %q: use chat or cli", iface)
	}
	if err := m.Store.SetAgentInterface(ctx, a.Project, a.Name, iface); err != nil {
		return a, err
	}
	a.Interface = iface
	if iface != state.InterfaceCLI {
		return a, nil
	}
	if inst, err := m.Incus.Instance(ctx, a.Instance); err != nil || inst.Status != "Running" {
		return a, nil // the session starts with the agent
	}
	script := fmt.Sprintf("tmux has-session -t %[1]s 2>/dev/null || exit 0\ntmux list-windows -t %[1]s -F '#W' | grep -qx %[2]s && exit 0\n",
		session, shellQuote(a.AI)) + toolWindowScript(a)
	var out bytes.Buffer
	if err := m.Incus.UserExec(ctx, a.Instance, m.User.Name, script, nil, &out, &out); err != nil {
		return a, fmt.Errorf("starting %s in the terminal: %w: %s", adapter.Tool, err, strings.TrimSpace(out.String()))
	}
	return a, nil
}

// toolWindowScript opens tmux window 1 with the AI tool's command line, for an
// agent you use from the terminal rather than the chat.
func toolWindowScript(a state.Agent) string {
	tool := Tools[a.AI]
	command := tool.Command
	if a.Autonomous {
		command = tool.Autonomous
	}
	if command == "" || a.Interface == state.InterfaceChat {
		return ""
	}
	// Typed into a shell, so the window stays open when the tool exits.
	return fmt.Sprintf("tmux new-window -t %[1]s -n %[2]s -c %[3]s\ntmux send-keys -t %[1]s:%[2]s %[4]s Enter\n",
		session, a.AI, shellQuote(a.Worktree), shellQuote(command))
}

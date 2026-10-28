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
	"sync"
	"time"

	"agentbox/internal/chat"
	"agentbox/internal/cursor"
	"agentbox/internal/hostos"
	"agentbox/internal/image"
	"agentbox/internal/opencode"
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
	// Script, when set, is an adapter AgentBox carries itself: written into
	// the agent at ScriptPath as the chat starts and run with node, with
	// Package (what it runs on) installed where SDKEnv says.
	Script     []byte
	ScriptPath string // under the agent's home
	SDKEnv     string
}

var ChatAdapters = map[string]ChatAdapter{
	"claude":   {Tool: "Claude Code", Command: "claude-agent-acp", Package: image.Pin("npm:@agentclientprotocol/claude-agent-acp")},
	"codex":    {Tool: "Codex", Command: "codex-acp", Package: image.Pin("npm:@agentclientprotocol/codex-acp")},
	"opencode": {Tool: "OpenCode", Command: "opencode", Args: []string{"acp"}, Package: image.Pin("opencode")},
	// Cursor's adapter is AgentBox's own, over Cursor's TypeScript SDK
	// (internal/cursor): Package is the SDK.
	"cursor": {Tool: "Cursor", Command: "node", Package: cursor.SDKPackage, Script: cursor.Script, ScriptPath: cursorScriptFile, SDKEnv: cursor.SDKEnv},
}

// openCodeAdapter is the chat adapter for the OpenCode this installation runs.
// OpenCode 2 is not the mise tool the base image installs as `opencode`: it is
// linked beside it as `opencode2`, so it runs by name rather than through mise.
func openCodeAdapter(adapter ChatAdapter, version opencode.Release) ChatAdapter {
	adapter.Command = version.Command()
	if version == opencode.V2 {
		adapter.Package = ""
	}
	return adapter
}

// claudeEntrypoint is the client label Claude Code reports, which decides how
// much of the 5-hour window a turn costs. The Agent SDK defaults to "sdk-ts",
// which for the same work used 8 points of the account's 1% meter per session,
// against 3 for the VS Code extension's label. Our chat drives the same
// transport and uses our own label: measured at 2-3 points, the same as the
// extension. Inferred from a small sample (one account, two sessions per
// label). The terminal interface really is "cli" and sets nothing.
const claudeEntrypoint = "CLAUDE_CODE_ENTRYPOINT=agentbox"

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
// adapter, on the host, in a private HOME with only AgentBox's login for its
// tool, reading the lead's detached worktree. Tools missing from this machine
// are installed first, which status reports.
func (m *Manager) LeadChatCommand(ctx context.Context, a state.Agent, status func(detail string)) (*exec.Cmd, error) {
	if !a.IsLead() {
		return nil, fmt.Errorf("%s isn't a project's chat", a.Ref())
	}
	home := m.leadHome(a)
	command, args, toolEnvVars, bin, err := m.leadAdapter(ctx, a, home, status)
	if err != nil {
		return nil, err
	}
	status("Starting " + ChatAdapters[a.AI].Tool)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = a.Worktree
	// A fresh environment, not the user's: the lead gets AgentBox's login and
	// nothing else that could point its tool at the host's own state. The
	// exception is toolEnv, without which the tools can't find themselves.
	env := []string{
		"HOME=" + home,
		"PATH=" + strings.Join(append(bin, os.Getenv("PATH")), string(os.PathListSeparator)),
		"LANG=C.UTF-8",
	}
	env = append(env, toolEnv()...)
	if a.AI == "claude" {
		env = append(env, claudeEntrypoint)
	}
	if term := os.Getenv("TERM"); term != "" {
		env = append(env, "TERM="+term)
	}
	// What the front end told the daemon about the host. A VM made before
	// /etc/agentbox/vm was has nothing else to say it's AgentBox's VM, and the
	// lead's HOME holds no state.db: without these, its `agentbox mcp` takes
	// itself for the Mac's front end and quits (hostvm.Front).
	for _, name := range []string{hostos.Env, hostos.HomeEnv} {
		if v := os.Getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	env = append(env, toolEnvVars...)
	// The project's GitHub account, as its agents have it (agentEnv), for gh
	// and for git through the helper configureLead wrote.
	p := homeProject
	if !a.IsHome() {
		if p, err = m.Store.Project(ctx, a.Project); err != nil {
			return nil, err
		}
	}
	if gh := m.leadGitHubToken(p); gh != "" {
		env = append(env, "GH_TOKEN="+gh, "GITHUB_TOKEN="+gh)
	}
	cmd.Env = env
	return cmd, nil
}

// ChatCommand prepares the command that runs an agent's ACP adapter, as the
// agent's user in its worktree, speaking over the command's stdin and stdout.
// An agent whose machine lacks the adapter's version gets it installed first,
// which status reports.
func (m *Manager) ChatCommand(ctx context.Context, a state.Agent, status func(detail string)) (*exec.Cmd, error) {
	script, err := m.chatScript(ctx, a, status)
	if err != nil {
		return nil, err
	}
	return m.Incus.Command(ctx, "exec", a.Instance, "-T", "--", "runuser", "-l", m.User.Name, "-c", script), nil
}

// chatScript is the shell command line that runs an agent's ACP adapter inside
// its machine. ChatCommand hands it to `incus exec`, and ChatProcess hands it
// to a pod's UserExec, so both runtimes get the same adapter at the same
// version. Everything that has to happen before the adapter starts happens
// here: the machine has to be running, the adapter's version installed, and
// bash_env written when the tool is started without some of its credentials.
func (m *Manager) chatScript(ctx context.Context, a state.Agent, status func(detail string)) (string, error) {
	adapter, ok := ChatAdapters[a.AI]
	if !ok {
		return "", fmt.Errorf("%s runs no AI tool to chat with", a.Ref())
	}
	if err := m.requireRunning(ctx, a); err != nil {
		return "", err
	}
	if a.AI == "opencode" {
		version, err := m.openCodeVersion(ctx)
		if err != nil {
			return "", err
		}
		adapter = openCodeAdapter(adapter, version)
		if version == opencode.V2 {
			// OpenCode 2 is not in the image: the agent installs it here, at
			// its first start on it (pinned by image.OpenCode2Version), so a
			// switch doesn't break the agents already running.
			if err := m.ensureOpenCode2InAgent(ctx, a); err != nil {
				return "", err
			}
		}
	}
	// A pod's image carries the adapters at the pins (internal/image/tools.txt)
	// and has no mise, so the adapter runs by name. Cursor's adapter is
	// AgentBox's own script and needs the Cursor SDK, which the image does not
	// carry yet: it says so rather than failing inside node.
	if a.OnCluster() {
		if adapter.Script != nil {
			return "", fmt.Errorf("%s runs %s, whose adapter needs a package the agent image does not carry yet: use Claude Code, Codex or OpenCode for an agent in a cluster", a.Ref(), adapter.Tool)
		}
		status("Starting " + adapter.Tool)
		command := shellQuote(adapter.Command)
		for _, arg := range adapter.Args {
			command += " " + shellQuote(arg)
		}
		hide, err := m.toolHides(a)
		if err != nil {
			return "", err
		}
		if len(hide) > 0 {
			if err := m.writeBashEnv(ctx, a); err != nil {
				return "", err
			}
		}
		// exec is the shell's, before the credential prefix: `env -u … exec
		// claude-agent-acp` asks env for a program called exec, which is exit
		// 127 before the adapter is ever reached. The Incus path above has the
		// same order.
		return fmt.Sprintf("cd %s && exec %s%s", shellQuote(m.worktree(a)), m.withoutCredentials(a, hide), command), nil

	}
	overrides, err := m.Store.ToolOverrides(ctx)
	if err != nil {
		return "", err
	}
	// The adapter at the version agents run now: the pin, or the newer one
	// "Keep Claude Code up to date" moved the base image to, which an agent
	// made before that gets here, before its next turn (chat.ToolUpdated).
	// OpenCode 2 is not a mise tool: ensureOpenCode2InAgent installs it.
	var spec, pkg string
	if adapter.Package != "" {
		spec = image.Overrides(overrides).Apply([]image.Tool{{Spec: adapter.Package}})[0].Spec
		if err := m.ensureMise(ctx, a, spec, "mise install", "the "+adapter.Tool+" adapter", status); err != nil {
			return "", err
		}
		if _, moved := overrides[a.AI]; moved {
			// And the tool's own command line, which the base image has globally.
			if err := m.ensureMise(ctx, a, image.Overrides(overrides).Spec(a.AI), "mise use -g", adapter.Tool, status); err != nil {
				return "", err
			}
		}
		pkg = shellQuote(spec)
	}
	user := m.machineUser(a)
	status("Starting " + adapter.Tool)
	// mise exec runs the version AgentBox pins, whatever the agent's own mise configuration says.
	command := shellQuote(adapter.Command)
	args := adapter.Args
	var env string
	if a.AI == "claude" {
		env = claudeEntrypoint + " "
	}
	if adapter.Script != nil {
		// Written every time, so the adapter is always this daemon's own.
		path := m.home(a) + "/" + adapter.ScriptPath
		if err := m.machine(a).WriteFile(ctx, path, adapter.Script, user.UID, user.GID, 0o644); err != nil {
			return "", fmt.Errorf("writing the %s adapter into %s: %w", adapter.Tool, a.Ref(), err)
		}
		args = append([]string{path}, args...)
		env = fmt.Sprintf("%s=\"$(mise where %s)\" ", adapter.SDKEnv, pkg)
	}
	for _, arg := range args {
		command += " " + shellQuote(arg)
	}
	cmdline := command
	if adapter.Package != "" {
		cmdline = "mise exec " + pkg + " -- " + command
	}
	hide, err := m.toolHides(a)
	if err != nil {
		return "", err
	}
	// bash_env as this daemon writes it, giving the tool's shells the
	// secrets back: an agent started by an older one has a copy without.
	if len(hide) > 0 {
		if err := m.writeBashEnv(ctx, a); err != nil {
			return "", err
		}
	}
	script := fmt.Sprintf("cd %s && %sexec %s%s", shellQuote(m.worktree(a)), env, m.withoutCredentials(a, hide), cmdline)
	return script, nil
}

// ChatProcess starts an agent's ACP adapter as the chat's process. On this
// host its stdio is an `incus exec`; a runtime whose machine is elsewhere
// answers with a stream over its own connection, in the same shape.
func (m *Manager) ChatProcess(ctx context.Context, a state.Agent, status func(detail string)) (*chat.Process, error) {
	if a.OnCluster() {
		script, err := m.chatScript(ctx, a, status)
		if err != nil {
			return nil, err
		}
		return m.streamChatProcess(ctx, a, script), nil
	}
	cmd, err := m.ChatCommand(ctx, a, status)
	if err != nil {
		return nil, err
	}
	return chat.StartCommand(cmd)
}

// streamChatProcess runs script as the machine's user over UserExec and gives
// the chat a process of the shape an exec produces. Two pipes are enough: ACP
// speaks JSON lines over stdin and stdout, so nothing here wants a terminal,
// and a terminal's echo and line discipline would corrupt the stream.
func (m *Manager) streamChatProcess(ctx context.Context, a state.Agent, script string) *chat.Process {
	stdin, stdinW := io.Pipe()
	stdout, stdoutW := io.Pipe()
	stderr := &syncBuffer{}
	execCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var waitErr error
	go func() {
		defer close(done)
		waitErr = m.machine(a).UserExec(execCtx, m.machineUser(a).Name, script, stdin, stdoutW, stderr)
		// End both ends with the exec: the chat's stdout reader gets EOF (or
		// the exec's error) and its stdin writer a closed pipe, rather than
		// waiting forever on a process that is gone.
		_ = stdoutW.CloseWithError(waitErr)
		_ = stdin.Close()
	}()
	var once sync.Once
	return &chat.Process{
		Stdin:  stdinW,
		Stdout: stdout,
		Stop: func() {
			once.Do(func() {
				cancel()
				_ = stdinW.Close()
			})
		},
		Wait: func() error {
			<-done
			return waitErr
		},
		Stderr: stderr.String,
	}
}

// syncBuffer holds a process's stderr for a reader that may ask while the
// command is still writing it, which bytes.Buffer alone does not allow.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// ensureMise installs spec in the agent with install ("mise install", or
// "mise use -g" for a command it runs by name) when its machine lacks it,
// saying so with what.
func (m *Manager) ensureMise(ctx context.Context, a state.Agent, spec, install, what string, status func(detail string)) error {
	if m.machine(a).UserExec(ctx, m.machineUser(a).Name, "mise where "+shellQuote(spec), nil, io.Discard, io.Discard) == nil {
		return nil
	}
	status("Installing " + what + " in this agent")
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	var out bytes.Buffer
	if err := m.machine(a).UserExec(ctx, m.machineUser(a).Name, "MISE_YES=1 "+install+" "+shellQuote(spec), nil, &out, &out); err != nil {
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		return fmt.Errorf("installing %s in %s: %w: %s", spec, a.Ref(), err, strings.Join(lines[max(len(lines)-3, 0):], " "))
	}
	return nil
}

// interfaceFor checks the interface asked for a new agent: the chat unless it
// says otherwise, and the command line for an agent without an AI tool.
func interfaceFor(ai, iface string) (string, error) {
	switch {
	case ai == "cursor" && iface == state.InterfaceCLI:
		return "", errors.New("a Cursor agent runs only in the chat: AgentBox drives Cursor through its SDK, which has no command line")
	case iface != "" && iface != state.InterfaceChat && iface != state.InterfaceCLI:
		return "", fmt.Errorf("unknown interface %q: use chat or cli", iface)
	case ai == "none" && iface == state.InterfaceChat:
		return "", errors.New("an agent without an AI tool has nothing to chat with: use claude, codex, opencode or cursor")
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
	if _, err := interfaceFor(a.AI, iface); err != nil {
		return a, err
	}
	if err := m.Store.SetAgentInterface(ctx, a.Project, a.Name, iface); err != nil {
		return a, err
	}
	a.Interface = iface
	if iface != state.InterfaceCLI {
		return a, nil
	}
	if inst, err := m.machine(a).Info(ctx); err != nil || inst.State != "Running" {
		return a, nil // the session starts with the agent
	}
	m.prepareToolWindow(ctx, a)
	window, err := m.toolWindowScript(ctx, a)
	if err != nil {
		return a, err
	}
	script := fmt.Sprintf("tmux has-session -t %[1]s 2>/dev/null || exit 0\ntmux list-windows -t %[1]s -F '#W' | grep -qx %[2]s && exit 0\n",
		session, shellQuote(a.AI)) + window
	var out bytes.Buffer
	if err := m.machine(a).UserExec(ctx, m.machineUser(a).Name, script, nil, &out, &out); err != nil {
		return a, fmt.Errorf("starting %s in the terminal: %w: %s", adapter.Tool, err, strings.TrimSpace(out.String()))
	}
	return a, nil
}

// prepareToolWindow gives the terminal's OpenCode what a chat's gets from
// PrepareChatModel before every chat: the config in the shape of the version
// running now, and OpenCode 2's logins (installing it first with
// ensureOpenCode2InAgent if the agent doesn't have it), so an agent switched in
// either direction starts on what its version reads. It is for a tool window
// about to be opened, and a failure is logged, not returned: the tool reports
// what it is missing, and a machine without the network still gets its shell.
func (m *Manager) prepareToolWindow(ctx context.Context, a state.Agent) {
	if a.AI != "opencode" || a.Interface == state.InterfaceChat {
		return
	}
	if err := m.prepareAgentOpenCodeSettings(ctx, a); err != nil {
		m.logf("preparing %s's OpenCode: %v", a.Ref(), err)
	}
}

// toolWindowScript opens tmux window 1 with the AI tool's command line, for an
// agent you use from the terminal rather than the chat, started without the
// credential variables in hide (toolHides).
func (m *Manager) toolWindowScript(ctx context.Context, a state.Agent) (string, error) {
	tool := Tools[a.AI]
	if a.AI == "opencode" {
		version, err := m.openCodeVersion(ctx)
		if err != nil {
			return "", err
		}
		tool = Tool{Command: version.Command(), Autonomous: version.Autonomous()}
	}
	command := tool.Command
	if a.Autonomous {
		command = tool.Autonomous
	}
	if command == "" || a.Interface == state.InterfaceChat {
		return "", nil
	}
	hide, err := m.toolHides(a)
	if err != nil {
		return "", err
	}
	// Typed into a shell, so the window stays open when the tool exits. The
	// shell keeps the secrets; env execs the tool without them.
	return fmt.Sprintf("tmux new-window -t %[1]s -n %[2]s -c %[3]s\ntmux send-keys -t %[1]s:%[2]s %[4]s Enter\n",
		session, a.AI, shellQuote(m.worktree(a)), shellQuote(m.withoutCredentials(a, hide)+command)), nil
}

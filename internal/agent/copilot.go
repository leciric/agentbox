package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/credentials"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// Where a GitHub Copilot CLI agent's files go, under its user's home. Copilot
// is its own ACP adapter (`copilot --acp`) and reads everything from
// ~/.copilot (COPILOT_HOME).
const (
	// copilotMCPFile is its user MCP configuration, where AgentBox's servers
	// and the connectors go.
	copilotMCPFile = ".copilot/mcp-config.json"
	// copilotSettingsFile is its user settings.json: no self-update, the
	// worktree trusted for its terminal interface (copilotSettings).
	copilotSettingsFile = ".copilot/settings.json"
	// copilotBriefFile is its personal instructions file, where the brief
	// goes. It reads the worktree's AGENTS.md by itself.
	copilotBriefFile = ".copilot/copilot-instructions.md"
	// copilotTokenFile holds the GitHub token of the agent's Copilot account,
	// readable by the agent's user only. Copilot's ACP offers no sign-in but
	// its terminal `copilot login`, which keeps the token in the OS keychain,
	// so the chat and the terminal both start Copilot with
	// COPILOT_GITHUB_TOKEN read from this file (copilotTokenEnv), after
	// withoutCredentials took any project secret of that name out: Copilot
	// reads it ahead of GH_TOKEN, which the agent keeps for gh and git.
	copilotTokenFile = ".copilot/agentbox-token"
	// copilotMCPTimeout is how long it waits on an MCP tool call, in
	// milliseconds: a day, since request_credential and agentbox ask wait on
	// a person (mcpServer.waits).
	copilotMCPTimeout = 24 * 60 * 60 * 1000
)

// copilotFlags are what every Copilot AgentBox starts runs with: the pinned
// version only (no download of a newer one, which it would then run instead),
// and no telemetry of its own.
var copilotFlags = []string{"--no-auto-update", "--disable-client-telemetry"}

// copilotNoUpdate keeps the terminal's Copilot on the pinned version too, and
// any Copilot a command starts.
const copilotNoUpdate = "COPILOT_AUTO_UPDATE=false"

// copilotMCPConfig is ~/.copilot/mcp-config.json: every MCP server AgentBox
// gives every tool, with all their tools, and a day to answer for the ones
// that wait on a person. env, by server name, is a lead's: its servers reach
// its socket through it.
func copilotMCPConfig(servers []mcpServer, env map[string]map[string]string) []byte {
	mcp := map[string]any{}
	for _, s := range servers {
		args := s.args
		if args == nil {
			args = []string{}
		}
		server := map[string]any{"type": "local", "command": s.command, "args": args, "tools": []string{"*"}}
		if s.waits {
			server["timeout"] = copilotMCPTimeout
		}
		if vars := env[s.name]; len(vars) > 0 {
			server["env"] = vars
		}
		mcp[s.name] = server
	}
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": mcp}, "", "  ")
	return b
}

// copilotSettings is ~/.copilot/settings.json: no self-update or update
// notice, no startup banner, and the worktree trusted, so its terminal
// interface doesn't stop to ask (the machine is the sandbox). The model isn't
// here: the chat sets it on the session.
func copilotSettings(worktree string) []byte {
	settings := map[string]any{"autoUpdate": false, "banner": "never"}
	if worktree != "" {
		settings["trustedFolders"] = []string{worktree}
	}
	b, _ := json.MarshalIndent(settings, "", "  ")
	return b
}

// copilotLoginFiles are the files, relative to the home, that sign Copilot in
// with a stored account: its token, which copilotTokenEnv reads.
func copilotLoginFiles(login credentials.CopilotLogin) map[string][]byte {
	return map[string][]byte{copilotTokenFile: []byte(login.Token)}
}

// copilotTokenEnv is the assignment that gives an agent's Copilot its
// account's token, to follow withoutCredentials(hide) in the command that
// starts it: "" when the agent runs another tool or has no Copilot account
// (hide is empty then), and Copilot falls back on the agent's GH_TOKEN.
func (m *Manager) copilotTokenEnv(a state.Agent, hide []string) string {
	if a.AI != "copilot" || len(hide) == 0 {
		return ""
	}
	return `COPILOT_GITHUB_TOKEN="$(cat ` + shellQuote("/home/"+m.User.Name+"/"+copilotTokenFile) + `)" `
}

// copilotPlatform is the suffix of Copilot's npm platform package for this
// machine (linux-x64, darwin-arm64), whose binary is Copilot itself.
func copilotPlatform(goos, goarch string) string {
	arch := "x64"
	if goarch == "arm64" {
		arch = "arm64"
	}
	if goos == "windows" {
		goos = "win32"
	}
	return goos + "-" + arch
}

// copilotNativeBin is the path, under node_modules, of the Copilot binary for
// this machine. `copilot` itself is a Node loader that runs it with
// spawnSync and waits: stopping the loader leaves the binary, the process
// running the session, behind under init until its stdin closes. So AgentBox
// runs the binary directly, in agents (copilotNativeScript) and on the host.
func copilotNativeBin() string {
	return "@github/copilot-" + copilotPlatform(runtime.GOOS, runtime.GOARCH) + "/copilot"
}

// copilotNativeScript is a shell command substitution that finds the Copilot
// binary of the pinned package (spec, mise's name@version) in an agent: mise's
// npm backend keeps the platform package under its own store, so it is
// looked for rather than named. Agents are Linux.
func copilotNativeScript(spec string) string {
	return `"$(find "$(mise where ` + shellQuote(spec) + `)" -path "*/@github/copilot-linux-$(case "$(uname -m)" in aarch64|arm64) echo arm64;; *) echo x64;; esac)/copilot" -type f 2>/dev/null | head -n1)"`
}

// copilotPin is the Copilot package the base image pins, as mise names it.
var copilotPin = image.Pin("npm:@github/copilot")

// prepareAgentCopilotSettings rewrites a Copilot agent's MCP configuration
// and settings whole, as configure does at creation, and copies its token in
// again, so an account added after the agent was created reaches it the next
// time its chat starts.
func (m *Manager) prepareAgentCopilotSettings(ctx context.Context, a state.Agent) error {
	home := "/home/" + m.User.Name
	connectors, err := m.connectorNames(ctx, a)
	if err != nil {
		return err
	}
	for rel, b := range map[string][]byte{
		copilotMCPFile:      copilotMCPConfig(agentMCPServers(home, connectors), nil),
		copilotSettingsFile: copilotSettings(a.Worktree),
	} {
		if err := m.Incus.WriteFile(ctx, a.Instance, home+"/"+rel, b, m.User.UID, m.User.GID, 0o600); err != nil {
			return err
		}
	}
	if a.AI != "copilot" {
		return nil
	}
	login, ok := m.Creds.CopilotLogin(a.CopilotAccount)
	if !ok {
		return nil
	}
	for rel, b := range copilotLoginFiles(login) {
		if err := m.Incus.WriteFile(ctx, a.Instance, home+"/"+rel, b, m.User.UID, m.User.GID, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// leadCopilotMCP is a Copilot lead's ~/.copilot/mcp-config.json, as an
// agent's but with each server's environment.
func leadCopilotMCP(servers map[string]any) []byte {
	var list []mcpServer
	env := map[string]map[string]string{}
	for _, s := range stdioServers(servers) {
		list = append(list, mcpServer{name: s.name, command: s.command, args: s.args, waits: true})
		env[s.name] = s.env
	}
	return copilotMCPConfig(list, env)
}

// copilotLeadToken is the token a Copilot lead runs on, "" for none: its
// composer's or project's account (LeadLogins).
func (m *Manager) copilotLeadToken(ctx context.Context, a state.Agent) (string, error) {
	logins, err := m.LeadLogins(ctx, a)
	if err != nil {
		return "", err
	}
	a.CopilotAccount = logins.Copilot
	auth, err := m.toolLoginFile(state.Project{}, a, "copilot")
	if err != nil || auth == nil {
		return "", err
	}
	login, ok := credentials.ParseCopilotLogin(auth)
	if !ok {
		return "", nil
	}
	return login.Token, nil
}

// copilotModelsTimeout bounds asking Copilot for its model menu, the install
// on first use included.
const copilotModelsTimeout = 3 * time.Minute

// CopilotModels asks GitHub Copilot CLI which models a stored account (the
// default for "") can run: it installs Copilot on this machine the first time,
// as a lead's is, starts it in ACP mode in a throwaway home with only the
// account's token, and reads the model menu of a new session. Nothing is sent
// to a model.
func (m *Manager) CopilotModels(ctx context.Context, account string) ([]acp.ConfigChoice, error) {
	login, ok := m.Creds.CopilotLogin(account)
	if !ok {
		return nil, errors.New("agents have no GitHub Copilot CLI login")
	}
	ctx, cancel := context.WithTimeout(ctx, copilotModelsTimeout)
	defer cancel()
	path, nodeBin, err := m.ensureNPMTool(ctx, "copilot", leadNPMTools["copilot"], func(string) {})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.Creds.Dir, 0o700); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp(m.Creds.Dir, ".copilot-models-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(home) }()
	if err := writePrivate(home, copilotSettingsFile, copilotSettings("")); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, ChatAdapters["copilot"].Args...)
	cmd.Dir = home
	cmd.Env = copilotModelsEnv(home, nodeBin, login.Token)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	conn := acp.NewConn(stdout, stdin, quietClient{})
	var init acp.InitializeResponse
	if err := conn.Call(ctx, acp.MethodInitialize, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersion}, &init); err != nil {
		return nil, fmt.Errorf("%w: %s", err, tail(stderr.String()))
	}
	var session acp.SessionResponse
	if err := conn.Call(ctx, acp.MethodSessionNew, acp.NewSessionRequest{Cwd: home, McpServers: []acp.McpServer{}}, &session); err != nil {
		return nil, fmt.Errorf("%w: %s", err, tail(stderr.String()))
	}
	for _, o := range session.ConfigOptions {
		if o.Category == "model" {
			return o.Choices, nil
		}
	}
	return nil, nil
}

// copilotModelsEnv is the environment CopilotModels starts Copilot in: the
// throwaway home, the account's token, and nothing else of this machine's but
// its PATH, so no GH_TOKEN of the daemon's stands in for the account.
func copilotModelsEnv(home, nodeBin, token string) []string {
	return []string{
		"HOME=" + home, "COPILOT_HOME=" + home + "/.copilot",
		"PATH=" + nodeBin + string(os.PathListSeparator) + os.Getenv("PATH"), "LANG=C.UTF-8",
		copilotNoUpdate, "COPILOT_GITHUB_TOKEN=" + token,
	}
}

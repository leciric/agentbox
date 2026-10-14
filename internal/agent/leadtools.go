package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"agentbox/internal/cursor"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// A lead's AI tool and account are chosen in its chat's composer, the way a
// model is: Claude Code by default, or Codex, OpenCode or Cursor, each on the
// login AgentBox has for agents. The choice is the chat's own (state.Chat's
// Tool, NextTool and Account), kept beside its conversation, so it reaches the
// Home chat too, which has no agents row; the project's settings stay what new
// agents and a reset chat start from.
//
// The chat decides when a chosen tool takes over (chat.Manager.SetRunner): a
// tool's session can't be resumed by another, so the switch rolls the session
// at the next message. What this file does is run whichever tool is in
// effect: install it on the host the first time, as Claude Code is
// (hosttools.go), give it AgentBox's login and the lead's brief and MCP
// servers in the lead's private HOME, and start its ACP adapter.

// LeadTools are the AI tools a lead can run, in the order the composer offers
// them.
var LeadTools = []string{"claude", "codex", "opencode", "cursor"}

// leadNPMTools are the npm packages a Codex or OpenCode lead runs, installed in
// AgentBox's tools directory, and the command each puts in node_modules/.bin.
// The versions are the base image's (tools.txt), so a lead and an agent on the
// same tool run the same one. OpenCode is mise's native package in an agent;
// on the host it is npm's opencode-ai, whose postinstall fetches the binary,
// which plain npm runs.
var leadNPMTools = map[string]struct{ pkg, bin string }{
	"codex":    {strings.TrimPrefix(image.Pin("npm:@agentclientprotocol/codex-acp"), "npm:"), "codex-acp"},
	"opencode": {"opencode-ai@" + pinnedVersion(image.Pin("opencode")), "opencode"},
}

// LeadChoice is what a lead's composer chose: the tool in effect, the one
// chosen for the next message ("" for none), and the Claude Code account (""
// for the project's).
type LeadChoice struct {
	Tool, NextTool, Account string
}

// LeadChoice reads a lead's choice from its chat. A chat never switched runs
// Claude Code.
func (m *Manager) LeadChoice(ctx context.Context, project string) (LeadChoice, error) {
	c, err := m.Store.Chat(ctx, project, state.LeadName)
	if err != nil {
		return LeadChoice{Tool: "claude"}, err
	}
	return LeadChoice{Tool: cmp.Or(c.Tool, "claude"), NextTool: c.NextTool, Account: c.Account}, nil
}

// LeadToolLogin says why a lead can't run tool: nil when AgentBox has a login
// for it, and the same refusal an agent asking for it gets otherwise.
func (m *Manager) LeadToolLogin(p state.Project, tool string) error {
	if !slices.Contains(LeadTools, tool) {
		return fmt.Errorf("a project's chat can't run %q: use claude, codex, opencode or cursor", tool)
	}
	_, err := m.CheckLogin(tool, p, "")
	return err
}

// LeadToolInstalled reports whether the host already has what a lead on tool
// runs, so the composer can say that the first message installs it.
func (m *Manager) LeadToolInstalled(tool string) bool {
	switch tool {
	case "claude":
		// Where hostTools looks: AgentBox's own copies, else the PATH's.
		return (usable(m.adapterPath()) || onPath(ChatAdapters["claude"].Command) != "") && (usable(m.claudePath()) || onPath(ChatTool) != "")
	case "cursor":
		return m.cursorSDKVersion() == cursor.SDKVersion()
	}
	t, ok := leadNPMTools[tool]
	return ok && usable(m.npmBin(t.bin)) && m.npmVersion(t.pkg) == pinnedVersion(t.pkg)
}

// leadAccount is the Claude Code account a lead on tool runs on: the one its
// chat chose, while the project may still use it, else the project's. Other
// tools have one login each and no account to name.
func (m *Manager) leadAccount(p state.Project, choice LeadChoice) (string, error) {
	if choice.Tool != "claude" {
		return "", m.LeadToolLogin(p, choice.Tool)
	}
	if choice.Account != "" {
		account, err := m.ClaudeAccountFor(p, choice.Account)
		if err == nil {
			return account, nil
		}
		// Removed, or taken off the project's list since: the project's own
		// account rather than no chat. The composer shows which it runs on.
		m.logf("The %s chat's chosen Claude Code account: %v; using the project's", p.Name, err)
	}
	return m.ClaudeAccountFor(p, "")
}

// LeadAccountFor is the Claude Code account a lead runs on with the tool a
// names (leadAccount), "" for another tool.
func (m *Manager) LeadAccountFor(ctx context.Context, a state.Agent) (string, error) {
	p := homeProject
	if !a.IsHome() {
		var err error
		if p, err = m.Store.Project(ctx, a.Project); err != nil {
			return "", err
		}
	}
	choice, err := m.LeadChoice(ctx, a.Project)
	if err != nil {
		return "", err
	}
	choice.Tool = a.AI
	return m.leadAccount(p, choice)
}

// npmBin is a command an npm package installed in AgentBox's tools directory.
func (m *Manager) npmBin(bin string) string {
	return filepath.Join(m.toolsHome(), "node_modules", ".bin", bin)
}

// npmVersion is the version of an npm package (name@version) installed in
// AgentBox's tools directory, or "" when there is none.
func (m *Manager) npmVersion(pkg string) string {
	name := pkg[:strings.LastIndex(pkg, "@")]
	raw, err := os.ReadFile(filepath.Join(m.toolsHome(), "node_modules", name, "package.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return ""
	}
	return manifest.Version
}

// ensureNPMTool installs a lead's Codex or OpenCode in AgentBox's tools
// directory the first time, and again when the pin moves, and returns its
// command and the directory node is in.
func (m *Manager) ensureNPMTool(ctx context.Context, tool string, status func(string)) (path, nodeBin string, err error) {
	t := leadNPMTools[tool]
	nodeBin, err = m.ensureNode(ctx, status)
	if err != nil {
		return "", "", err
	}
	own := m.npmBin(t.bin)
	installed := m.npmVersion(t.pkg)
	if usable(own) && installed == pinnedVersion(t.pkg) {
		return own, nodeBin, nil
	}
	name := ChatAdapters[tool].Tool
	if installed == "" {
		status("Installing " + name + " for the project chat, once for this machine")
	} else {
		status(fmt.Sprintf("Updating %s from %s to %s", name, installed, pinnedVersion(t.pkg)))
	}
	if err := os.MkdirAll(m.toolsHome(), 0o700); err != nil {
		return "", "", err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(nodeBin, "npm"), "install", "--silent", "--no-fund", "--no-audit", "--prefix", m.toolsHome(), t.pkg)
	cmd.Env = append(m.installEnv(), "PATH="+nodeBin+string(os.PathListSeparator)+os.Getenv("PATH")+string(os.PathListSeparator)+systemPath)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", "", installError(t.pkg, err, out.String(), ctx)
	}
	if !usable(own) {
		return "", "", fmt.Errorf("installing %s: it reported success but %s isn't there: %s", t.pkg, own, tail(out.String()))
	}
	return own, nodeBin, nil
}

// leadAdapter is the command, arguments and environment that start a lead's
// ACP adapter for its tool, and the directories its PATH needs. Claude Code's
// is hostTools, unchanged; the others are installed the same way, on first use.
func (m *Manager) leadAdapter(ctx context.Context, a state.Agent, home string, status func(string)) (command string, args, env, bin []string, err error) {
	switch a.AI {
	case "claude":
		tools, err := m.hostTools(ctx, status)
		if err != nil {
			return "", nil, nil, nil, err
		}
		token, err := m.Creds.ClaudeToken(a.ClaudeAccount)
		if err != nil {
			return "", nil, nil, nil, err
		}
		if token != "" {
			env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+token)
		}
		return tools.Adapter, nil, env, []string{tools.Bin}, nil
	case "cursor":
		helper, err := m.CursorHelper(ctx, status)
		if err != nil {
			return "", nil, nil, nil, err
		}
		// The script reads the sign-in and the brief from HOME, which
		// writeLeadTool filled.
		return helper.Node, []string{helper.Script}, []string{cursor.SDKEnv + "=" + helper.SDK}, []string{filepath.Dir(helper.Node)}, nil
	case "codex", "opencode":
		ictx, cancel := context.WithTimeout(ctx, hostInstallTimeout)
		defer cancel()
		path, nodeBin, err := m.ensureNPMTool(ictx, a.AI, status)
		if err != nil {
			return "", nil, nil, nil, err
		}
		bin = []string{filepath.Dir(path), nodeBin}
		if gh, err := m.ensureGH(ictx, status); err == nil {
			bin = append(bin, filepath.Dir(gh))
		}
		if a.AI == "codex" {
			return path, nil, []string{"CODEX_HOME=" + filepath.Join(home, ".codex")}, bin, nil
		}
		return path, ChatAdapters["opencode"].Args, nil, bin, nil
	}
	return "", nil, nil, nil, fmt.Errorf("a project's chat can't run %q", a.AI)
}

// leadToolFiles are what a lead's HOME holds for the tools other than Claude
// Code (whose files writeLeadHome writes): each one's brief where it reads
// one, its MCP servers in its own format, and a copy of AgentBox's login for
// it, as an agent's machine gets them (configure). They are written whichever
// tool runs, so a switch finds them in place; a login AgentBox doesn't have
// is left out, and an old copy of it removed.
func (m *Manager) leadToolFiles(home, worktree, text string, servers map[string]any) (files map[string][]byte, remove []string, err error) {
	files = map[string][]byte{
		".codex/AGENTS.md":               []byte(text),
		".config/opencode/AGENTS.md":     []byte(text),
		".codex/config.toml":             []byte(leadCodexConfig(worktree, servers)),
		".config/opencode/opencode.json": leadOpenCodeConfig(servers),
		".cursor/mcp.json":               leadCursorMCP(servers),
	}
	for rel, src := range map[string]string{
		".codex/auth.json":                m.Creds.CodexAuthPath(),
		".local/share/opencode/auth.json": m.Creds.OpenCodeAuthPath(),
		cursorAuthFile:                    m.Creds.CursorAuthPath(),
	} {
		auth, err := os.ReadFile(src)
		switch {
		case err == nil:
			files[rel] = auth
		case errors.Is(err, os.ErrNotExist):
			remove = append(remove, filepath.Join(home, rel))
		default:
			return nil, nil, err
		}
	}
	return files, remove, nil
}

// stdioServers reads leadMCPServers' map, in Claude Code's format, back as
// name, command, arguments and environment, sorted by name.
func stdioServers(servers map[string]any) []leadServer {
	out := make([]leadServer, 0, len(servers))
	for name, raw := range servers {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		command, _ := s["command"].(string)
		if command == "" {
			continue
		}
		args, _ := s["args"].([]string)
		env, _ := s["env"].(map[string]string)
		out = append(out, leadServer{name, command, args, env})
	}
	slices.SortFunc(out, func(a, b leadServer) int { return strings.Compare(a.name, b.name) })
	return out
}

type leadServer struct {
	name, command string
	args          []string
	env           map[string]string
}

// leadCodexConfig is a Codex lead's ~/.codex/config.toml: its worktree
// trusted, and its MCP servers with their environment.
func leadCodexConfig(worktree string, servers map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[projects.%q]\ntrust_level = \"trusted\"\n", worktree)
	for _, s := range stdioServers(servers) {
		quoted := make([]string, len(s.args))
		for i, arg := range s.args {
			quoted[i] = fmt.Sprintf("%q", arg)
		}
		fmt.Fprintf(&b, "\n[mcp_servers.%s]\ncommand = %q\nargs = [%s]\n", s.name, s.command, strings.Join(quoted, ", "))
		// create_agent and the questions wait on the user, which Codex's
		// default of a minute would cut short.
		fmt.Fprintf(&b, "tool_timeout_sec = %d\n", int(waitingToolTimeout.Seconds()))
		if len(s.env) > 0 {
			keys := slices.Sorted(maps.Keys(s.env))
			pairs := make([]string, len(keys))
			for i, k := range keys {
				pairs[i] = fmt.Sprintf("%s = %q", k, s.env[k])
			}
			fmt.Fprintf(&b, "env = { %s }\n", strings.Join(pairs, ", "))
		}
	}
	return b.String()
}

// leadOpenCodeConfig is an OpenCode lead's opencode.json: its MCP servers as
// "local" ones. It asks before it acts, like a Claude Code lead (leadSettings).
func leadOpenCodeConfig(servers map[string]any) []byte {
	mcp := map[string]any{}
	for _, s := range stdioServers(servers) {
		server := map[string]any{"type": "local", "command": append([]string{s.command}, s.args...), "enabled": true}
		if len(s.env) > 0 {
			server["environment"] = s.env
		}
		mcp[s.name] = server
	}
	b, _ := json.Marshal(map[string]any{"$schema": "https://opencode.ai/config.json", "mcp": mcp, "subagent_depth": opencodeSubagentDepth})
	return b
}

// leadCursorMCP is a Cursor lead's ~/.cursor/mcp.json, which AgentBox's
// adapter reads (adapter.mjs, mcpServers).
func leadCursorMCP(servers map[string]any) []byte {
	out := map[string]any{}
	for _, s := range stdioServers(servers) {
		server := map[string]any{"type": "stdio", "command": s.command, "args": s.args}
		if len(s.env) > 0 {
			server["env"] = s.env
		}
		out[s.name] = server
	}
	b, _ := json.Marshal(map[string]any{"mcpServers": out})
	return b
}

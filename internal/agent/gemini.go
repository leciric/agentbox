package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentbox/internal/acp"
	"agentbox/internal/credentials"
	"agentbox/internal/opencode"
	"agentbox/internal/state"
)

// Where a Gemini CLI agent's files go, under its user's home. Gemini CLI is
// its own ACP adapter (`gemini --acp`) and reads everything from ~/.gemini.
const (
	// geminiSettingsFile is its settings.json: the sign-in it uses, the MCP
	// servers and the rest of geminiSettings.
	geminiSettingsFile = ".gemini/settings.json"
	// geminiOAuthFile is where it keeps a Google sign-in, and refreshes it.
	geminiOAuthFile = ".gemini/oauth_creds.json"
	// geminiAccountsFile names the Google account signed in, which it shows.
	geminiAccountsFile = ".gemini/google_accounts.json"
	// geminiKeyFile holds an API key login. Gemini CLI reads its key only from
	// GEMINI_API_KEY, which a project secret of that name would replace and
	// which ~/.gemini/.env can't be trusted to give (a worktree's own .env is
	// read instead of it), so the tool is started with the variable read from
	// this file (geminiKeyEnv).
	geminiKeyFile = ".gemini/agentbox-api-key"
	// geminiBriefFile is its global context file, where the brief goes.
	geminiBriefFile = ".gemini/GEMINI.md"
	// geminiMCPTimeout is how long it waits on an MCP tool call, in
	// milliseconds: a day, since request_credential and agentbox ask wait
	// on a person (mcpServer.waits). Its default is ten minutes.
	geminiMCPTimeout = 24 * 60 * 60 * 1000
)

// geminiSettings is ~/.gemini/settings.json: every MCP server AgentBox gives
// every tool, trusted, so a call doesn't stop to ask before it runs, the
// sign-in the login holds (authType, "" for none yet), the worktree's
// AGENTS.md read as well as GEMINI.md, and nothing that would stop or change
// the pinned tool: no folder trust (the machine is the sandbox, and an
// untrusted folder refuses YOLO mode) and no self-update. The model isn't
// here: the chat sets it on the session, as it does Cursor's.
func geminiSettings(servers []mcpServer, authType string) []byte {
	mcp := map[string]any{}
	for _, s := range servers {
		server := map[string]any{"command": s.command, "args": s.args, "trust": true}
		if s.waits {
			server["timeout"] = geminiMCPTimeout
		}
		mcp[s.name] = server
	}
	security := map[string]any{"folderTrust": map[string]any{"enabled": false}}
	if authType != "" {
		security["auth"] = map[string]any{"selectedType": authType}
	}
	settings := map[string]any{
		"mcpServers": mcp,
		"security":   security,
		"context":    map[string]any{"fileName": []string{"GEMINI.md", "AGENTS.md"}},
		"general":    map[string]any{"enableAutoUpdate": false, "enableAutoUpdateNotification": false},
	}
	b, _ := json.MarshalIndent(settings, "", "  ")
	return b
}

// geminiLoginFiles are the files, relative to the home, that sign Gemini CLI
// in with a stored login: its oauth_creds.json and the account's address for
// a Google sign-in, the key file for an API key.
func geminiLoginFiles(login credentials.GeminiLogin) map[string][]byte {
	if login.OAuth != nil {
		files := map[string][]byte{geminiOAuthFile: login.OAuth}
		if login.Email != "" {
			accounts, _ := json.Marshal(map[string]any{"active": login.Email, "old": []string{}})
			files[geminiAccountsFile] = accounts
		}
		return files
	}
	return map[string][]byte{geminiKeyFile: []byte(login.APIKey)}
}

// geminiKeyEnv is the assignment that gives Gemini CLI its API key, to follow
// withoutCredentials(hide) in the command that starts it: "" unless the agent
// runs Gemini CLI on an API key login, whose variable hide then removes.
func (m *Manager) geminiKeyEnv(a state.Agent, hide []string) string {
	if a.AI != "gemini" || len(hide) == 0 {
		return ""
	}
	login, ok := m.Creds.GeminiLogin(a.GeminiAccount)
	if !ok || login.APIKey == "" {
		return ""
	}
	return `GEMINI_API_KEY="$(cat ` + shellQuote("/home/"+m.User.Name+"/"+geminiKeyFile) + `)" `
}

// geminiAuthType is the sign-in an agent's Gemini CLI uses: its own account's
// when it runs Gemini CLI, else its project's (the machine's default when
// that fails), so a command it runs finds a login as the other tools' are
// found; "" when there is none.
func (m *Manager) geminiAuthType(ctx context.Context, a state.Agent) string {
	account := a.GeminiAccount
	if a.AI != "gemini" && !a.IsHome() {
		if p, err := m.Store.Project(ctx, a.Project); err == nil {
			account = p.GeminiAccount
		}
	}
	login, ok := m.Creds.GeminiLogin(account)
	if !ok {
		if login, ok = m.Creds.GeminiLogin(""); !ok {
			return ""
		}
	}
	return login.AuthType()
}

// prepareAgentGeminiSettings rewrites a Gemini CLI agent's settings.json
// whole, as configure does at creation, and copies the sign-in in again, so
// one made after the agent was created reaches it the next time its chat
// starts.
func (m *Manager) prepareAgentGeminiSettings(ctx context.Context, a state.Agent) error {
	home := "/home/" + m.User.Name
	connectors, err := m.connectorNames(ctx, a)
	if err != nil {
		return err
	}
	settings := geminiSettings(agentMCPServers(home, connectors), m.geminiAuthType(ctx, a))
	if err := m.Incus.WriteFile(ctx, a.Instance, home+"/"+geminiSettingsFile, settings, m.User.UID, m.User.GID, 0o600); err != nil {
		return err
	}
	if a.AI != "gemini" {
		return nil
	}
	login, ok := m.Creds.GeminiLogin(a.GeminiAccount)
	if !ok {
		return nil
	}
	for rel, b := range geminiLoginFiles(login) {
		if err := m.Incus.WriteFile(ctx, a.Instance, home+"/"+rel, b, m.User.UID, m.User.GID, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// withGeminiServerEnv gives the MCP servers of a settings.json their
// environment, by server name: a lead's servers reach its socket through it.
func withGeminiServerEnv(settings []byte, env map[string]map[string]string) ([]byte, error) {
	var s map[string]any
	if err := json.Unmarshal(settings, &s); err != nil {
		return nil, err
	}
	servers, _ := s["mcpServers"].(map[string]any)
	for name, vars := range env {
		if server, ok := servers[name].(map[string]any); ok && len(vars) > 0 {
			server["env"] = vars
		}
	}
	return json.MarshalIndent(s, "", "  ")
}

// withGeminiAuth sets the sign-in a settings.json names, leaving the rest as
// it was: authType "" takes it out, for a lead that has no login.
func withGeminiAuth(settings []byte, authType string) ([]byte, error) {
	s := map[string]any{}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &s); err != nil {
			return nil, err
		}
	}
	security, _ := s["security"].(map[string]any)
	if security == nil {
		security = map[string]any{}
	}
	if authType == "" {
		delete(security, "auth")
	} else {
		security["auth"] = map[string]any{"selectedType": authType}
	}
	s["security"] = security
	return json.MarshalIndent(s, "", "  ")
}

// writeLeadGeminiLogin puts a Gemini CLI lead's login into its HOME: the
// files geminiLoginFiles names, and the sign-in in its settings.json, which
// leadToolFiles wrote with none. The key file of an earlier API key login
// goes, so leadGeminiEnv never hands over a key the lead no longer has.
func writeLeadGeminiLogin(home string, auth []byte) error {
	login, ok := credentials.ParseGeminiLogin(auth)
	authType := ""
	if ok {
		authType = login.AuthType()
		for rel, b := range geminiLoginFiles(login) {
			if err := writePrivate(home, rel, b); err != nil {
				return err
			}
		}
	}
	if !ok || login.APIKey == "" {
		if err := os.Remove(filepath.Join(home, geminiKeyFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	path := filepath.Join(home, geminiSettingsFile)
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	settings, err := withGeminiAuth(existing, authType)
	if err != nil {
		return err
	}
	return writePrivate(home, geminiSettingsFile, settings)
}

// leadGeminiEnv is the API key a Gemini CLI lead starts with, from the key
// file writeLeadGeminiLogin wrote: none for a Google sign-in, which is a file
// Gemini CLI reads itself. The lead's environment is AgentBox's own, with no
// project secrets in it to hide the key from.
func (m *Manager) leadGeminiEnv(a state.Agent, home string) []string {
	key, err := os.ReadFile(filepath.Join(home, geminiKeyFile))
	if err != nil || strings.TrimSpace(string(key)) == "" {
		return nil
	}
	return []string{"GEMINI_API_KEY=" + strings.TrimSpace(string(key))}
}

// geminiModelsTimeout bounds asking Gemini CLI for its model menu, the
// install on first use included.
const geminiModelsTimeout = 3 * time.Minute

// GeminiModels asks Gemini CLI which models a stored login (the default for
// "") can run: it installs Gemini CLI on this machine the first time, as a
// lead's is, starts it in ACP mode in a throwaway home holding only the login,
// and reads the model menu of a new session. Nothing is sent to a model.
func (m *Manager) GeminiModels(ctx context.Context, account string) ([]acp.ConfigChoice, error) {
	login, ok := m.Creds.GeminiLogin(account)
	if !ok {
		return nil, errors.New("agents have no Gemini CLI login")
	}
	ctx, cancel := context.WithTimeout(ctx, geminiModelsTimeout)
	defer cancel()
	path, nodeBin, err := m.ensureNPMTool(ctx, "gemini", opencode.V1, func(string) {})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.Creds.Dir, 0o700); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp(m.Creds.Dir, ".gemini-models-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(home) }()
	files := geminiLoginFiles(login)
	files[geminiSettingsFile] = geminiSettings(nil, login.AuthType())
	for rel, b := range files {
		if err := writePrivate(home, rel, b); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, path, ChatAdapters["gemini"].Args...)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + nodeBin + string(os.PathListSeparator) + os.Getenv("PATH"), "LANG=C.UTF-8"}
	if login.APIKey != "" {
		cmd.Env = append(cmd.Env, "GEMINI_API_KEY="+login.APIKey)
	}
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

// quietClient is the client end of a connection that only asks: it answers
// any request from the agent with a refusal and ignores what it says.
type quietClient struct{}

func (quietClient) Notify(string, json.RawMessage) {}

func (quietClient) Request(_ string, _ json.RawMessage, reply func(any, error)) {
	reply(nil, errors.New("not offered"))
}

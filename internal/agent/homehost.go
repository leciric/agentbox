package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"agentbox/internal/brief"
	"agentbox/internal/homehost"
	"agentbox/internal/hostos"
	"agentbox/internal/state"
)

// The Home chat on the user's computer (package homehost). The daemon keeps
// everything it keeps for the Home chat in the VM — its conversation, its
// private HOME, which the chat falls back to — and sends the host only what
// its tool reads at start: the brief, Claude Code's project settings, and
// AgentBox's login for a tool that has none of its own there.

// HostHomeSpec is what the Home chat's tool, a, is started with on the host
// info describes. EnsureHome and PrepareChatModel have run: the settings it
// sends are the ones they wrote into the chat's HOME, model and all.
func (m *Manager) HostHomeSpec(ctx context.Context, a state.Agent, info homehost.Info) (homehost.Spec, error) {
	projects, err := m.Store.Projects(ctx)
	if err != nil {
		return homehost.Spec{}, err
	}
	d := brief.HomeData{Dir: hostSessionDir(info, a.Session()), VM: hostos.InVM(), Host: hostName(info.OS), OnHost: true, HostHome: info.Home}
	if d.Session, d.OtherSessions, err = m.sessionTopics(ctx, a); err != nil {
		return homehost.Spec{}, err
	}
	for _, p := range projects {
		d.Projects = append(d.Projects, brief.HomeProject{Name: p.DisplayName, Root: p.Root})
	}
	text, err := brief.RenderHome(d)
	if err != nil {
		return homehost.Spec{}, err
	}
	spec := homehost.Spec{Tool: a.AI, Session: a.Session(), Files: map[string]string{"CLAUDE.md": text, "AGENTS.md": text}}
	switch a.AI {
	case "claude":
		spec.Package = ChatAdapters["claude"].Package
		// Project settings, which win over the user's own: ask before each
		// command and edit (leadSettings), on the model the composer chose.
		settings, err := os.ReadFile(filepath.Join(m.leadHome(a), claudeSettingsFile))
		if err != nil {
			if settings, err = json.Marshal(leadSettings()); err != nil {
				return homehost.Spec{}, err
			}
		}
		spec.Files[".claude/settings.json"] = string(settings)
		token, err := m.Creds.ClaudeToken(a.ClaudeAccount)
		if err != nil {
			return homehost.Spec{}, err
		}
		if token != "" {
			spec.LoginEnv = []string{"CLAUDE_CODE_OAUTH_TOKEN=" + token}
		}
	case "codex":
		spec.Package = ChatAdapters["codex"].Package
	case "opencode":
		// OpenCode merges this over the user's configuration.
		spec.Env = []string{`OPENCODE_CONFIG_CONTENT={"permission":{"edit":"ask","bash":"ask","webfetch":"ask"}}`}
	case "gemini":
		// It reads GEMINI.md and .gemini/settings.json from its working
		// directory, the second over the user's own settings (homehost
		// starts it with GEMINI_CLI_NO_RELAUNCH), and asks before each tool
		// call in its default mode. AgentBox's login can only go over as an
		// API key: a Google sign-in is a file in its home.
		spec.Files["GEMINI.md"] = text
		spec.Files[".gemini/settings.json"] = hostGeminiSettings()
		if login, ok := m.Creds.GeminiLogin(a.GeminiAccount); ok && login.APIKey != "" {
			spec.LoginEnv = []string{"GEMINI_API_KEY=" + login.APIKey}
		}
	default:
		return homehost.Spec{}, fmt.Errorf("%s runs only in AgentBox's VM", ChatAdapters[a.AI].Tool)
	}
	return spec, nil
}

// hostSessionDir is where homehost runs a session on the host info describes
// (homehost.Host.sessionDir), in its own separators.
func hostSessionDir(info homehost.Info, session string) string {
	if session == state.MigratedSession {
		return info.Dir
	}
	sep := "/"
	if info.OS == "windows" {
		sep = `\`
	}
	return info.Dir + sep + "sessions" + sep + session
}

// hostName is hostos.Name for a GOOS the host reported.
func hostName(goos string) string {
	switch goos {
	case "darwin":
		return "a Mac"
	case "windows":
		return "Windows"
	case "linux":
		return "a Linux host"
	}
	return goos
}

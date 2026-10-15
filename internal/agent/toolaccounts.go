package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"agentbox/internal/credentials"
	"agentbox/internal/state"
)

// Logins are the stored accounts an agent's AI tool signs in with: one per
// tool, OpenCode's one per provider. Only the agent's own tool's is set on a
// resolved one.
type Logins struct {
	Claude, Codex, Cursor string
	OpenCode              state.AccountMap
}

// The files each tool reads its login from, under the agent's (or the lead's)
// home.
const (
	codexAuthFile    = ".codex/auth.json"
	openCodeAuthFile = ".local/share/opencode/auth.json"
)

// CheckLogin reports that AgentBox can log the new agent in to its AI tool,
// and returns the account it will use: want's, else the project's, else the
// machine's default. An account named for a tool the agent doesn't run is
// refused, as is one that doesn't exist. Exported so a caller that starts a
// job for Create can run this check first, synchronously, rather than let the
// job fail after it has already answered that the agent is on its way (see
// createAgentFrom).
func (m *Manager) CheckLogin(ai string, p state.Project, want Logins) (Logins, error) {
	for tool, named := range map[string]bool{"claude": want.Claude != "", "codex": want.Codex != "", "cursor": want.Cursor != "", "opencode": len(want.OpenCode.Clean()) > 0} {
		if named && tool != ai {
			return Logins{}, fmt.Errorf("a %s account only applies to agents that run %s, and this one runs %s", ChatAdapters[tool].Tool, ChatAdapters[tool].Tool, ai)
		}
	}
	switch ai {
	case "claude":
		account, err := m.ClaudeAccountFor(p, want.Claude)
		return Logins{Claude: account}, err
	case "codex", "cursor":
		named := map[string]string{"codex": want.Codex, "cursor": want.Cursor}[ai]
		account, err := m.toolAccountFor(ai, cmpOr(named, projectToolAccount(p, ai)))
		if err != nil {
			return Logins{}, err
		}
		if ai == "codex" {
			return Logins{Codex: account}, nil
		}
		return Logins{Cursor: account}, nil
	case "opencode":
		used, err := m.openCodeAccountsFor(p, want.OpenCode)
		return Logins{OpenCode: used}, err
	}
	return Logins{}, nil
}

// projectToolAccount is the account a project names for a tool, "" for the
// machine's default.
func projectToolAccount(p state.Project, tool string) string {
	switch tool {
	case "codex":
		return p.CodexAccount
	case "cursor":
		return p.CursorAccount
	}
	return ""
}

// toolAccountFor resolves a Codex or Cursor account: the one named, which
// must exist (and for Cursor, hold a key that hasn't expired), else the
// machine's default.
func (m *Manager) toolAccountFor(tool, name string) (string, error) {
	b, resolved, err := m.Creds.ToolLogin(tool, name)
	if err != nil {
		return "", err
	}
	if b != nil {
		return resolved, nil
	}
	if name != "" {
		if ok, _ := m.Creds.HasToolAccount(tool, "", name); ok {
			return "", fmt.Errorf("the Cursor account %q has expired: sign in to it again under Settings → Accounts, or run agentbox auth cursor --account %s", name, name)
		}
		return "", fmt.Errorf("no %s account named %q: %s", credentials.ToolName(tool), name, m.toolAccountsHint(tool))
	}
	if tool == "codex" {
		return "", errors.New("agents have no Codex login: run agentbox auth codex (or use --ai none)")
	}
	return "", errors.New("agents aren't signed in to Cursor: run agentbox auth cursor, or sign in under Settings → Accounts (or use --ai claude)")
}

// openCodeAccountsFor resolves OpenCode's account for each provider: want's,
// else the project's, else the provider's default.
func (m *Manager) openCodeAccountsFor(p state.Project, want state.AccountMap) (state.AccountMap, error) {
	chosen := p.OpenCodeAccounts.Clean()
	maps.Copy(chosen, want.Clean())
	auth, used, err := m.Creds.OpenCodeAuth(chosen)
	if err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, errors.New("agents have no OpenCode login: run agentbox auth opencode (or use --ai claude)")
	}
	return used, nil
}

// toolAccountsHint says what to do about an unknown account name.
func (m *Manager) toolAccountsHint(tool string) string {
	accounts, err := m.Creds.ToolAccounts(tool)
	if err != nil || len(accounts) == 0 {
		return fmt.Sprintf("add it with agentbox auth %s --account <name>", tool)
	}
	names := make([]string, len(accounts))
	for i, a := range accounts {
		names[i] = a.Name
	}
	return "this machine has " + strings.Join(names, ", ")
}

// toolLoginFiles are the other tools' login files an agent's home gets,
// relative to it: its own tool's from the accounts it was created with, and
// the others' from its project's (the machine's defaults when that fails),
// so a command the agent runs finds them as the old single logins were found.
func (m *Manager) toolLoginFiles(ctx context.Context, a state.Agent) (map[string][]byte, error) {
	var p state.Project
	if !a.IsHome() {
		var err error
		if p, err = m.Store.Project(ctx, a.Project); err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
	}
	files := map[string][]byte{}
	for _, tool := range credentials.AccountTools {
		auth, err := m.toolLoginFile(p, a, tool)
		if err != nil {
			if tool == a.AI {
				return nil, err
			}
			auth = nil
		}
		if auth != nil {
			files[toolAuthFile(tool)] = auth
		}
	}
	return files, nil
}

func toolAuthFile(tool string) string {
	switch tool {
	case "codex":
		return codexAuthFile
	case "opencode":
		return openCodeAuthFile
	}
	return cursorAuthFile
}

// toolLoginFile is one tool's login file for an agent, nil when there is
// none: the agent's own account for its tool, else the project's.
func (m *Manager) toolLoginFile(p state.Project, a state.Agent, tool string) ([]byte, error) {
	own := tool == a.AI
	switch tool {
	case "opencode":
		chosen := p.OpenCodeAccounts.Clean()
		if own && len(a.OpenCodeAccounts) > 0 {
			chosen = a.OpenCodeAccounts.Clean()
		}
		auth, _, err := m.Creds.OpenCodeAuth(chosen)
		return auth, err
	default:
		name := projectToolAccount(p, tool)
		if own {
			name = cmpOr(agentToolAccount(a, tool), name)
		}
		auth, _, err := m.Creds.ToolLogin(tool, name)
		return auth, err
	}
}

// agentToolAccount is the Codex or Cursor account an agent holds.
func agentToolAccount(a state.Agent, tool string) string {
	switch tool {
	case "codex":
		return a.CodexAccount
	case "cursor":
		return a.CursorAccount
	}
	return ""
}

// LeadLogins are the accounts a lead on a tool other than Claude Code runs on:
// the one its composer chose for that tool while it still exists, else the
// project's. OpenCode's are the project's, since the composer doesn't choose
// them.
func (m *Manager) LeadLogins(ctx context.Context, a state.Agent) (Logins, error) {
	p := homeProject
	if !a.IsHome() {
		var err error
		if p, err = m.Store.Project(ctx, a.Project); err != nil {
			return Logins{}, err
		}
	}
	choice, err := m.LeadChoice(ctx, a.Project)
	if err != nil {
		return Logins{}, err
	}
	return m.leadLogins(p, a.AI, choice)
}

func (m *Manager) leadLogins(p state.Project, tool string, choice LeadChoice) (Logins, error) {
	switch tool {
	case "codex", "cursor":
		if chosen := choice.ToolAccounts[tool]; chosen != "" {
			want := Logins{Cursor: chosen}
			if tool == "codex" {
				want = Logins{Codex: chosen}
			}
			logins, err := m.CheckLogin(tool, p, want)
			if err == nil {
				return logins, nil
			}
			// Removed since: the project's own account rather than no chat.
			m.logf("The %s chat's chosen %s account: %v; using the project's", p.Name, ChatAdapters[tool].Tool, err)
		}
	}
	return m.CheckLogin(tool, p, Logins{})
}

// writeLeadLogin puts the login a lead's tool reads into its HOME, for the
// accounts it runs on, and records them on its row so the app can say which.
func (m *Manager) writeLeadLogin(ctx context.Context, a state.Agent, home string) error {
	if !slices.Contains(credentials.AccountTools, a.AI) {
		return nil
	}
	logins, err := m.LeadLogins(ctx, a)
	if err != nil {
		return err
	}
	a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount = logins.Codex, logins.OpenCode, logins.Cursor
	auth, err := m.toolLoginFile(state.Project{}, a, a.AI)
	if err != nil {
		return err
	}
	if err := writePrivate(home, toolAuthFile(a.AI), auth); err != nil {
		return err
	}
	if a.IsHome() || a.Status == "" {
		return nil
	}
	if err := m.Store.SetAgentToolAccounts(ctx, a.Project, a.Name, a.CodexAccount, a.OpenCodeAccounts, a.CursorAccount); err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	return nil
}

// writePrivate writes a file under dir readable only by its owner, making the
// directories it needs.
func writePrivate(dir, rel string, b []byte) error {
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

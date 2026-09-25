package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"agentbox/internal/brief"
	"agentbox/internal/gitrepo"
	"agentbox/internal/hostos"
	"agentbox/internal/notes"
	"agentbox/internal/state"
)

// The lead is a project's chat: the one agent that runs on the host instead of
// in a machine of its own, so a project you have never chatted with costs
// nothing. It directs the project's agents, and since D89 it also has the
// user's machine, the way the user's own Claude Code in a terminal does: a
// shell, and every tool, asking before it uses them (see leadSettings).

// LeadSocket returns the path of a project's lead socket, or "" when the
// manager has none (a preview, or a test).
func (m *Manager) LeadSocket(project string) string {
	if m.LeadSocketPath == nil {
		return ""
	}
	return m.LeadSocketPath(project)
}

// LeadRef is the agent ref of a project's lead.
func LeadRef(project string) string { return project + "/" + state.LeadName }

// Lead returns a project's lead, or state.ErrNotFound if it has never been used.
func (m *Manager) Lead(ctx context.Context, project string) (state.Agent, error) {
	a, err := m.Store.Agent(ctx, project, state.LeadName)
	if err != nil {
		return state.Agent{}, err
	}
	if !a.IsLead() {
		return state.Agent{}, fmt.Errorf("agent %s isn't a lead", a.Ref())
	}
	return a, nil
}

// EnsureLead returns a project's lead, creating it the first time the project
// is chatted with. Creating one makes no machine: a detached worktree on the
// project's branch, a private HOME, and a row.
func (m *Manager) EnsureLead(ctx context.Context, project string) (state.Agent, error) {
	switch a, err := m.Lead(ctx, project); {
	case err == nil:
		return m.repairLead(ctx, a)
	case !errors.Is(err, state.ErrNotFound):
		return state.Agent{}, err
	}

	p, repo, err := m.project(ctx, project)
	if err != nil {
		return state.Agent{}, err
	}
	if !repo.HasCommits() {
		return state.Agent{}, fmt.Errorf("%s has no commits yet: make one, so the chat has something to read", project)
	}
	// Before anything is written: the lead needs a login, like any agent.
	account, err := m.CheckLogin("claude", p, "")
	if err != nil {
		return state.Agent{}, err
	}
	baseRef := repo.CurrentBranch()
	commit, err := repo.ResolveCommit(baseRef)
	if err != nil {
		return state.Agent{}, err
	}

	a := state.Agent{
		Project: p.Name,
		Name:    state.LeadName,
		Title:   "Project chat",
		// The name is reserved, and the column is UNIQUE. No such machine exists.
		Instance:   InstanceName(p.Name, state.LeadName),
		AI:         "claude",
		Branch:     "", // detached: the lead commits nothing
		BaseRef:    baseRef,
		BaseCommit: commit,
		Worktree:   m.Paths.Worktree(p.Name, state.LeadName),
		Status:     state.AgentCreating,
		CreatedAt:  time.Now(),

		ClaudeAccount: account,
		Interface:     state.InterfaceChat,
		Role:          state.RoleLead,
	}
	if _, err := os.Stat(a.Worktree); err == nil {
		// Left by an interrupted create, or by a lead destroyed without git.
		_ = repo.RemoveWorktree(a.Worktree)
	}
	if err := m.Store.AddAgent(ctx, a); err != nil {
		return state.Agent{}, err
	}
	undo := func() {
		_ = repo.RemoveWorktree(a.Worktree)
		_ = m.Store.RemoveAgent(context.WithoutCancel(ctx), a.Project, a.Name)
	}
	m.logf("Creating the %s chat: a worktree on %s, detached", p.Name, baseRef)
	if err := repo.AddWorktreeDetached(a.Worktree, commit); err != nil {
		undo()
		return state.Agent{}, fmt.Errorf("creating the %s chat: worktree: %w", p.Name, err)
	}
	if err := m.configureLead(ctx, a, p, repo.Root, m.LeadSocket(a.Project)); err != nil {
		undo()
		return state.Agent{}, fmt.Errorf("creating the %s chat: %w", p.Name, err)
	}
	if err := m.Store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentReady); err != nil {
		undo()
		return state.Agent{}, err
	}
	a.Status = state.AgentReady
	return a, nil
}

// repairLead rebuilds what a lead needs but no longer has: its worktree, if the
// user removed it or git forgot it (see gitrepo.HasWorktree), and its private
// HOME, so settings changed by a new AgentBox version reach a lead made by an
// older one. It also moves the lead to the Claude Code account its project
// resolves to now (syncLeadAccount), so a session started from what it returns
// runs on that account.
func (m *Manager) repairLead(ctx context.Context, a state.Agent) (state.Agent, error) {
	p, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return a, err
	}
	if a, err = m.syncLeadAccount(ctx, a, p); err != nil {
		return a, err
	}
	if !repo.HasWorktree(a.Worktree) {
		m.logf("Recreating the %s chat's worktree", a.Project)
		_ = repo.RemoveWorktree(a.Worktree)
		if err := repo.AddWorktreeDetached(a.Worktree, a.BaseCommit); err != nil {
			return a, fmt.Errorf("recreating the %s chat's worktree: %w", a.Project, err)
		}
	}
	return a, m.configureLead(ctx, a, p, repo.Root, m.LeadSocket(a.Project))
}

// ReconfigureLead rewrites a project's chat brief where it already has one, so
// a project setting the brief states — its autonomy, the model its agents are
// created on, the Claude Code accounts it may use — reaches the chat when it is
// changed rather than whenever the chat next starts. It goes through
// configureLead, which carries the lead's own model across (carryClaudeModel),
// and moves the lead to the account the project resolves to now
// (syncLeadAccount). A project that has never been chatted with has no lead and
// nothing to rewrite, which is not an error: EnsureLead renders the brief, and
// resolves the account, from the settings as they are then.
func (m *Manager) ReconfigureLead(ctx context.Context, project string) error {
	a, err := m.Lead(ctx, project)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	p, repo, err := m.project(ctx, project)
	if err != nil {
		return err
	}
	// An account the project no longer resolves to doesn't stop the brief
	// from being rewritten: the chat's next start fails on it instead
	// (repairLead), which is where the user can see why.
	_, accountErr := m.syncLeadAccount(ctx, a, p)
	return errors.Join(m.configureLead(ctx, a, p, repo.Root, m.LeadSocket(a.Project)), accountErr)
}

// syncLeadAccount moves the lead to the Claude Code account its project
// resolves to now. The lead has no account of its own to pick — it follows
// the project, as agent create does for an agent that names none — but it is
// resolved once, when the lead is made, and stored on its row, which is where
// its chat reads the token from (LeadChatCommand). Without this, a project
// moved to another account kept its chat on the old one for good. The row is
// all that changes: a running session keeps the token it was started with
// until it restarts, and the conversation is not touched, since Claude Code
// keeps it in the lead's HOME, whichever account the session logs in with.
// A project whose account no longer resolves (removed, or not on its allow
// list) is refused rather than left on an account it may not use.
func (m *Manager) syncLeadAccount(ctx context.Context, a state.Agent, p state.Project) (state.Agent, error) {
	account, err := m.ClaudeAccountFor(p, "")
	if err != nil {
		return a, fmt.Errorf("the %s chat's Claude Code account: %w", a.Project, err)
	}
	if account == a.ClaudeAccount {
		return a, nil
	}
	m.logf("Moving the %s chat from the Claude Code account %q to %q", a.Project, a.ClaudeAccount, account)
	if err := m.Store.SetAgentClaudeAccount(ctx, a.Project, a.Name, account); err != nil {
		return a, err
	}
	a.ClaudeAccount = account
	return a, nil
}

// SyncLead moves the lead's worktree to the tip of the branch it stands on, so
// each turn reads what is on that branch now. Anything left in the worktree is
// discarded: nothing there is the lead's to keep.
func (m *Manager) SyncLead(ctx context.Context, a state.Agent) (state.Agent, error) {
	_, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return a, err
	}
	commit, err := repo.ResolveCommit(a.BaseRef)
	if err != nil {
		// The branch was renamed or deleted: stay where we are rather than fail.
		m.logf("chat %s: %s is gone (%v); staying on %s", a.Project, a.BaseRef, err, a.BaseCommit[:min(7, len(a.BaseCommit))])
		return a, nil
	}
	if commit == a.BaseCommit {
		return a, nil
	}
	if err := gitrepo.MoveWorktree(a.Worktree, commit); err != nil {
		return a, fmt.Errorf("moving the %s chat to %s: %w", a.Project, a.BaseRef, err)
	}
	if err := m.Store.SetAgentBaseCommit(ctx, a.Project, a.Name, commit); err != nil {
		return a, err
	}
	a.BaseCommit = commit
	return a, nil
}

// DestroyLead removes a project's chat: its worktree, its private HOME and its
// conversation. The project's agents are untouched.
func (m *Manager) DestroyLead(ctx context.Context, project string) error {
	a, err := m.Lead(ctx, project)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, repo, err := m.project(ctx, project); err == nil {
		_ = repo.RemoveWorktree(a.Worktree)
	} else {
		_ = os.RemoveAll(a.Worktree)
	}
	_ = os.RemoveAll(m.Paths.LeadHome(project))
	_ = os.RemoveAll(m.Paths.ChatImages(project, a.Name))
	return m.Store.RemoveAgent(ctx, a.Project, a.Name)
}

// configureLead writes the lead's private HOME: the settings that take its
// tools away, the brief, and the state that skips Claude Code's onboarding.
//
// socket, when set, is the project's lead socket: it is written into the MCP
// server's environment here, in a file the lead cannot change, so the only way
// it can act is through those tools and only on its own project.
func (m *Manager) configureLead(ctx context.Context, a state.Agent, p state.Project, root, socket string) error {
	home := m.Paths.LeadHome(a.Project)
	// The models are the ones a Claude Code adapter really advertised, plus
	// AgentBox's own small pinned list (D69), so a lead told to choose one
	// names a model worth naming even before any chat has run.
	menu, err := m.Store.Setting(ctx, state.SettingClaudeModelChoices)
	if err != nil {
		return err
	}
	// "default" is on that menu and is not a model: it is the menu's own word
	// for "the tool's own default" (modelSettingFor), so it says nothing about
	// what an agent will cost and has no place in a list to choose one from.
	models := state.MergePinnedClaudeModelValues(slices.DeleteFunc(state.ChoiceValues(menu), func(v string) bool { return v == "default" }))
	// OpenCode's models, when agents can run OpenCode at all. Same rule as the
	// Claude Code menu above: these are ids OpenCode itself named, never a
	// list AgentBox composed.
	var openCodeModels []string
	if ready, err := m.OpenCodeReady(ctx); err != nil {
		return err
	} else if ready {
		menu, err := m.Store.Setting(ctx, state.SettingOpenCodeModelChoices)
		if err != nil {
			return err
		}
		openCodeModels = state.ChoiceValues(menu)
	}
	projectNotes, err := notes.Read(m.Paths.ProjectNotes(a.Project))
	if err != nil {
		return err
	}
	// Named accounts (D39) this project may use, so the brief only tells
	// the lead to spread agents across them when there is more than one.
	claudeAccounts, err := m.Creds.ClaudeAccounts()
	if err != nil {
		return err
	}
	var accountNames []string
	for _, acc := range claudeAccounts {
		if len(p.ClaudeAccounts) == 0 || slices.Contains(p.ClaudeAccounts, acc.Name) {
			accountNames = append(accountNames, acc.Name)
		}
	}
	// Where the chat had got to, for a chat whose earlier session was
	// compacted away (D73). It is rendered on every configureLead rather than
	// only by the rollover, so a daemon restart, or any other rewrite of this
	// brief, doesn't quietly drop what the session was started with.
	recap, err := m.leadRecap(ctx, a.Project)
	if err != nil {
		return err
	}
	text, err := brief.RenderLead(brief.LeadData{
		VM:             hostos.InVM(),
		Host:           hostos.Name(),
		Project:        a.Project,
		Root:           root,
		Worktree:       a.Worktree,
		BaseRef:        a.BaseRef,
		Autonomy:       p.Autonomy,
		AgentModel:     p.AgentModel,
		ModelMenu:      models,
		OpenCodeMenu:   openCodeModels,
		ClaudeAccounts: accountNames,
		CanSpawn:       socket != "",
		Notes:          projectNotes,
		Recap:          recap,
	})
	if err != nil {
		return err
	}
	policy := leadSettings()
	// The policy is rewritten whole, so a lead made by an older AgentBox gets
	// what a newer one denies. The model isn't policy: it's what the user
	// chose, and PrepareChatModel put it here (D46), so it comes across.
	if was, err := os.ReadFile(filepath.Join(home, claudeSettingsFile)); err == nil {
		carryClaudeModel(was, policy)
	}
	settings, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	// Claude Code asks to trust a new folder, and there is nobody at the
	// lead's terminal to answer. The MCP server is registered here too: its
	// tools are how the lead reaches AgentBox and its agents.
	claude := map[string]any{
		"hasCompletedOnboarding": true,
		"projects":               map[string]any{a.Worktree: map[string]any{"hasTrustDialogAccepted": true}},
	}
	if socket != "" && m.Binary != "" {
		claude["mcpServers"] = map[string]any{
			"agentbox": map[string]any{
				"type": "stdio", "command": m.Binary, "args": []string{"mcp"},
				"env": map[string]string{"AGENTBOX_SOCKET": socket, "AGENTBOX_NO_AUTOSTART": "1"},
			},
		}
	}
	claudeState, err := json.Marshal(claude)
	if err != nil {
		return err
	}
	// The lead reads the repository to brief its agents, and sends searches to
	// Explore like any Claude Code: on Haiku rather than on the lead's own
	// model (D84). It has no display, so no desktop subagent.
	explore, err := exploreAgent()
	if err != nil {
		return err
	}
	files := map[string][]byte{
		".claude/settings.json": settings,
		".claude/CLAUDE.md":     []byte(text),
		".claude.json":          claudeState,
		"AGENTBOX.md":           []byte(text),
		exploreAgentFile:        []byte(explore),
	}
	for rel, content := range files {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// leadSettings is the lead's permission policy: none of its own (D89). It
// runs on the user's machine as the user, with every tool Claude Code has and
// no fence around what it reads — what the user's own Claude Code in a
// terminal has. What stands between it and the machine is the mode it runs
// in: "default" asks before a command, an edit or a fetch, and the question
// reaches the user in the project's chat, who may switch the mode there like
// in any other chat. It's rewritten whole every time, so a lead made by an
// older AgentBox loses the deny list that used to keep it off the host.
func leadSettings() map[string]any {
	return map[string]any{
		"permissions": map[string]any{
			"defaultMode": "default",
		},
		"includeCoAuthoredBy": false,
	}
}

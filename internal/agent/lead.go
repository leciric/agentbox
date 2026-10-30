package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"agentbox/internal/brief"
	"agentbox/internal/gitrepo"
	"agentbox/internal/hostos"
	"agentbox/internal/memory"
	"agentbox/internal/notes"
	"agentbox/internal/opencode"
	"agentbox/internal/state"
)

// The lead is a project's chat: the one agent that runs on the host instead of
// in a machine of its own, so a project you have never chatted with costs
// nothing. It directs the project's agents, and since D89 it also has the
// user's machine, the way the user's own Claude Code in a terminal does: a
// shell, and every tool, asking before it uses them (see leadSettings).

// LeadSocket returns the path of the socket a project's chat session reaches
// AgentBox through, or "" when the manager has none (a preview, or a test).
// Each session has its own, which is how the daemon knows which session made
// an agent or asked for anything.
func (m *Manager) LeadSocket(a state.Agent) string {
	if m.LeadSocketPath == nil {
		return ""
	}
	return m.LeadSocketPath(a.Project, a.Session())
}

// leadHome is a lead's private HOME: its session's (paths.LeadSessionHome).
func (m *Manager) leadHome(a state.Agent) string {
	return m.Paths.LeadSessionHome(a.Project, a.Session())
}

// LeadRef is the agent ref of a project's lead: Main's.
func LeadRef(project string) string { return project + "/" + state.LeadName }

// SessionRef is the agent ref of one of a project's chat sessions.
func SessionRef(project, session string) string {
	return project + "/" + state.LeadSessionName(session)
}

// Lead returns the lead of a project's first session, the one migrated from
// its chat before sessions (state.MigratedSession), or state.ErrNotFound if
// it has never been used.
func (m *Manager) Lead(ctx context.Context, project string) (state.Agent, error) {
	return m.LeadSession(ctx, project, state.MigratedSession)
}

// LeadSession returns the lead of one of a project's chat sessions, or
// state.ErrNotFound if it has never been written to.
func (m *Manager) LeadSession(ctx context.Context, project, session string) (state.Agent, error) {
	a, err := m.Store.Agent(ctx, project, state.LeadSessionName(session))
	if err != nil {
		return state.Agent{}, err
	}
	if !a.IsLead() {
		return state.Agent{}, fmt.Errorf("agent %s isn't a lead", a.Ref())
	}
	return a, nil
}

// EnsureLead is EnsureLeadSession for the project's first session
// (state.MigratedSession), opened as "Chat" when the project doesn't have it.
func (m *Manager) EnsureLead(ctx context.Context, project string) (state.Agent, error) {
	if _, err := m.Store.LeadSession(ctx, project, state.MigratedSession); errors.Is(err, state.ErrNotFound) {
		if err := m.Store.AddLeadSessionID(ctx, project, state.MigratedSession, "Chat", time.Now()); err != nil && !errors.Is(err, state.ErrExists) {
			return state.Agent{}, err
		}
	} else if err != nil {
		return state.Agent{}, err
	}
	return m.EnsureLeadSession(ctx, project, state.MigratedSession)
}

// leadWorktree is where every session of a project's chat reads the project:
// one detached worktree, which no lead ever writes to.
func (m *Manager) leadWorktree(project string) string {
	return m.Paths.Worktree(project, state.LeadName)
}

// EnsureLeadSession returns the lead of one of a project's chat sessions,
// creating it the first time the session is written to. Creating one makes no
// machine: a private HOME and a row, and the chat's worktree, detached on the
// project's branch, when no other session has made it.
//
// One call at a time per lead (#454): opening a new chat and its first
// message both reach here, and a create can take a minute, so without the
// lock both would find no lead and the second insert would fail on the
// UNIQUE name. The one that waits gets the lead the first one made.
func (m *Manager) EnsureLeadSession(ctx context.Context, project, session string) (state.Agent, error) {
	ls, err := m.Store.LeadSession(ctx, project, session)
	if err != nil {
		return state.Agent{}, err
	}
	name := state.LeadSessionName(session)
	unlock, err := lockLead(ctx, m.Paths.Worktree(project, name))
	if err != nil {
		return state.Agent{}, err
	}
	defer unlock()

	switch a, err := m.LeadSession(ctx, project, session); {
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
	// Before anything is written: the lead needs a login, like any agent, on
	// the tool and account its composer chose before its first message.
	a := state.Agent{
		Project: p.Name,
		Name:    name,
		Title:   ls.Topic,
		// The name is reserved, and the column is UNIQUE. No such machine exists.
		Instance:  InstanceName(p.Name, name),
		Branch:    "", // detached: the lead commits nothing
		Worktree:  m.leadWorktree(p.Name),
		Status:    state.AgentCreating,
		CreatedAt: time.Now(),

		Interface: state.InterfaceChat,
		Role:      state.RoleLead,
	}
	choice, err := m.LeadChoiceOf(ctx, a)
	if err != nil {
		return state.Agent{}, err
	}
	a.AI = choice.Tool
	if a.ClaudeAccount, err = m.leadAccount(p, choice); err != nil {
		return state.Agent{}, err
	}
	a.BaseRef = repo.CurrentBranch()
	if a.BaseCommit, err = repo.ResolveCommit(a.BaseRef); err != nil {
		return state.Agent{}, err
	}
	if err := m.Store.AddAgent(ctx, a); err != nil {
		return state.Agent{}, err
	}
	undo := func() { _ = m.Store.RemoveAgent(context.WithoutCancel(ctx), a.Project, a.Name) }
	if err := m.ensureLeadWorktree(ctx, repo, a); err != nil {
		undo()
		return state.Agent{}, fmt.Errorf("creating the %s chat: worktree: %w", p.Name, err)
	}
	m.logf("Creating the %s chat's %q session", p.Name, ls.Topic)
	if err := m.configureLead(ctx, a, p, repo.Root, m.LeadSocket(a)); err != nil {
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

// ensureLeadWorktree makes the chat's worktree when git doesn't know it,
// detached at the lead's commit, which every session shares. A directory left
// by an interrupted create, or by a chat destroyed without git, goes first.
func (m *Manager) ensureLeadWorktree(ctx context.Context, repo gitrepo.Repo, a state.Agent) error {
	unlock, err := lockLead(ctx, "worktree:"+a.Worktree)
	if err != nil {
		return err
	}
	defer unlock()
	if repo.HasWorktree(a.Worktree) {
		return nil
	}
	_ = repo.RemoveWorktree(a.Worktree)
	_ = os.RemoveAll(a.Worktree)
	m.logf("Creating the %s chat's worktree on %s, detached", a.Project, a.BaseRef)
	return repo.AddWorktreeDetached(a.Worktree, a.BaseCommit, "agentbox: "+a.Project+" chat")
}

// DeleteLeadSession removes what a deleted session's lead had on disk: its
// HOME and its chat's images, and the chat's worktree once no session is
// left to read it. Its rows went with Store.DeleteLeadSession.
func (m *Manager) DeleteLeadSession(ctx context.Context, project, session string) {
	_ = os.RemoveAll(m.Paths.LeadSessionHome(project, session))
	_ = os.RemoveAll(m.Paths.ChatImages(project, state.LeadSessionName(session)))
	if leads, err := m.SessionLeads(ctx, project); err == nil && len(leads) == 0 {
		if _, repo, err := m.project(ctx, project); err == nil {
			_ = repo.RemoveWorktree(m.leadWorktree(project))
		}
	}
}

// leadLocks keeps one EnsureLead per lead in flight. The daemon makes a
// Manager for every request, and schedules make their own, so the locks live
// with the package, keyed by the lead's worktree, which is unique to the
// installation and the project. Each is a channel so that a request that gives
// up stops waiting.
var (
	leadLocksMu sync.Mutex
	leadLocks   = map[string]chan struct{}{}
)

func lockLead(ctx context.Context, key string) (unlock func(), err error) {
	leadLocksMu.Lock()
	l := leadLocks[key]
	if l == nil {
		l = make(chan struct{}, 1)
		leadLocks[key] = l
	}
	leadLocksMu.Unlock()
	select {
	case l <- struct{}{}:
		return func() { <-l }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
		if a, err = m.rebaseLead(ctx, a, repo); err != nil {
			return a, err
		}
		if err := m.ensureLeadWorktree(ctx, repo, a); err != nil {
			return a, fmt.Errorf("recreating the %s chat's worktree: %w", a.Project, err)
		}
	}
	return a, m.configureLead(ctx, a, p, repo.Root, m.LeadSocket(a))
}

// rebaseLead finds a commit to rebuild the lead's worktree on when the one it
// stood on is gone, as it is when the project's repository was cloned afresh
// and the lead had stood on a branch never pushed: its branch's tip, or, with
// that branch gone too, the branch the project is on now, as EnsureLead would
// pick. The lead commits nothing, so nothing is lost.
func (m *Manager) rebaseLead(ctx context.Context, a state.Agent, repo gitrepo.Repo) (state.Agent, error) {
	if _, err := repo.ResolveCommit(a.BaseCommit); err == nil {
		return a, nil
	}
	ref := a.BaseRef
	commit, err := repo.ResolveCommit(ref)
	if err != nil {
		ref = repo.CurrentBranch()
		if commit, err = repo.ResolveCommit(ref); err != nil {
			return a, fmt.Errorf("recreating the %s chat's worktree: %w", a.Project, err)
		}
	}
	m.logf("The %s chat's commit %s is gone; moving it to %s", a.Project, a.BaseCommit[:min(7, len(a.BaseCommit))], ref)
	if err := m.Store.SetAgentBase(ctx, a.Project, a.Name, ref, commit); err != nil {
		return a, err
	}
	a.BaseRef, a.BaseCommit = ref, commit
	return a, nil
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
//
// Every session of the chat is rewritten: they share the project's settings,
// and each one's brief names the others.
func (m *Manager) ReconfigureLead(ctx context.Context, project string) error {
	leads, err := m.SessionLeads(ctx, project)
	if err != nil || len(leads) == 0 {
		return err
	}
	p, repo, err := m.project(ctx, project)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range leads {
		// Under the lead's own lock: a session being made writes its HOME in
		// EnsureLeadSession, and the daemon rewrites briefs in the background
		// as that happens (sessionsChanged).
		unlock, err := lockLead(ctx, m.Paths.Worktree(a.Project, a.Name))
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		// An account the project no longer resolves to doesn't stop the brief
		// from being rewritten: the chat's next start fails on it instead
		// (repairLead), which is where the user can see why.
		_, accountErr := m.syncLeadAccount(ctx, a, p)
		errs = append(errs, m.configureLead(ctx, a, p, repo.Root, m.LeadSocket(a)), accountErr)
		unlock()
	}
	return errors.Join(errs...)
}

// SessionLeads are the leads of a project's chat sessions that have been
// written to, the migrated session's first.
func (m *Manager) SessionLeads(ctx context.Context, project string) ([]state.Agent, error) {
	agents, err := m.Store.Agents(ctx, project)
	if err != nil {
		return nil, err
	}
	var out []state.Agent
	for _, a := range agents {
		if a.IsLead() {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(x, y state.Agent) int {
		return cmp.Compare(boolInt(x.Session() != state.MigratedSession), boolInt(y.Session() != state.MigratedSession))
	})
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// syncLeadAccount moves the lead to the AI tool its chat runs and the Claude
// Code account it resolves to now: the one its composer chose, while the
// project may use it, else the project's (leadAccount). It is resolved when
// the lead is made, and stored on its row, which is where its chat reads the
// token from (LeadChatCommand). Without this, a project moved to another
// account kept its chat on the old one for good. The row is all that changes
// here: a running session keeps the token it was started with until its
// adapter restarts, which the daemon asks for once the account has changed
// (chat.Manager.AccountChanged), and the conversation carries on, since
// Claude Code keeps it in the lead's HOME, whichever account the session logs
// in with. A lead on another tool has no Claude Code account.
// A project whose account no longer resolves (removed, or not on its allow
// list) is refused rather than left on an account it may not use.
func (m *Manager) syncLeadAccount(ctx context.Context, a state.Agent, p state.Project) (state.Agent, error) {
	choice, err := m.LeadChoiceOf(ctx, a)
	if err != nil {
		return a, err
	}
	if choice.Tool != a.AI {
		m.logf("The %s chat runs %s now, not %s", a.Project, choice.Tool, a.AI)
		if err := m.Store.SetAgentAI(ctx, a.Project, a.Name, choice.Tool); err != nil {
			return a, err
		}
		a.AI = choice.Tool
	}
	account, err := m.leadAccount(p, choice)
	if err != nil {
		return a, fmt.Errorf("the %s chat's %s login: %w", a.Project, ChatAdapters[a.AI].Tool, err)
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

// DestroyLead removes a project's chat: every session's private HOME and
// conversation, and the worktree they read. The project's agents are
// untouched, and belong to no session after it.
func (m *Manager) DestroyLead(ctx context.Context, project string) error {
	leads, err := m.SessionLeads(ctx, project)
	if err != nil {
		return err
	}
	sessions, err := m.Store.LeadSessions(ctx, project)
	if err != nil {
		return err
	}
	if _, repo, err := m.project(ctx, project); err == nil {
		_ = repo.RemoveWorktree(m.leadWorktree(project))
	} else {
		_ = os.RemoveAll(m.leadWorktree(project))
	}
	for _, a := range leads {
		_ = os.RemoveAll(m.leadHome(a))
		_ = os.RemoveAll(m.Paths.ChatImages(project, a.Name))
		if err := m.Store.RemoveAgent(ctx, a.Project, a.Name); err != nil {
			return err
		}
	}
	for _, ls := range sessions {
		_ = os.RemoveAll(m.Paths.LeadSessionHome(project, ls.ID))
	}
	return m.Store.RemoveLeadSessions(ctx, project)
}

// configureLead writes the lead's private HOME: the settings that take its
// tools away, the brief, and the state that skips Claude Code's onboarding.
//
// socket, when set, is the project's lead socket: it is written into the MCP
// server's environment here, in a file the lead cannot change, so the only way
// it can act is through those tools and only on its own project.
func (m *Manager) configureLead(ctx context.Context, a state.Agent, p state.Project, root, socket string) error {
	home := m.leadHome(a)
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
	recap, err := m.leadRecap(ctx, a)
	if err != nil {
		return err
	}
	// Memories enough agents have been handed to be worth offering as notes
	// (memory/promote.go). Only to a lead with the tools to answer them, and
	// best-effort: a brief is worth writing without its offers.
	var suggestions []brief.NoteSuggestion
	if socket != "" {
		offered, err := m.memory().OfferNoteSuggestions(ctx, a.Project)
		if err != nil {
			m.logf("offering %s's memories as notes: %v", a.Project, err)
		}
		for _, n := range offered {
			suggestions = append(suggestions, brief.NoteSuggestion{
				ID: n.ID, Title: n.Title, Text: memory.Excerpt(n.Content, noteSuggestionExcerpt), Agents: n.Agents,
			})
		}
	}
	// Whether the pull request watch is on here: the project's own say, or
	// the installation's (the daemon's prWatchOn reads it the same way).
	prWatch := p.PRWatch == state.PRWatchOn
	if p.PRWatch == "" {
		on, err := m.Store.FlagOn(ctx, state.SettingPRWatch)
		prWatch = err != nil || on
	}
	defModel, defWindow, enforced, err := m.LeadAgentDefaults(ctx, p)
	if err != nil {
		return err
	}
	recheck, _, _ := m.Store.LeadRecheck(ctx)
	session, others, err := m.sessionTopics(ctx, a)
	if err != nil {
		return err
	}
	text, err := brief.RenderLead(brief.LeadData{
		Session:              session,
		OtherSessions:        others,
		Recheck:              recheck,
		PRWatch:              prWatch,
		VM:                   hostos.InVM(),
		Host:                 hostos.Name(),
		Project:              a.Project,
		ProjectName:          p.DisplayName,
		Root:                 root,
		Worktree:             a.Worktree,
		BaseRef:              a.BaseRef,
		Autonomy:             p.Autonomy,
		AgentModel:           leadAgentModel(p),
		AgentTool:            agentToolName(p.AgentTool),
		AgentDefaultModel:    defModel,
		AgentDefaultWindow:   strings.ToLower(state.FormatContextWindow(defWindow)),
		EnforceAgentDefaults: enforced,
		ModelMenu:            models,
		OpenCodeMenu:         openCodeModels,
		CursorReady:          m.CursorReady(),
		ClaudeAccounts:       accountNames,
		CanSpawn:             socket != "",
		AgentPRs:             p.AgentPRs,
		Notes:                projectNotes,
		Recap:                recap,
		NoteSuggestions:      suggestions,
	})
	if err != nil {
		return err
	}
	connectors, err := m.connectorNames(ctx, a)
	if err != nil {
		return err
	}
	version, err := m.openCodeVersion(ctx)
	if err != nil {
		return err
	}
	if err := m.writeLeadHome(home, a.Worktree, text, socket, nil, connectors, m.leadGitHubToken(p) != "", version); err != nil {
		return err
	}
	return m.WriteSkills(ctx, a)
}

// sessionTopics is the topic of the session a lead runs, and the project's
// other sessions' topics, for its brief: neither when it is the only one.
func (m *Manager) sessionTopics(ctx context.Context, a state.Agent) (string, []string, error) {
	sessions, err := m.Store.LeadSessions(ctx, a.Project)
	if err != nil || len(sessions) < 2 {
		return "", nil, err
	}
	var own string
	var others []string
	for _, ls := range sessions {
		if ls.ID == a.Session() {
			own = ls.Topic
		} else {
			others = append(others, ls.Topic)
		}
	}
	return own, others, nil
}

// writeLeadHome writes what a lead's private HOME holds: its policy, its brief,
// the state that skips Claude Code's onboarding and registers the MCP server,
// and its git config. A project's lead and the Home chat share it, and differ
// in their brief, in mcpEnv (added to the MCP server's environment) and in
// connectors, the project's enabled connectors (leadMCPServers); the Home chat
// spans every project rather than one, so it is given none.
func (m *Manager) writeLeadHome(home, worktree, text, socket string, mcpEnv map[string]string, connectors []string, github bool, version opencode.Release) error {
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
		"projects":               map[string]any{worktree: map[string]any{"hasTrustDialogAccepted": true}},
	}
	if socket != "" && m.Binary != "" {
		env := map[string]string{"AGENTBOX_SOCKET": socket, "AGENTBOX_NO_AUTOSTART": "1"}
		for k, v := range mcpEnv {
			env[k] = v
		}
		servers := m.leadMCPServers(socket, connectors)
		servers["agentbox"] = map[string]any{
			"type": "stdio", "command": m.Binary, "args": []string{"mcp"},
			"env": env,
		}
		claude["mcpServers"] = servers
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
	// The other tools' brief, MCP servers and login, so a chat switched to one
	// of them finds them (leadtools.go).
	servers, _ := claude["mcpServers"].(map[string]any)
	others, stale, err := m.leadToolFiles(home, worktree, text, servers, version)
	if err != nil {
		return err
	}
	maps.Copy(files, others)
	for _, path := range stale {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// Git and GitHub, the way an agent has them (see gitConfig): the lead's
	// HOME is its own, so this is the only git config it reads, and the user's
	// ~/.gitconfig and ~/.ssh are never touched. Without an account the file
	// goes, so a lead on the host falls back to whatever ssh finds there.
	gitconfig := filepath.Join(home, ".gitconfig")
	if github {
		files[".gitconfig"] = []byte(leadGitConfig())
	} else if err := os.Remove(gitconfig); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
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

// leadGitHubToken is the token of the GitHub account the project's agents get
// when none is named for them (GitHubAccountFor), which the lead uses too: in
// its chat's environment (LeadChatCommand) and, through leadGitConfig, for git.
// GitHub is optional, so an account that no longer resolves leaves the lead
// without one rather than without a chat.
func (m *Manager) leadGitHubToken(p state.Project) string {
	token, err := m.GitHubToken(p)
	if err != nil {
		m.logf("The %s chat has no GitHub account: %v", p.Name, err)
		return ""
	}
	return token
}

// GitHubToken is the token of the GitHub account the project's agents get
// (GitHubAccountFor), or "" when there is none.
func (m *Manager) GitHubToken(p state.Project) (string, error) {
	account, err := m.GitHubAccountFor(p, "")
	if err != nil || account == "" {
		return "", err
	}
	return m.Creds.GitHubToken(account)
}

// leadCredentialHelper answers git's credential requests for github.com with
// GH_TOKEN, without needing gh: the lead runs on the host (or AgentBox's
// VM), which need not have it.
const leadCredentialHelper = gitrepo.GitHubCredentialHelper

// leadGitConfig is the lead's ~/.gitconfig: an agent's (gitConfig), with a
// credential helper of its own. The lead's git would otherwise reach
// git@github.com over ssh, which works on a host whose user has keys but not
// in the VM, whose home has none (and no known_hosts either).
func leadGitConfig() string { return gitConfigWith(gitQuote(leadCredentialHelper), "") }

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

// noteSuggestionExcerpt is how much of an offered memory's content the lead's
// brief carries: enough to judge it by, since the brief is resent with every
// call the lead makes. search_memory has the rest.
const noteSuggestionExcerpt = 200

// leadAgentModel is the project's model as the lead's brief reads it, which is
// a Claude Code one: none for a project whose agents run another tool, unless
// it leaves the model to the lead.
func leadAgentModel(p state.Project) string {
	if p.ModelTool() != "claude" && !p.LeadPicksModel() {
		return ""
	}
	return p.AgentModel
}

// agentToolName is a project's AgentTool as a brief names it.
func agentToolName(tool string) string {
	if tool == "" {
		return ""
	}
	if a, ok := ChatAdapters[tool]; ok && a.Tool != "" {
		return a.Tool
	}
	return tool
}

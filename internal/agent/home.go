package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"agentbox/internal/brief"
	"agentbox/internal/hostos"
	"agentbox/internal/state"
)

// The Home chat is the user's main chat, across every project and tied to
// none. It is a lead like a project's — Claude Code on the host through its
// ACP adapter, with no machine — but it stands in ~/.agentbox rather than in a
// worktree, and its tools (agentbox mcp, with AGENTBOX_CHAT=home) reach every
// project instead of one. Nothing about it is in the agents table: its key,
// state.HomeProject, has no projects row to hang one from, so Home builds it
// whole each time, and its conversation, session and token rows are kept under
// that key like any lead's.
//
// It has sessions as a project's chat does (state/leadsessions.go), kept under
// the same key: each its own conversation, HOME and socket, under the lead name
// state.LeadSessionName gives it. Which sessions there are, and which one hears
// what, is the store's and the daemon's; this only builds a session's lead.

// HomeRef is the Home chat's agent ref.
var HomeRef = LeadRef(state.HomeProject)

// homeProject is the project the Home chat resolves its accounts against: one
// that names none, so it gets the machine's defaults.
var homeProject = state.Project{Name: state.HomeProject}

// Home is HomeSession for the session the Home chat had before sessions.
func (m *Manager) Home() state.Agent { return m.HomeSession(state.MigratedSession) }

// HomeSession returns one session of the Home chat as an agent, without making
// anything. Its Status is AgentReady once EnsureHomeSession has written its
// HOME, and empty before. It runs on the machine's default Claude Code
// account, whichever that is now, unless its composer chose another.
func (m *Manager) HomeSession(session string) state.Agent {
	a := state.Agent{
		Project:   state.HomeProject,
		Name:      state.LeadSessionName(session),
		Title:     "Home",
		AI:        "claude",
		Worktree:  m.Paths.HomeChat(),
		Interface: state.InterfaceChat,
		Role:      state.RoleLead,
	}
	if _, err := os.Stat(filepath.Join(m.leadHome(a), ".claude.json")); err == nil {
		a.Status = state.AgentReady
	}
	choice, err := m.LeadChoiceOf(context.Background(), a)
	if err != nil {
		m.logf("The Home chat's tool: %v", err)
	}
	a.AI = choice.Tool
	a.ClaudeAccount, _ = m.leadAccount(homeProject, choice)
	return a
}

// EnsureHome is EnsureHomeSession for the session the Home chat had before
// sessions.
func (m *Manager) EnsureHome(ctx context.Context) (state.Agent, error) {
	return m.EnsureHomeSession(ctx, state.MigratedSession)
}

// EnsureHomeSession readies one session of the Home chat for a turn: its
// working directory, made if it is missing, and its private HOME, rewritten
// every time so the projects its brief lists are the ones there are now.
func (m *Manager) EnsureHomeSession(ctx context.Context, session string) (state.Agent, error) {
	a := m.HomeSession(session)
	choice, err := m.LeadChoiceOf(ctx, a)
	if err != nil {
		return state.Agent{}, err
	}
	if a.ClaudeAccount, err = m.leadAccount(homeProject, choice); err != nil {
		return state.Agent{}, err
	}
	if err := os.MkdirAll(a.Worktree, 0o700); err != nil {
		return state.Agent{}, err
	}
	if err := m.configureHome(ctx, a); err != nil {
		return state.Agent{}, err
	}
	a.Status = state.AgentReady
	return a, nil
}

// DestroyHome forgets the Home chat: every session's conversation, private
// HOME and pictures, and the sessions themselves. Its working directory is
// AgentBox's own folder, and stays.
func (m *Manager) DestroyHome(ctx context.Context) error {
	sessions, err := m.Store.LeadSessions(ctx, state.HomeProject)
	if err != nil {
		return err
	}
	// The session from before sessions, whether or not it is still listed.
	ids := []string{state.MigratedSession}
	for _, ls := range sessions {
		ids = append(ids, ls.ID)
	}
	var errs []error
	for _, id := range ids {
		name := state.LeadSessionName(id)
		errs = append(errs,
			os.RemoveAll(m.Paths.LeadSessionHome(state.HomeProject, id)),
			os.RemoveAll(m.Paths.ChatImages(state.HomeProject, name)),
			// No agents row, but RemoveAgent takes the conversation with it.
			m.Store.RemoveAgent(ctx, state.HomeProject, name),
		)
	}
	return errors.Join(append(errs, m.Store.RemoveLeadSessions(ctx, state.HomeProject))...)
}

func (m *Manager) configureHome(ctx context.Context, a state.Agent) error {
	projects, err := m.Store.Projects(ctx)
	if err != nil {
		return err
	}
	d := brief.HomeData{Dir: a.Worktree, VM: hostos.InVM(), Host: hostos.Name()}
	if d.Session, d.OtherSessions, err = m.sessionTopics(ctx, a); err != nil {
		return err
	}
	for _, p := range projects {
		d.Projects = append(d.Projects, brief.HomeProject{Name: p.DisplayName, Root: p.Root})
	}
	text, err := brief.RenderHome(d)
	if err != nil {
		return err
	}
	home := m.leadHome(a)
	if err := m.WriteSkills(ctx, a); err != nil {
		return err
	}
	version, err := m.openCodeVersion(ctx)
	if err != nil {
		return err
	}
	return m.writeLeadHome(home, a.Worktree, text, m.LeadSocket(a),
		map[string]string{"AGENTBOX_CHAT": "home"}, nil, m.leadGitHubToken(homeProject) != "", version)
}

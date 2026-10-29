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

// HomeRef is the Home chat's agent ref.
var HomeRef = LeadRef(state.HomeProject)

// homeProject is the project the Home chat resolves its accounts against: one
// that names none, so it gets the machine's defaults.
var homeProject = state.Project{Name: state.HomeProject}

// Home returns the Home chat as an agent, without making anything. Its Status
// is AgentReady once EnsureHome has written its HOME, and empty before. It runs
// on the machine's default Claude Code account, whichever that is now.
func (m *Manager) Home() state.Agent {
	a := state.Agent{
		Project:   state.HomeProject,
		Name:      state.LeadName,
		Title:     "Home",
		AI:        "claude",
		Worktree:  m.Paths.HomeChat(),
		Interface: state.InterfaceChat,
		Role:      state.RoleLead,
	}
	if _, err := os.Stat(filepath.Join(m.Paths.LeadHome(state.HomeProject), ".claude.json")); err == nil {
		a.Status = state.AgentReady
	}
	choice, err := m.LeadChoice(context.Background(), state.HomeProject)
	if err != nil {
		m.logf("The Home chat's tool: %v", err)
	}
	a.AI = choice.Tool
	a.ClaudeAccount, _ = m.leadAccount(homeProject, choice)
	return a
}

// EnsureHome readies the Home chat for a turn: its working directory, made if
// it is missing, and its private HOME, rewritten every time so the projects
// its brief lists are the ones there are now.
func (m *Manager) EnsureHome(ctx context.Context) (state.Agent, error) {
	a := m.Home()
	choice, err := m.LeadChoice(ctx, state.HomeProject)
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

// DestroyHome forgets the Home chat: its conversation, its private HOME and
// its pictures. Its working directory is AgentBox's own folder, and stays.
func (m *Manager) DestroyHome(ctx context.Context) error {
	return errors.Join(
		os.RemoveAll(m.Paths.LeadHome(state.HomeProject)),
		os.RemoveAll(m.Paths.ChatImages(state.HomeProject, state.LeadName)),
		// No agents row, but RemoveAgent takes the conversation with it.
		m.Store.RemoveAgent(ctx, state.HomeProject, state.LeadName),
	)
}

func (m *Manager) configureHome(ctx context.Context, a state.Agent) error {
	projects, err := m.Store.Projects(ctx)
	if err != nil {
		return err
	}
	d := brief.HomeData{Dir: a.Worktree, VM: hostos.InVM(), Host: hostos.Name()}
	for _, p := range projects {
		d.Projects = append(d.Projects, brief.HomeProject{Name: p.DisplayName, Root: p.Root})
	}
	text, err := brief.RenderHome(d)
	if err != nil {
		return err
	}
	home := m.Paths.LeadHome(state.HomeProject)
	if err := m.WriteSkills(ctx, a); err != nil {
		return err
	}
	return m.writeLeadHome(home, a.Worktree, text, m.LeadSocket(state.HomeProject),
		map[string]string{"AGENTBOX_CHAT": "home"}, nil, m.leadGitHubToken(homeProject) != "")
}

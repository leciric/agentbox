package agent

import (
	"context"
	"fmt"
	"time"

	"agentbox/internal/state"
)

// Enqueue puts a new agent in its project's queue: it is checked the way
// Create checks it before copying anything, and given its name, title and
// branch now, but no worktree and no machine. request is what the daemon
// starts it from later (Create with CreateOptions.Queued), kept alongside it.
// The branch named here isn't made until then.
func (m *Manager) Enqueue(ctx context.Context, project string, opts CreateOptions, request []byte) (state.Agent, error) {
	if _, ok := Tools[opts.AI]; !ok {
		return state.Agent{}, fmt.Errorf("unknown AI tool %q: use claude, codex, opencode or none", opts.AI)
	}
	if opts.FinishNotice != "" && opts.FinishNotice != state.FinishNoticesChat && opts.FinishNotice != state.FinishNoticesOff {
		return state.Agent{}, fmt.Errorf("unknown finish notice %q: use chat or off, or leave it out to follow the project", opts.FinishNotice)
	}
	if err := m.ChatChoices(ctx, opts.AI, opts.Model, opts.Effort); err != nil {
		return state.Agent{}, err
	}
	if err := validateName(opts.Name); err != nil {
		return state.Agent{}, err
	}
	if err := CheckBranchSlug(opts.Branch); err != nil {
		return state.Agent{}, err
	}
	title, err := CleanTitle(opts.Title)
	if err != nil {
		return state.Agent{}, err
	}
	iface, err := interfaceFor(opts.AI, opts.Interface)
	if err != nil {
		return state.Agent{}, err
	}
	size, err := CheckSize(opts.Size)
	if err != nil {
		return state.Agent{}, err
	}
	p, repo, err := m.project(ctx, project)
	if err != nil {
		return state.Agent{}, err
	}
	account, err := m.CheckLogin(opts.AI, p, opts.ClaudeAccount)
	if err != nil {
		return state.Agent{}, err
	}
	ghAccount, err := m.GitHubAccountFor(p, opts.GitHubAccount)
	if err != nil {
		return state.Agent{}, err
	}
	name := opts.Name
	if name == "" {
		if name, err = m.nextName(ctx, p, repo); err != nil {
			return state.Agent{}, fmt.Errorf("choosing a name: %w", err)
		}
	}
	from := opts.From
	if from == "" {
		from = repo.CurrentBranch()
	}
	a := state.Agent{
		Project:       p.Name,
		Name:          name,
		Title:         title,
		Instance:      InstanceName(p.Name, name),
		AI:            opts.AI,
		Autonomous:    opts.Autonomous,
		Branch:        m.branchFor(ctx, p, repo, opts.Branch, title, opts.Task, name),
		BaseRef:       from,
		Worktree:      m.Paths.Worktree(p.Name, name),
		Status:        state.AgentQueued,
		CreatedAt:     time.Now(),
		ClaudeAccount: account,
		GitHubAccount: ghAccount,
		Interface:     iface,
		FinishNotice:  opts.FinishNotice,
		Size:          size,
		Parent:        opts.Parent,
	}
	if err := m.Store.Enqueue(ctx, a, request); err != nil {
		return state.Agent{}, err
	}
	return m.Store.Agent(ctx, a.Project, a.Name)
}

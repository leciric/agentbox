package agent

import (
	"context"
	"errors"
	"fmt"

	"agentbox/internal/image"
	"agentbox/internal/incus"
	"agentbox/internal/state"
)

// ErrNotBeingMade is FinishCreate on an agent whose create isn't unfinished.
var ErrNotBeingMade = errors.New("it isn't being made")

// FinishCreate makes the machine of an agent whose create AgentBox's stop cut
// short — the daemon killed, the VM going down — and left "incomplete": its
// row and worktree are there, marked creating, and its machine is missing or
// half made. What Create would still have done is done: the machine made
// anew from what the agent was being made from (the half-made one goes
// first), its first snapshot, and the agent marked ready. task is what it is
// about to be asked to do, for its brief (CreateOptions.Task).
//
// A create stopped before the worktree was made leaves nothing to finish:
// that agent is destroyed and made again. A failure removes the new machine
// and leaves the agent as it was, to try again.
func (m *Manager) FinishCreate(ctx context.Context, a state.Agent, task string) (state.Agent, error) {
	if a.IsLead() || a.Status != state.AgentCreating {
		return state.Agent{}, fmt.Errorf("%s: %w", a.Ref(), ErrNotBeingMade)
	}
	p, repo, err := m.project(ctx, a.Project)
	if err != nil {
		return state.Agent{}, err
	}
	if !repo.HasWorktree(a.Worktree) {
		return state.Agent{}, fmt.Errorf("%s was stopped before its worktree was made, so there is nothing to finish: destroy it and create it again", a.Ref())
	}
	switch _, err := m.Incus.Instance(ctx, a.Instance); {
	case err == nil:
		m.logf("Removing %s, which the interrupted create left half made", a.Instance)
		if err := m.Incus.Delete(ctx, a.Instance); err != nil {
			return state.Agent{}, err
		}
	case !errors.Is(err, incus.ErrNotFound):
		return state.Agent{}, err
	}
	source := a.Source
	if source == "" || source == image.SnapshotRef() {
		source = image.SnapshotRef()
		ready, err := image.Ready(ctx, m.Incus)
		if err != nil {
			return state.Agent{}, err
		}
		if !ready {
			return state.Agent{}, errors.New("the base image isn't built: run agentbox image build")
		}
	}
	envFiles, err := repo.EnvFiles()
	if err != nil {
		return state.Agent{}, err
	}
	var undo []func()
	fail := func(step string, err error) (state.Agent, error) {
		m.logf("%s failed; removing the new machine", step)
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		return state.Agent{}, &CreateError{Ref: a.Ref(), Step: step, Err: err}
	}
	if step, err := m.makeMachine(ctx, a, repo, source, envFiles, task, "", &undo); err != nil {
		return fail(step, err)
	}
	// The first snapshot, unless the create got as far as its worktree half.
	if _, err := repo.ResolveRef(snapshotRef(a.Name, initialSnapshot)); err != nil {
		m.logf("Taking the %q snapshot", initialSnapshot)
		if _, err := m.takeSnapshot(ctx, a, initialSnapshot, false); err != nil {
			return fail("snapshot", err)
		}
	} else if err := m.Incus.CreateSnapshot(ctx, a.Instance, initialSnapshot); err != nil {
		return fail("snapshot", err)
	}
	if err := m.Store.SetAgentStatus(ctx, a.Project, a.Name, state.AgentReady); err != nil {
		return fail("state", err)
	}
	a.Status = state.AgentReady
	m.EnsureImageCache(ctx, a)
	m.EnsureDockerOnDemand(ctx, a)
	m.EnsureBrowser(ctx, a)
	m.EnsureNesting(ctx, a, p)
	return a, nil
}

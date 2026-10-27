package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agentbox/internal/gateway"
	"agentbox/internal/machine"
	"agentbox/internal/state"
)

// podLifecycle is what a pod's machine asks of the daemon: start, stop and
// destroy change what the reconciler works towards, and Info reads back what it
// last saw. Nothing here talks to a cluster, because the pod itself is only
// reachable through the gateway and the cluster is the reconciler's.
type podLifecycle struct{ store *state.Store }

func (p podLifecycle) Start(ctx context.Context, ref gateway.Ref) error {
	return p.store.SetAgentDesiredState(ctx, ref.Project, ref.Agent, state.DesiredRunning)
}

func (p podLifecycle) Stop(ctx context.Context, ref gateway.Ref) error {
	return p.store.SetAgentDesiredState(ctx, ref.Project, ref.Agent, state.DesiredStopped)
}

// Destroy starts the retention clock, which is what stops and then deletes the
// pod: the claim outlives it for the window the user configured.
func (p podLifecycle) Destroy(ctx context.Context, ref gateway.Ref) error {
	return p.store.MarkAgentDestroyed(ctx, ref.Project, ref.Agent, time.Now())
}

func (p podLifecycle) Info(ctx context.Context, ref gateway.Ref) (machine.Info, error) {
	a, err := p.store.Agent(ctx, ref.Project, ref.Agent)
	if err != nil {
		return machine.Info{}, err
	}
	return podInfo(a), nil
}

// podInfo is what the agent's row says about its machine. It is running when
// the user asked for that and the reconciler has adopted a pod with nothing
// wrong with it; a pod still being made counts as stopped, so the manager's
// guards do not let a chat or a terminal at one that isn't there yet.
func podInfo(a state.Agent) machine.Info {
	info := machine.Info{Ref: a.Pod, State: "Stopped"}
	if info.Ref == "" {
		info.Ref = a.Ref()
	}
	if a.DesiredState == state.DesiredRunning && a.Pod != "" && a.StatusDetail == "" {
		info.State = "Running"
	}
	return info
}

// destroyMachine deletes a cluster agent's machine for good: its pod, the
// token its agentd authenticated with and the claim its worktree was on.
// agent.Destroy calls it before the row is archived, and removing a project
// calls it for each of its cluster agents, because both take away the row the
// reconciler finds the pod by.
//
// A machine that is already gone is done, not an error, so a destroy that
// failed partway can simply be run again. The cluster itself being gone from
// this daemon's configuration is done too: there is no client left to ask, and
// the pod is no longer this machine's to delete.
func (s *Server) destroyMachine(ctx context.Context, a state.Agent) error {
	if !a.OnCluster() {
		return nil
	}
	cluster, err := s.store.Cluster(ctx, a.Cluster)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	client, err := s.clusterClient(cluster)
	if err != nil {
		return err
	}
	if err := client.DeleteAll(ctx, a.Project, a.Name); err != nil && !errors.Is(err, machine.ErrNotFound) {
		return fmt.Errorf("deleting %s's machine in cluster %s: %w", a.Ref(), a.Cluster, err)
	}
	err = s.store.RemoveAgentToken(ctx, a.Project, a.Name)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	return err
}

// destroyProjectMachines deletes the machines of a project's cluster agents
// before the project is removed. It lives in the daemon and not in the store
// because deleting a pod needs a cluster client and a kubeconfig, which the
// store does not hold; a row deleted first would leave a pod that nothing
// names.
func (s *Server) destroyProjectMachines(ctx context.Context, project string) error {
	agents, err := s.store.Agents(ctx, project)
	if err != nil {
		return err
	}
	for _, a := range agents {
		if !a.OnCluster() {
			continue
		}
		if err := s.destroyMachine(ctx, a); err != nil {
			return fmt.Errorf("removing %s: %w", a.Ref(), err)
		}
	}
	return nil
}

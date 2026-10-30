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
// destroy change what the reconciler works towards, Info reads back what it last
// saw, and the snapshot operations archive and prepare the claim through the
// cluster. None of it goes through the pod: the pod itself is only reachable
// through the gateway, and the cluster is the reconciler's.
type podLifecycle struct{ s *Server }

func (p podLifecycle) Start(ctx context.Context, ref gateway.Ref) error {
	if err := p.s.checkAgentFree(ctx, ref, "start"); err != nil {
		return err
	}
	return p.s.store.SetAgentDesiredState(ctx, ref.Project, ref.Agent, state.DesiredRunning)
}

func (p podLifecycle) Stop(ctx context.Context, ref gateway.Ref) error {
	if err := p.s.checkAgentFree(ctx, ref, "stop"); err != nil {
		return err
	}
	return p.s.store.SetAgentDesiredState(ctx, ref.Project, ref.Agent, state.DesiredStopped)
}

// Destroy marks the row destroyed, which is what stops and then deletes the
// pod: the reconciler removes the pod, its token Secret and its claim, with no
// retention window.
func (p podLifecycle) Destroy(ctx context.Context, ref gateway.Ref) error {
	if err := p.s.checkAgentFree(ctx, ref, "destroy"); err != nil {
		return err
	}
	return p.s.store.MarkAgentDestroyed(ctx, ref.Project, ref.Agent, time.Now())
}

func (p podLifecycle) Info(ctx context.Context, ref gateway.Ref) (machine.Info, error) {
	a, err := p.s.store.Agent(ctx, ref.Project, ref.Agent)
	if err != nil {
		return machine.Info{}, err
	}
	info := podInfo(a)
	// A pod whose agentd is connected is running, whatever the row's last
	// round says: the session is what every operation uses, and the cluster
	// detail that clears it lags behind by up to a reconciler round. A create
	// returns when its session is up, so without this a just-created agent
	// reads as stopped for a few seconds and its first exec or shell refuses.
	if info.State != "Running" && a.DesiredState == state.DesiredRunning && p.s.gateway != nil && p.s.gateway.Connected(ref) {
		info.State = "Running"
	}
	return info, nil
}

// Recreate deletes the agent's pod and asks for a new one on the same claim. The
// claim is the machine, so its files stay; the pod is a use of it, and the new
// one runs the image the cluster points at now. The row's pinned image goes with
// the pod, which is what makes this a recreate and not a restart: a restore pins
// the image its snapshot was taken with, and recreating is how an agent picks up
// the cluster's current one.
//
// The old pod is waited for before the new one is asked for, so a reconciler
// round that lands between the two can never adopt the pod this is replacing.
func (p podLifecycle) Recreate(ctx context.Context, ref gateway.Ref) error {
	if err := p.s.checkAgentFree(ctx, ref, "recreate"); err != nil {
		return err
	}
	a, err := p.s.store.Agent(ctx, ref.Project, ref.Agent)
	if err != nil {
		return err
	}
	if !a.OnCluster() {
		return fmt.Errorf("%s runs on this host, where recreating a machine is the manager's job, not its pod's", a.Ref())
	}
	cluster, err := p.s.store.Cluster(ctx, a.Cluster)
	if err != nil {
		return err
	}
	client, err := p.s.clusterClient(cluster)
	if err != nil {
		return err
	}
	// Stopped first, so a reconciler round that lands between the delete and the
	// image change cannot make a pod at the image this is replacing.
	if err := p.s.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredStopped); err != nil {
		return err
	}
	if err := client.Delete(ctx, a.Project, a.Name); err != nil {
		return err
	}
	if err := waitPodGone(ctx, client, a, "the pod has to go before a new one can be made"); err != nil {
		return err
	}
	// The row names the pod that just went, and a token is good for exactly the
	// pod it was minted for: the reconciler mints a new one with the new pod.
	_ = p.s.store.RemoveAgentToken(ctx, a.Project, a.Name)
	p.s.dropGatewaySession(a.Project, a.Name)
	if a.Pod != "" || a.PodUID != "" || a.StatusDetail != "" {
		if err := p.s.store.SetAgentPod(ctx, a.Project, a.Name, "", "", ""); err != nil {
			return err
		}
	}
	// The claim's name is the one the row already names, which is "" for an
	// agent that never moved: Start's spec derives the same name again.
	if err := p.s.store.SetAgentClaim(ctx, a.Project, a.Name, a.Claim, ""); err != nil {
		return err
	}
	return p.s.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredRunning)
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
	if err := client.DeleteAll(ctx, a.Project, a.Name, a.Claim); err != nil && !errors.Is(err, machine.ErrNotFound) {
		return fmt.Errorf("deleting %s's machine in cluster %s: %w", a.Ref(), a.Cluster, err)
	}
	err = s.store.RemoveAgentToken(ctx, a.Project, a.Name)
	s.dropGatewaySession(a.Project, a.Name)
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

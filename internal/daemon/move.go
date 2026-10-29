package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/gateway"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// A move takes an agent's machine from one cluster to another: its worktree is
// checkpointed, its claim archived and restored into a new one on the target,
// and its row moved onto that claim, where the reconciler makes the target pod.
//
// A move is done when the target pod answers and Start has written the agent
// back into it, which is the same proof a create and a recreate use. The plan
// had the source deleted after the target's first successful chat, and this does
// not: two live pods on one branch for an unbounded time, with only one row able
// to name it, is worse than the guarantee it buys. The source claim is deleted
// as soon as the target is up, and the archive is the machine.
//
// This file is also what `cluster drain --move-to` runs (moveAgentRun), which is
// why the target is resolved by the caller: admission and placement belong to
// the command that asked, and the drain checks its target before it marks the
// cluster.

// moveReadyTimeout bounds the wait for the target pod's agentd to connect. It is
// a pod's create timeout (agent's podReadyTimeout), repeated here because that
// one is the manager's.
const moveReadyTimeout = 5 * time.Minute

// moveHelperTag names the helper pods that carry a claim from one cluster to the
// other. They run in different clusters, so one tag serves both.
const moveHelperTag = "move"

// What a move asks of a cluster and a pod, behind variables so a test can assert
// the orchestration without a cluster whose API server can exec. A fake
// clientset has no REST config, so the real archive stops at the helper pod's
// exec (internal/k8s/archive_test.go), and a real Start writes the agent's env,
// git config and tmux session into a pod a fake cluster does not have.
var (
	// moveCheckpoint records the worktree before the machine goes.
	moveCheckpoint = func(ctx context.Context, log io.Writer, s *Server, a state.Agent, target string) (string, error) {
		return s.manager(log).CheckpointForMove(ctx, a, target)
	}
	// moveArchive streams a claim out of one cluster, and moveRestore unpacks it
	// into another.
	moveArchive = (*k8s.Cluster).ArchiveClaim
	moveRestore = (*k8s.Cluster).RestoreClaim
	// moveAdopt waits for the pod the target makes from the row, then starts the
	// agent on it. The explicit wait names the timeout the move keeps; Start's
	// own readiness wait is the same gateway check a create and a recreate make.
	moveAdopt = func(ctx context.Context, log io.Writer, s *Server, a state.Agent) error {
		ref := gateway.Ref{Project: a.Project, Agent: a.Name}
		if err := s.gateway.WaitFor(ctx, ref, moveReadyTimeout); err != nil {
			return fmt.Errorf("its pod in the target cluster never answered: %w", err)
		}
		row, err := s.store.Agent(ctx, a.Project, a.Name)
		if err != nil {
			return err
		}
		_, err = s.manager(log).Start(ctx, row)
		return err
	}
	// moveRestart asks for the source pod again after a move that failed.
	moveRestart = func(ctx context.Context, s *Server, a state.Agent) error {
		return (podLifecycle{s: s}).restart(ctx, a)
	}
)

// moveResult is what a move job answers with: where the agent's machine is now.
// The claim is here rather than in api.Agent because no other route needed one.
type moveResult struct {
	Cluster string `json:"cluster"`
	Claim   string `json:"claim"`
}

// moveAgent is POST /v1/agents/{project}/{agent}/move: an agent's machine from
// one cluster to another.
func (s *Server) moveAgent(w http.ResponseWriter, r *http.Request) error {
	a, err := s.agentFromPath(r)
	if err != nil {
		return err
	}
	var req api.MoveAgentRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	target, err := s.moveTarget(r.Context(), a, strings.TrimSpace(req.Cluster))
	if err != nil {
		return err
	}
	return s.startAgentJob(r.Context(), w, "move", a, func(ctx context.Context, log io.Writer) (any, error) {
		if err := s.moveAgentRun(ctx, log, a, target); err != nil {
			return nil, err
		}
		row, err := s.store.Agent(ctx, a.Project, a.Name)
		if err != nil {
			return nil, err
		}
		s.refreshAgents(ctx)
		return moveResult{Cluster: row.Cluster, Claim: row.Claim}, nil
	})
}

// moveTarget is the cluster a move was asked for, and everything it refuses
// before a job starts: an agent that isn't on a cluster, a target that is this
// host or the cluster it is already in, and a missing target. "auto" goes
// through placement, which is where a full or draining cluster is refused.
func (s *Server) moveTarget(ctx context.Context, a state.Agent, requested string) (string, error) {
	if !a.OnCluster() {
		if a.Status == state.AgentQueued {
			return "", fmt.Errorf("%s is waiting for room in its project's queue, not running in a cluster: there is no machine to move yet", a.Ref())
		}
		return "", fmt.Errorf("%s runs on this host, where its machine is an Incus instance: a move takes a pod from one cluster to another", a.Ref())
	}
	if a.DesiredState == state.DesiredDestroyed {
		return "", fmt.Errorf("%s is being destroyed: there is no machine to move", a.Ref())
	}
	if requested == "" {
		return "", fmt.Errorf("a move needs a target cluster: agentbox move %s --cluster <name>, or --cluster auto for the one with the most room", a.Ref())
	}
	if requested == state.ClusterLocal {
		return "", fmt.Errorf("a move is from one cluster's pod to another's, and %q is this host, where a machine is an Incus instance: %s runs in cluster %s", state.ClusterLocal, a.Ref(), a.Cluster)
	}
	if requested == a.Cluster {
		return "", fmt.Errorf("%s already runs in cluster %s: name another cluster, or auto for the one with the most room", a.Ref(), a.Cluster)
	}
	project, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return "", err
	}
	target, err := s.placeAgent(ctx, project, requested)
	if err != nil {
		return "", err
	}
	if target == "" || target == a.Cluster {
		return "", fmt.Errorf("%s already runs in cluster %s, which is where placement put it: name the cluster to move it to", a.Ref(), a.Cluster)
	}
	return target, nil
}

// moveAgentRun moves one agent's machine to the cluster target, and is what both
// the endpoint and `cluster drain --move-to` run. It is in this file because a
// move is the one operation both of them share.
//
// The row is re-read: a drain hands over rows it read before it started, and the
// machine it describes is the one this moves.
func (s *Server) moveAgentRun(ctx context.Context, log io.Writer, a state.Agent, target string) error {
	// The endpoint's job already holds the agent; a drain's takes it here.
	ctx, release, err := s.holdAgent(ctx, a, "move")
	if err != nil {
		return err
	}
	defer release()
	row, err := s.store.Agent(ctx, a.Project, a.Name)
	if err != nil {
		return err
	}
	a = row
	if !a.OnCluster() {
		return fmt.Errorf("%s runs on this host, not in a cluster: there is no pod to move", a.Ref())
	}
	if a.DesiredState == state.DesiredDestroyed {
		return fmt.Errorf("%s is being destroyed: there is no machine to move", a.Ref())
	}
	if target == "" || target == state.ClusterLocal {
		return fmt.Errorf("moving %s needs a cluster to move it to, not %q", a.Ref(), target)
	}
	if target == a.Cluster {
		return fmt.Errorf("%s already runs in cluster %s: name another cluster", a.Ref(), target)
	}

	source, sourceReg, sourceClient, err := s.moveSource(ctx, a)
	if err != nil {
		return err
	}
	targetCluster, err := s.store.Cluster(ctx, target)
	if errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("no cluster named %q: add it with agentbox cluster add %s, or list the ones there are with agentbox cluster list", target, target)
	}
	if err != nil {
		return err
	}
	targetReg, err := s.registryFor(ctx, targetCluster.Registry)
	if err != nil {
		return err
	}
	targetClient, err := s.clusterClient(targetCluster)
	if err != nil {
		return err
	}

	// The target has to be able to run this agent before the source is stopped:
	// the same refusals a create gets, and then the slot is taken out of the
	// daemon's count so a burst of moves doesn't all start into the last one.
	// The endpoint already sent a named target through placement, and a drain
	// checks its own; this catches a cluster that filled between the two, and
	// is what a drain's target would otherwise never ask.
	project, err := s.store.Project(ctx, a.Project)
	if err != nil {
		return err
	}
	if _, _, err := s.admit(ctx, project, targetCluster); err != nil {
		return err
	}
	s.reserve(targetCluster)

	// The chat goes before the checkpoint: a turn that lands between the two
	// would write files the archive has to catch up with.
	s.chat.Stop(a.Ref(), "its machine is moving to cluster "+target)

	sourceSpec := s.podSpec(ctx, source, sourceReg, a)
	image, _, err := podLifecycle{s: s}.agentImage(ctx, a, sourceClient)
	if err != nil {
		return err
	}
	if image != "" {
		sourceSpec.Image = image
	}
	oldClaim := sourceSpec.WorkspaceClaim()
	newClaim := nextClaimName(oldClaim)
	// The target's own image, not the source's pin: a pin names an image the
	// target may not be able to pull, and the row is cleared of it below so the
	// reconciler builds the same thing.
	targetAgent := a
	targetAgent.Image = ""
	targetSpec := s.podSpec(ctx, targetCluster, targetReg, targetAgent)
	targetSpec.Claim = newClaim

	desired := a.DesiredState
	if desired == "" {
		desired = state.DesiredRunning
	}
	// A pod that isn't running has no gateway session to checkpoint through,
	// and its worktree is on the claim the archive carries either way.
	running := podInfo(a).Running()

	// Everything before the row names the target: a failure here leaves the
	// source where it was, and the claim the move made on the target is not
	// kept. The source's own claim was only read.
	abort := func(cause error) error {
		cleanup := context.WithoutCancel(ctx)
		if err := targetClient.DeleteClaim(cleanup, newClaim); err != nil {
			_, _ = fmt.Fprintf(log, "the claim %s the move made in cluster %s is still there: %v\n", newClaim, target, err)
		}
		s.restoreSource(cleanup, log, a, desired)
		return cause
	}

	if running {
		commit, err := moveCheckpoint(ctx, log, s, a, target)
		if err != nil {
			return abort(fmt.Errorf("checkpointing %s before moving it to cluster %s: %w", a.Ref(), target, err))
		}
		_, _ = fmt.Fprintf(log, "checkpointed %s at %s\n", a.Ref(), commit)
	} else {
		_, _ = fmt.Fprintf(log, "%s has no running pod, so its worktree is only in the claim the archive carries\n", a.Ref())
	}

	// The claim is ReadWriteOnce: the pod has to be gone before a helper can
	// mount it, and stopped in the row so the reconciler doesn't make another.
	if err := s.store.SetAgentDesiredState(ctx, a.Project, a.Name, state.DesiredStopped); err != nil {
		return abort(err)
	}
	if err := waitPodGone(ctx, sourceClient, a, "its claim has to be archived before its machine moves"); err != nil {
		return abort(fmt.Errorf("stopping %s on cluster %s: %w", a.Ref(), source.Name, err))
	}
	// A session the deleted pod left behind would make the gateway answer for
	// the old machine, not the new one.
	s.awaitSessionGone(ctx, a, 30*time.Second)

	archived, err := transferClaim(ctx, sourceClient, sourceSpec, targetClient, targetSpec)
	if err != nil {
		return abort(fmt.Errorf("moving %s's workspace from cluster %s to %s: %w", a.Ref(), source.Name, target, err))
	}
	_, _ = fmt.Fprintf(log, "archived %s's claim %s: %d bytes\n", a.Ref(), oldClaim, archived)
	_, _ = fmt.Fprintf(log, "restored it as claim %s in cluster %s\n", newClaim, target)

	// The row, in one statement: after this the agent's machine is the target's,
	// the pod it named is the source's, and the image is whatever the target
	// runs rather than the source's pin.
	if err := s.store.MoveAgent(ctx, a.Project, a.Name, state.MovePlacement{
		Cluster: target, Workspace: a.Workspace, Claim: newClaim, Desired: state.DesiredRunning,
	}); err != nil {
		return abort(fmt.Errorf("recording where %s's machine is: %w", a.Ref(), err))
	}

	if err := moveAdopt(ctx, log, s, a); err != nil {
		return s.rollBackMove(ctx, log, a, source, oldClaim, targetClient, newClaim, desired,
			fmt.Errorf("cluster %s did not take %s: %w", target, a.Ref(), err))
	}
	_, _ = fmt.Fprintf(log, "adopted %s's pod in cluster %s\n", a.Ref(), target)

	// The target is the machine now. What the source still holds is a leftover
	// no row names any more, so a failure here is reported for a human rather
	// than failing a move that already worked.
	cleanup := context.WithoutCancel(ctx)
	if err := sourceClient.DeleteAll(cleanup, a.Project, a.Name, oldClaim); err != nil {
		_, _ = fmt.Fprintf(log, "cluster %s still has %s's pod, token or claim %s: %v (remove them by hand)\n", source.Name, a.Ref(), oldClaim, err)
	} else {
		_, _ = fmt.Fprintf(log, "removed %s's pod and claim %s from cluster %s\n", a.Ref(), oldClaim, source.Name)
	}
	return nil
}

// moveSource reads the agent's cluster and the client to ask it with, which a
// move needs before it can archive anything.
func (s *Server) moveSource(ctx context.Context, a state.Agent) (state.Cluster, *state.Registry, *k8s.Cluster, error) {
	cluster, err := s.store.Cluster(ctx, a.Cluster)
	if err != nil {
		return state.Cluster{}, nil, nil, fmt.Errorf("reading cluster %s, where %s runs: %w", a.Cluster, a.Ref(), err)
	}
	reg, err := s.registryFor(ctx, cluster.Registry)
	if err != nil {
		return state.Cluster{}, nil, nil, err
	}
	client, err := s.clusterClient(cluster)
	if err != nil {
		return state.Cluster{}, nil, nil, err
	}
	return cluster, reg, client, nil
}

// transferClaim streams the claim from one cluster into a new claim on another
// through a pipe: nothing of it lands on this host, and a target that stops
// reading stops the source writing. The count is what the move logs.
func transferClaim(ctx context.Context, source *k8s.Cluster, sourceSpec k8s.Spec, target *k8s.Cluster, targetSpec k8s.Spec) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pr, pw := io.Pipe()
	counter := new(countingWriter)
	archiveErr := make(chan error, 1)
	go func() {
		err := moveArchive(source, ctx, sourceSpec, sourceSpec.Image, moveHelperTag, io.MultiWriter(pw, counter))
		if err != nil {
			_ = pw.CloseWithError(err)
		} else {
			_ = pw.Close()
		}
		archiveErr <- err
	}()

	restoreErr := moveRestore(target, ctx, targetSpec, targetSpec.Image, moveHelperTag, pr)
	if restoreErr != nil {
		// Unblock the archive, whose helper may still be writing, before
		// waiting for it below.
		_ = pr.CloseWithError(restoreErr)
	}
	archiveFailed := <-archiveErr
	if restoreErr != nil {
		// The target's reason is the one the caller can act on; the archive's
		// is the same failure at the other end of the pipe.
		return 0, restoreErr
	}
	if archiveFailed != nil {
		return 0, archiveFailed
	}
	_ = pr.Close()
	return counter.n, nil
}

// countingWriter counts what passes through it, for the size a move logs.
type countingWriter struct{ n int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// awaitSessionGone waits for the gateway to forget the source pod's session, so
// the target's connection is the one the move waits for. A session that lingers
// is stale and the target's connection replaces it, so the wait is bounded
// rather than fatal.
func (s *Server) awaitSessionGone(ctx context.Context, a state.Agent, timeout time.Duration) {
	if s.gateway == nil {
		return
	}
	ref := gateway.Ref{Project: a.Project, Agent: a.Name}
	deadline := time.Now().Add(timeout)
	for s.gateway.Connected(ref) {
		if time.Now().After(deadline) {
			s.logf("moving %s: its source pod's gateway session is still there; the target's replaces it", a.Ref())
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// restoreSource puts the source pod back after a move that failed before the row
// named the target. The row's placement never changed; only what it asked the
// machine to do did, while the claim was being archived.
func (s *Server) restoreSource(ctx context.Context, log io.Writer, a state.Agent, desired string) {
	if desired == state.DesiredRunning {
		if err := moveRestart(ctx, s, a); err != nil {
			_, _ = fmt.Fprintf(log, "starting %s again in cluster %s after the failed move: %v\n", a.Ref(), a.Cluster, err)
		}
		return
	}
	if err := s.store.SetAgentDesiredState(ctx, a.Project, a.Name, desired); err != nil {
		_, _ = fmt.Fprintf(log, "putting %s back to %s after the failed move: %v\n", a.Ref(), desired, err)
	}
}

// rollBackMove puts a move that got as far as the row back: the target pod and
// claim go, the row names the source again with what it ran there, and the
// source pod comes back when it was running. A move that fails at the target
// therefore leaves the agent as it was, rather than a row naming a machine that
// never answered.
func (s *Server) rollBackMove(ctx context.Context, log io.Writer, a state.Agent, source state.Cluster, oldClaim string, targetClient *k8s.Cluster, newClaim string, desired string, cause error) error {
	cleanup := context.WithoutCancel(ctx)
	// The target is asked for nothing first, so no reconciler round makes a new
	// target pod while this takes the old one away. The row keeps naming the
	// target until that pod is gone, which is what stops the source's pod from
	// starting beside it.
	if err := s.store.SetAgentDesiredState(cleanup, a.Project, a.Name, state.DesiredStopped); err != nil {
		return fmt.Errorf("%w, and stopping %s in cluster %s before the rollback failed: %v", cause, a.Ref(), targetClient.Name(), err)
	}
	gone := true
	if err := targetClient.Delete(cleanup, a.Project, a.Name); err != nil {
		gone = false
		_, _ = fmt.Fprintf(log, "deleting %s's pod in cluster %s during the rollback: %v\n", a.Ref(), targetClient.Name(), err)
	} else if err := waitPodGone(cleanup, targetClient, a, "the rollback has to stop the target pod before the source one starts"); err != nil {
		gone = false
		_, _ = fmt.Fprintf(log, "waiting for %s's pod in cluster %s to go during the rollback: %v\n", a.Ref(), targetClient.Name(), err)
	}
	if err := targetClient.DeleteClaim(cleanup, newClaim); err != nil {
		_, _ = fmt.Fprintf(log, "deleting the claim %s during the rollback: %v\n", newClaim, err)
	}
	// The row goes back asking for nothing: the source pod is only asked for
	// once the target's is confirmed gone, so one agent never has two machines
	// up at the same time.
	if err := s.store.MoveAgent(cleanup, a.Project, a.Name, state.MovePlacement{
		Cluster: source.Name, Workspace: a.Workspace, Claim: a.Claim, Image: a.Image, Desired: state.DesiredStopped,
	}); err != nil {
		return fmt.Errorf("%w, and putting %s back in cluster %s failed: %v", cause, a.Ref(), source.Name, err)
	}
	if !gone {
		return fmt.Errorf("%w (its row is back in cluster %s on claim %s, but the target pod may still be there, so it is left stopped: remove it, then start the agent)", cause, source.Name, oldClaim)
	}
	if desired != state.DesiredRunning {
		return fmt.Errorf("%w (it is back in cluster %s on claim %s)", cause, source.Name, oldClaim)
	}
	if err := moveRestart(cleanup, s, a); err != nil {
		return fmt.Errorf("%w (its row is back in cluster %s on claim %s, and starting it again failed: %v)", cause, source.Name, oldClaim, err)
	}
	return fmt.Errorf("%w (it is back in cluster %s on claim %s)", cause, source.Name, oldClaim)
}

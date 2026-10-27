package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Draining a cluster is how it stops taking new agents. The cluster is marked
// first, so admission refuses it from that moment, and then the agents it has
// are stopped or moved. Marking before the work is what makes an interrupted
// drain safe: the cluster stays draining, and running the command again picks up
// the agents that are left.

// drainCluster is POST /v1/clusters/{name}/drain: stop a cluster taking new
// agents, and move or stop the ones it has.
func (s *Server) drainCluster(w http.ResponseWriter, r *http.Request) error {
	var req api.DrainClusterRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	req.MoveTo = strings.TrimSpace(req.MoveTo)
	if req.MoveTo != "" && req.KeepRunning {
		return errors.New("a drain can't move its agents and keep them running: pass one, or neither to stop every agent on the cluster")
	}
	ctx := r.Context()
	name := r.PathValue("name")
	cluster, err := s.store.Cluster(ctx, name)
	if err != nil {
		return err
	}
	// A target that can't take them is refused before anything is marked or
	// stopped: a drain that fails halfway leaves the cluster draining.
	if req.MoveTo != "" {
		if err := s.checkDrainTarget(ctx, cluster.Name, req.MoveTo); err != nil {
			return err
		}
	}
	return s.startJob(w, "drain", name, func(ctx context.Context, log io.Writer) (any, error) {
		return s.runDrain(ctx, log, cluster.Name, req)
	})
}

// checkDrainTarget is the cluster a drain was told to move the agents to: one
// that exists, isn't the cluster being drained, and isn't itself on its way out.
func (s *Server) checkDrainTarget(ctx context.Context, from, to string) error {
	if to == from {
		return fmt.Errorf("cluster %s is the one being drained: move its agents to another cluster", from)
	}
	if to == state.ClusterAuto || to == state.ClusterLocal {
		return fmt.Errorf("a drain moves its agents to a cluster by name, not %q: list the ones there are with agentbox cluster list", to)
	}
	target, err := s.store.Cluster(ctx, to)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("no cluster named %q: list the ones there are with agentbox cluster list", to)
		}
		return err
	}
	if target.Draining {
		return fmt.Errorf("cluster %s is draining: it takes no new agents", target.Name)
	}
	return nil
}

// runDrain is the job: mark the cluster, then stop or move every agent it has,
// one at a time. A failure leaves the cluster draining and the agents it hadn't
// reached as they were.
func (s *Server) runDrain(ctx context.Context, log io.Writer, name string, req api.DrainClusterRequest) (any, error) {
	if err := s.store.SetClusterDraining(ctx, name, true); err != nil {
		return nil, err
	}
	// The room a draining cluster had is not room for anything now, and a
	// stale number would let placement pick it between this write and the next
	// capacity round.
	s.forgetClusterCapacity(name)
	_, _ = fmt.Fprintf(log, "%s is draining: it takes no new agents.\n", name)

	agents, err := s.store.AgentsOnCluster(ctx, name)
	if err != nil {
		return nil, err
	}
	switch {
	case req.KeepRunning:
		for _, a := range agents {
			if a.IsLead() {
				continue
			}
			_, _ = fmt.Fprintf(log, "kept %s running\n", a.Ref())
		}
	case req.MoveTo != "":
		for _, a := range agents {
			if a.IsLead() {
				continue
			}
			switch {
			case a.DesiredState == state.DesiredDestroyed:
				_, _ = fmt.Fprintf(log, "skipped %s: it is being destroyed\n", a.Ref())
				continue
			case !wantsRunning(a) || a.Pod == "":
				// A stopped agent has no machine to move, and a claim stays
				// on its cluster until the agent is started somewhere else.
				_, _ = fmt.Fprintf(log, "skipped %s: it has no machine\n", a.Ref())
				continue
			}
			if err := s.moveAgentRun(ctx, log, a, req.MoveTo); err != nil {
				return nil, fmt.Errorf("moving %s to %s: %w", a.Ref(), req.MoveTo, err)
			}
			_, _ = fmt.Fprintf(log, "moved %s to %s\n", a.Ref(), req.MoveTo)
		}
		// The rows on this cluster changed under the daemon's view: a moved
		// agent's next start is in another cluster.
		s.refreshAgents(ctx)
	default:
		m := s.manager(log)
		for _, a := range agents {
			if a.IsLead() {
				continue
			}
			if !wantsRunning(a) {
				_, _ = fmt.Fprintf(log, "skipped %s: it is not running\n", a.Ref())
				continue
			}
			s.chat.Stop(a.Ref(), "the cluster it runs in was drained")
			if err := s.stopAgent(ctx, m, a); err != nil {
				return nil, fmt.Errorf("stopping %s: %w", a.Ref(), err)
			}
			_, _ = fmt.Fprintf(log, "stopped %s\n", a.Ref())
		}
		s.refreshAgents(ctx)
	}
	cluster, err := s.store.Cluster(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.clusterInfo(cluster), nil
}

// wantsRunning is what an agent's row asks its machine to do, with "" meaning
// running: the state an agent made before desired_state existed has.
func wantsRunning(a state.Agent) bool {
	return a.DesiredState == "" || a.DesiredState == state.DesiredRunning
}

// undrainCluster is POST /v1/clusters/{name}/undrain: let it take them again.
func (s *Server) undrainCluster(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	name := r.PathValue("name")
	cluster, err := s.store.Cluster(ctx, name)
	if err != nil {
		return err
	}
	if err := s.store.SetClusterDraining(ctx, name, false); err != nil {
		return err
	}
	// What it had is not what it has now: measure it again rather than let
	// placement choose it on the room it had before the drain.
	s.forgetClusterCapacity(name)
	cluster.Draining = false
	return writeJSON(w, http.StatusOK, s.clusterInfo(cluster))
}

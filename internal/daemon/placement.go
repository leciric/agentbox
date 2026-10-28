package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Where an agent's machine runs. A create names a cluster, or asks for one with
// state.ClusterAuto, or names nothing and follows what its project chose. The
// daemon turns all of that into one cluster name before it starts the job, so
// the job and the agent's row deal in something concrete.
//
// Placement is admission as well as choice: a cluster that is full is not a
// candidate, and one the caller named is refused with what to do about it
// rather than given a pod the cluster's scheduler will leave Pending. "Full"
// means the daemon's own count (max_agents, the cluster's CPU and memory
// budget, the nodes' room, divided by what one pod asks for), which is the
// number `agentbox cluster list` shows as FREE.

// capacityFresh is how old a cluster's room may be and still decide admission.
// The capacity loop refreshes every cluster every capacityEvery, so this is
// only reached when that loop is stuck; a create then measures the cluster
// itself rather than trusting a number nothing has confirmed.
const capacityFresh = 2 * time.Minute

// placeTimeout bounds the capacity read placement does when the daemon's own
// number is missing or stale: a create should fail with a reason in seconds,
// not wait out the capacity loop's own 15-second budget.
const placeTimeout = 5 * time.Second

// NoRoomError is a cluster with no room for one more agent: the reason says how
// full it is, and that queueing is the way to wait for it. It is a type so a
// caller that asked to queue can tell this from a cluster that is broken.
type NoRoomError struct {
	Reason string
}

func (e *NoRoomError) Error() string { return e.Reason }

// placeAgent decides which cluster an agent of p runs in, from what the request
// named: "" follows the project's own choice, state.ClusterAuto asks for the
// cluster with the most room, and any other value is that cluster, by name. ""
// comes back for this host, which is an Incus instance, and never for a control
// plane, which has none: there a project or a request that names nothing is
// placed automatically.
//
// A refused cluster is an error before anything is made. An Auto request that
// nothing can take is a *NoRoomError, like a full cluster named outright, so a
// create that asked to wait can queue instead of failing.
func (s *Server) placeAgent(ctx context.Context, p state.Project, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == state.ClusterLocal {
		return "", nil
	}
	if requested == "" {
		requested = p.Cluster
	}
	if requested == "" {
		// "This host" is the answer here, except for a control plane: it has no
		// Incus, so a machine it made itself can't exist, and a create that
		// names nothing has to land on a cluster (runsOnlyInClusters). A store
		// that can't answer leaves the create on this host, which is what it
		// asked for and what it did before any of this was asked.
		if only, err := s.runsOnlyInClusters(ctx); err != nil || !only {
			return "", nil
		}
		requested = state.ClusterAuto
	}
	if requested != state.ClusterAuto {
		cluster, err := s.store.Cluster(ctx, requested)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return "", fmt.Errorf("no cluster named %q: add it with agentbox cluster add %s, or list the ones there are with agentbox cluster list", requested, requested)
			}
			return "", err
		}
		if _, _, err := s.admit(ctx, p, cluster); err != nil {
			return "", err
		}
		return cluster.Name, nil
	}
	return s.autoPlace(ctx, p)
}

// autoPlace picks the cluster with the most room that can run an agent of p.
// Ties go to the project's own default cluster, when that is one of the
// candidates, and then to the name, so the same installation places the same
// agent in the same place twice.
func (s *Server) autoPlace(ctx context.Context, p state.Project) (string, error) {
	clusters, err := s.store.Clusters(ctx)
	if err != nil {
		return "", err
	}
	if len(clusters) == 0 {
		return "", fmt.Errorf("%s runs its agents on a cluster automatically, but there are none: add one with agentbox cluster add, or run them on this machine with agentbox project cluster %s local", p.Name, p.Name)
	}
	var candidates []candidate
	var refused []string
	for _, cluster := range clusters {
		room, known, err := s.admit(ctx, p, cluster)
		if err != nil {
			var noRoom *NoRoomError
			if errors.As(err, &noRoom) {
				refused = append(refused, fmt.Sprintf("%s (%s)", cluster.Name, noRoom.Reason))
			} else {
				refused = append(refused, fmt.Sprintf("%s (%v)", cluster.Name, err))
			}
			continue
		}
		if !known {
			refused = append(refused, fmt.Sprintf("%s (its room couldn't be read)", cluster.Name))
			continue
		}
		candidates = append(candidates, candidate{cluster: cluster, room: room})
	}
	if len(candidates) == 0 {
		return "", &NoRoomError{Reason: fmt.Sprintf("no cluster has room for an agent of %s: %s", p.Name, strings.Join(refused, "; "))}
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.betterThan(best, p.Cluster) {
			best = c
		}
	}
	return best.cluster.Name, nil
}

// candidate is one cluster autoPlace may choose, with what it has free.
type candidate struct {
	cluster state.Cluster
	room    api.ClusterCapacity
}

// betterThan is the order autoPlace picks by: the most agents of room, then the
// most free CPU, then the most free memory, then the project's own cluster, and
// then the name. A cluster with no ceiling it can count (room.Agents is -1) is
// ranked by what it has free like any other, and reads as the most room only
// when nothing else has a count.
func (c candidate) betterThan(other candidate, preferred string) bool {
	if c.agents() != other.agents() {
		return c.agents() > other.agents()
	}
	if cpu, otherCPU := quantity(c.room.CPU), quantity(other.room.CPU); cpu.Cmp(otherCPU) != 0 {
		return cpu.Cmp(otherCPU) > 0
	}
	if mem, otherMem := quantity(c.room.Memory), quantity(other.room.Memory); mem.Cmp(otherMem) != 0 {
		return mem.Cmp(otherMem) > 0
	}
	if (c.cluster.Name == preferred) != (other.cluster.Name == preferred) {
		return c.cluster.Name == preferred
	}
	return c.cluster.Name < other.cluster.Name
}

// agents is the room a candidate has, with -1 (no ceiling to count) ranked
// above every count: a cluster the daemon cannot fill up on its own is a
// cluster it has no reason to refuse.
func (c candidate) agents() int {
	if c.room.Agents < 0 {
		return int(^uint(0) >> 1)
	}
	return c.room.Agents
}

// quantity parses a capacity string, and reads one that isn't a quantity as
// nothing: the daemon wrote it, so this is a bug rather than input.
func quantity(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return resource.Quantity{}
	}
	return q
}

// admit refuses a cluster that can't take an agent of p now: one that is
// draining, one that is full, one whose registry can't serve the image the
// project's agents run. known says whether the daemon could read its room at
// all; when it couldn't, a cluster named outright may still take the pod and the
// pod is what says why it didn't, while auto placement has no number to choose
// on.
func (s *Server) admit(ctx context.Context, p state.Project, cluster state.Cluster) (api.ClusterCapacity, bool, error) {
	// A draining cluster is on its way out: whatever room it has is for the
	// agents already there, not for another one.
	if cluster.Draining {
		return api.ClusterCapacity{}, false, &NoRoomError{Reason: fmt.Sprintf("cluster %s is draining: it takes no new agents", cluster.Name)}
	}
	// What the reconciler would refuse the pod for, refused here instead: a
	// create that fails with the command that fixes the cluster beats an
	// agent that waits for a pod nothing can start.
	if cluster.Image == "" {
		return api.ClusterCapacity{}, false, fmt.Errorf("cluster %s has no image: set one with agentbox cluster set %s --image <name>:<tag>", cluster.Name, cluster.Name)
	}
	if cluster.MasterURL == "" {
		return api.ClusterCapacity{}, false, fmt.Errorf("cluster %s has no master URL: set it with agentbox cluster set %s --master-url <url>", cluster.Name, cluster.Name)
	}
	if err := s.canPull(ctx, p, cluster); err != nil {
		return api.ClusterCapacity{}, false, err
	}
	room, known := s.roomFor(ctx, cluster)
	if !known {
		return api.ClusterCapacity{}, false, nil
	}
	if room.Agents == 0 {
		if cluster.MaxAgents > 0 && room.Running >= cluster.MaxAgents {
			return room, true, &NoRoomError{Reason: fmt.Sprintf("it is at its ceiling of %d %s", cluster.MaxAgents, plural(cluster.MaxAgents, "agent", "agents"))}
		}
		return room, true, &NoRoomError{Reason: fmt.Sprintf("its nodes have no room for one more agent, with %d %s running", room.Running, plural(room.Running, "agent", "agents"))}
	}
	return room, true, nil
}

// canPull says whether a cluster could start the image an agent of p runs: the
// project's own image when base save made one, which lives in the registry of
// the cluster that made it, or any image the cluster carries when it didn't.
func (s *Server) canPull(ctx context.Context, p state.Project, cluster state.Cluster) error {
	image, ok, err := s.store.ProjectImage(ctx, p.Name)
	if err != nil || !ok {
		return err
	}
	reg, err := s.registryFor(ctx, cluster.Registry)
	if err != nil {
		return err
	}
	if reg == nil {
		return fmt.Errorf("cluster %s pulls no registry, and %s's agents run %s: give it one with agentbox cluster set %s --registry <name>, or remove the project image with agentbox base rm %s", cluster.Name, p.Name, image.Image, cluster.Name, p.Name)
	}
	prefix := strings.TrimSuffix(reg.URL, "/") + "/"
	if !strings.HasPrefix(image.Image, prefix) {
		return fmt.Errorf("%s's agents run %s, which is in %s, and cluster %s pulls %s: save the base on an agent of %s, or remove the project image with agentbox base rm %s", p.Name, image.Image, strings.TrimSuffix(prefix, "/"), cluster.Name, reg.URL, cluster.Name, p.Name)
	}
	return nil
}

// roomFor is a cluster's room as the daemon last measured it, and measures it
// now when that number is missing, stale or the look failed. ok is false when
// even a fresh look couldn't say, which admission treats as "let the cluster
// decide".
func (s *Server) roomFor(ctx context.Context, cluster state.Cluster) (api.ClusterCapacity, bool) {
	if room, ok := s.clusterCapacity(cluster.Name); ok && room.Error == "" && time.Since(room.At) < capacityFresh {
		return room, true
	}
	client, err := s.clusterClient(cluster)
	if err != nil {
		return api.ClusterCapacity{}, false
	}
	round, cancel := context.WithTimeout(ctx, placeTimeout)
	defer cancel()
	room, err := client.Capacity(round)
	if err != nil {
		return api.ClusterCapacity{}, false
	}
	agents, err := s.store.AgentsOnCluster(ctx, cluster.Name)
	if err != nil {
		return api.ClusterCapacity{}, false
	}
	running := 0
	for _, a := range agents {
		if podInfo(a).Running() {
			running++
		}
	}
	capacity := capacityOf(cluster, room, running, time.Now().UTC())
	s.setClusterCapacity(cluster.Name, capacity)
	return capacity, true
}

// reserve takes the room one starting agent needs out of the daemon's copy of a
// cluster's capacity, so a queue of agents waiting for the last slot doesn't
// all start into it before the next capacity round sees the first one's pod.
func (s *Server) reserve(cluster state.Cluster) {
	room, ok := s.clusterCapacity(cluster.Name)
	if !ok || room.Agents <= 0 {
		return
	}
	room.Agents--
	room.Running++
	s.setClusterCapacity(cluster.Name, room)
}

// plural is "n thing" or "n things", for the reasons placement gives.

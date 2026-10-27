package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// clusterDetail is GET /v1/clusters/{name}/detail: the pods, claims, recent
// events and queued agents of one cluster, for its page.
//
// It is a snapshot, not a watch: the page asks again. A cluster the daemon
// can't reach does not fail the request: the page still shows the cluster, its
// health (LastError) and the queue, which is this daemon's own, and the lists
// that come from the cluster are empty. Recording the failure as the cluster's
// health is what makes the list say why too.
func (s *Server) clusterDetail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	name := r.PathValue("name")
	cluster, err := s.store.Cluster(ctx, name)
	if err != nil {
		return err
	}
	detail := api.ClusterDetail{Cluster: s.clusterInfo(cluster)}
	readErr := s.readCluster(ctx, cluster, &detail)
	// The queue lives here, not in the cluster, so it is worth showing even
	// when the cluster didn't answer.
	if err := s.clusterQueue(ctx, name, &detail); err != nil {
		return err
	}
	now := time.Now()
	// A look that read everything says so with the health it found; one that
	// failed says why. An error the doctor recorded survives a read that
	// doesn't check what it checked: the image, the registry, the storage
	// class. The Doctor button is what clears it.
	health := cluster.LastError
	if readErr != nil {
		health = readErr.Error()
	}
	if err := s.store.SetClusterHealth(ctx, cluster.Name, now, health); err != nil {
		return err
	}
	detail.Cluster.LastSeen = &now
	detail.Cluster.LastError = health
	return writeJSON(w, http.StatusOK, detail)
}

// readCluster fills what the cluster itself holds: its pods, its claims and its
// recent events. The first read that fails stops it: a cluster that can't list
// one of them can't list the others either, and the reason is the same.
func (s *Server) readCluster(ctx context.Context, cluster state.Cluster, detail *api.ClusterDetail) error {
	client, err := s.clusterClient(cluster)
	if err != nil {
		return err
	}
	pods, err := client.List(ctx)
	if err != nil {
		return err
	}
	detail.Pods = make([]api.ClusterPod, 0, len(pods))
	for _, pod := range pods {
		detail.Pods = append(detail.Pods, clusterPod(pod))
	}
	slices.SortStableFunc(detail.Pods, func(a, b api.ClusterPod) int { return cmp.Compare(a.Name, b.Name) })
	claims, err := client.Claims(ctx)
	if err != nil {
		return err
	}
	detail.Claims = make([]api.ClusterClaim, 0, len(claims))
	for _, claim := range claims {
		detail.Claims = append(detail.Claims, api.ClusterClaim{
			Name:      claim.Name,
			Project:   claim.Project,
			Agent:     claim.Agent,
			Storage:   claim.Storage,
			CreatedAt: claim.Created,
		})
	}
	events, err := client.Events(ctx)
	if err != nil {
		return err
	}
	detail.Events = make([]api.ClusterEvent, 0, len(events))
	for _, event := range events {
		detail.Events = append(detail.Events, api.ClusterEvent{
			Type:    event.Type,
			Reason:  event.Reason,
			Object:  event.Object,
			Message: event.Message,
			Count:   event.Count,
			At:      event.At,
		})
	}
	return nil
}

// clusterPod is one pod as the page draws it.
func clusterPod(pod k8s.Pod) api.ClusterPod {
	return api.ClusterPod{
		Name:      pod.Name,
		Project:   pod.Project,
		Agent:     pod.Agent,
		Phase:     string(pod.Phase),
		Ready:     pod.Ready,
		Node:      pod.Node,
		Image:     pod.Image,
		Restarts:  int(pod.Restarts),
		CreatedAt: pod.Created,
		Reason:    pod.Reason,
		Message:   pod.Message,
	}
}

// clusterQueue is the queued agents waiting for this cluster, sorted by their
// place in line. A request that named this cluster waits for it; one that named
// nothing follows its project, so it waits here only when the project's own
// choice is this cluster. A project (or a request) set to auto could land
// anywhere, so it is left out rather than guessed at.
func (s *Server) clusterQueue(ctx context.Context, name string, detail *api.ClusterDetail) error {
	queue, err := s.store.LeftoverQueue(ctx, "")
	if err != nil {
		return err
	}
	projectClusters := map[string]string{}
	for _, queued := range queue {
		var request queuedRequest
		if err := json.Unmarshal(queued.Request, &request); err != nil {
			// A request nothing can start from is not waiting for any cluster;
			// the queue loop drops it (daemon/queue.go).
			continue
		}
		if !s.waitsFor(ctx, request.Request.Cluster, queued.Project, name, projectClusters) {
			continue
		}
		detail.Queue = append(detail.Queue, api.ClusterQueue{
			Project:  queued.Project,
			Agent:    queued.Name,
			Position: queued.Position,
			Reason:   queued.Reason,
		})
	}
	slices.SortStableFunc(detail.Queue, func(a, b api.ClusterQueue) int { return cmp.Compare(a.Position, b.Position) })
	return nil
}

// waitsFor says whether a queued create is waiting for the cluster name. It is
// one place for the rule: the request's own cluster first, and otherwise the
// project's, with auto (either one) left out because it could be placed anywhere.
func (s *Server) waitsFor(ctx context.Context, requested, project, name string, projects map[string]string) bool {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested == name
	}
	projectCluster, seen := projects[project]
	if !seen {
		if p, err := s.store.Project(ctx, project); err == nil {
			projectCluster = p.Cluster
		}
		projects[project] = projectCluster
	}
	return projectCluster == name
}

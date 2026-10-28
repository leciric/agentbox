package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// setupBackends says where this installation runs its agents, for Setup.
// A daemon beside Incus runs them here, and may also have clusters. One in a
// cluster with no Incus on the machine at all has only its clusters: that is
// a control plane (packaging/daemon/Dockerfile), and asking it for host setup
// is asking for something it cannot have.
//
// A missing binary is what clusterOnly turns on, and only that: Incus that is
// installed and can't be used is a host that needs its setup, not one that
// never will, so it keeps blocking exactly as before.
func setupBackends(incusErr error, clusters []state.Cluster) (clusterOnly bool, backends []string) {
	clusterOnly = errors.Is(incusErr, exec.ErrNotFound) && len(clusters) > 0
	switch {
	case clusterOnly:
		return true, []string{api.SetupBackendCluster}
	case len(clusters) == 0:
		return false, []string{api.SetupBackendHost}
	default:
		return false, []string{api.SetupBackendHost, api.SetupBackendCluster}
	}
}

// runsOnlyInClusters reports whether this daemon is a control plane: no Incus
// on its machine, and at least one cluster to run agents in. Placement asks it
// before it reads "" as this host, because there is no host to have.
//
// It is one Ping per create, the same check Setup makes. When Incus answers,
// or is installed and merely broken, the answer is no without touching the
// store: a host installation is the usual one, and it stays cheap.
func (s *Server) runsOnlyInClusters(ctx context.Context) (bool, error) {
	incusErr := s.cfg.Incus.Ping(ctx)
	if !errors.Is(incusErr, exec.ErrNotFound) {
		return false, nil
	}
	clusters, err := s.store.Clusters(ctx)
	if err != nil {
		return false, err
	}
	only, _ := setupBackends(incusErr, clusters)
	return only, nil
}

// setupClusterChecks is one Setup check per cluster this installation has,
// whether or not an agent runs in it: a cluster with no project yet is still a
// place an agent can be created, and its row is what says whether it can.
func (s *Server) setupClusterChecks(ctx context.Context, clusters []state.Cluster, clusterOnly bool) ([]api.SetupCheck, error) {
	projects, err := s.store.Projects(ctx)
	if err != nil {
		return nil, err
	}
	checks := make([]api.SetupCheck, 0, len(clusters))
	for _, cluster := range clusters {
		checks = append(checks, setupClusterCheck(cluster, setupClusterRequired(cluster.Name, clusterOnly, clusters, projects)))
	}
	return checks, nil
}

// setupClusterRequired says whether a cluster blocks Setup.
//
// A control plane is its clusters, so every one of them is required: without
// them there is no machine to make at all. A project that runs its agents in a
// cluster makes that cluster required too, which is what tells the app an agent
// can't be created there.
//
// A project set to auto makes a cluster required only when it is the only one.
// Auto asks for whichever cluster can take the agent, so one that works is
// enough; requiring each of them would hold the whole installation up over a
// cluster that is merely the worse choice of two, which auto would not pick
// anyway.
func setupClusterRequired(name string, clusterOnly bool, clusters []state.Cluster, projects []state.Project) bool {
	if clusterOnly {
		return true
	}
	for _, p := range projects {
		if p.Cluster == name {
			return true
		}
		if p.Cluster == state.ClusterAuto && len(clusters) == 1 {
			return true
		}
	}
	return false
}

// setupClusterCheck is Setup's check of one cluster, worked out from its row
// and nothing else: Setup polls every five seconds, and asking each cluster
// every time would be five seconds of someone else's API on the back of a page
// that is already open. `agentbox cluster doctor` is the deep check, and what
// it found is the row's LastError.
func setupClusterCheck(c state.Cluster, required bool) api.SetupCheck {
	check := api.SetupCheck{
		ID: "cluster/" + c.Name, Title: "Cluster " + c.Name, Required: required,
		Backend: api.SetupBackendCluster, Cluster: c.Name, Status: api.SetupOK,
	}
	if c.Image == "" {
		check.Status = api.SetupMissing
		check.Detail = fmt.Sprintf("no agent image: build one with `agentbox image agent build --push`, then `agentbox cluster set %s --image <name>:<tag>`", c.Name)
		check.Fix = fmt.Sprintf("agentbox cluster set %s --image <name>:<tag>", c.Name)
		return check
	}
	if c.MasterURL == "" {
		check.Status = api.SetupMissing
		check.Detail = "pods can't reach this daemon without a master URL"
		check.Fix = fmt.Sprintf("agentbox cluster set %s --master-url <url>", c.Name)
		return check
	}
	// A cluster that answered badly is a caveat, not a wall: one bad pod or a
	// blip in its API says nothing about whether an agent could start, and only
	// something the user must do should hold the whole installation up.
	if c.LastError != "" {
		check.Status = api.SetupWarn
		check.Detail = fmt.Sprintf("the last check found %q: the cluster may have recovered since, and the doctor looks again", c.LastError)
		check.Fix = fmt.Sprintf("agentbox cluster doctor %s", c.Name)
		return check
	}
	if c.LastSeen.IsZero() {
		check.Status = api.SetupWarn
		check.Detail = "the daemon hasn't reached this cluster yet"
		return check
	}
	if c.Draining {
		check.Status = api.SetupWarn
		check.Detail = "draining: it takes no new agents"
		return check
	}
	check.Detail = fmt.Sprintf("%s, image %s, last seen %s", c.Namespace, c.Image, setupAgo(c.LastSeen))
	return check
}

// setupAgo is how a cluster check words when the daemon last reached it: short,
// because it is the end of a sentence, and relative, because how long ago is
// what a reader is asking.
func setupAgo(t time.Time) string {
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

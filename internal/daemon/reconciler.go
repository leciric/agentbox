package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// The reconciler keeps every cluster's pods in the state their agents' rows ask
// for: a running agent has a pod, a stopped one doesn't, a destroyed one leaves
// nothing behind, and the row says what the cluster says about it. It is not a
// scheduler: nothing here decides where a pod runs, only whether there is one.

// reconcileEvery is how often the pods of a cluster are compared with the rows
// that ask for them. A pod the cluster lost is made again within it.
const reconcileEvery = 5 * time.Second

// reconcileTimeout bounds one cluster's round, so a cluster whose API server
// stopped answering doesn't hold every other cluster's round behind it.
const reconcileTimeout = 30 * time.Second

// reconcileClusters is the loop Run starts. It does a round at once, for the pods
// an earlier daemon left, then one every reconcileEvery.
func (s *Server) reconcileClusters(ctx context.Context) {
	ticker := time.NewTicker(reconcileEvery)
	defer ticker.Stop()
	for {
		s.reconcileOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) reconcileOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	names, err := s.store.ClustersInUse(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("listing the clusters agents run in: %v", err)
		}
		return
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		if err := s.reconcileCluster(ctx, name); err != nil {
			s.logf("cluster %s: %v", name, err)
		}
	}
}

// reconcileCluster is one cluster's round: list its pods, read the rows that ask
// for them, and make the two agree.
func (s *Server) reconcileCluster(ctx context.Context, name string) error {
	cluster, err := s.store.Cluster(ctx, name)
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
	round, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	pods, err := client.List(round)
	if err != nil {
		return err
	}
	agents, err := s.store.AgentsOnCluster(round, name)
	if err != nil {
		return err
	}
	return s.reconcileAgents(round, cluster, client, pods, agents)
}

// reconcileAgents is the round's work, one agent at a time. The pods are the ones
// the cluster has, so an agent whose pod the cluster lost is one with nothing in
// the list.
func (s *Server) reconcileAgents(ctx context.Context, cluster state.Cluster, client *k8s.Cluster, pods []k8s.Pod, agents []state.Agent) error {
	reg, err := s.registryFor(ctx, cluster.Registry)
	if errors.Is(err, state.ErrNotFound) {
		// The cluster names a registry that has been removed. Its pods go on
		// running with the login they were made with; `cluster doctor` is what
		// says the configuration is wrong.
		reg, err = nil, nil
	}
	if err != nil {
		return err
	}
	byRef := make(map[string]k8s.Pod, len(pods))
	for _, pod := range pods {
		byRef[pod.Ref()] = pod
	}
	var problems []string
	for _, a := range agents {
		if a.IsLead() {
			continue
		}
		pod, ok := byRef[a.Ref()]
		if err := s.reconcileAgent(ctx, cluster, client, reg, a, pod, ok); err != nil {
			problems = append(problems, a.Ref()+": "+err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// reconcileAgent makes one agent's pod what the row asks for.
func (s *Server) reconcileAgent(ctx context.Context, cluster state.Cluster, client *k8s.Cluster, reg *state.Registry, a state.Agent, pod k8s.Pod, found bool) error {
	switch {
	case a.DesiredState == state.DesiredDestroyed:
		// Destroyed means gone, which is what destroying an Incus machine
		// does: the pod, its token, and the claim its worktree was on. The
		// branch is on the remote, and the row is what the app archives.
		if !found {
			return nil
		}
		if err := client.DeleteAll(ctx, a.Project, a.Name); err != nil {
			return err
		}
		return s.clearPod(ctx, a)
	case a.DesiredState == state.DesiredStopped:
		// Stopped keeps the claim: the worktree is what starting it again
		// resumes from.
		if !found {
			return nil
		}
		if err := client.Delete(ctx, a.Project, a.Name); err != nil {
			return err
		}
		return s.clearPod(ctx, a)
	case !found:
		return s.startPod(ctx, cluster, client, reg, a)
	case pod.Failed():
		// A failed pod is a machine the cluster gave up on. Deleting it makes
		// the next round start a new one, which is not a loop: a pod that
		// keeps failing to pull its image stays Pending, not Failed.
		if err := client.Delete(ctx, a.Project, a.Name); err != nil {
			return err
		}
		return s.clearPod(ctx, a)
	default:
		return s.adoptPod(ctx, a, pod)
	}
}

// clearPod forgets the pod a row named, once it is gone: a row that names a pod
// the cluster doesn't have is a row the app shows the wrong thing for.
//
// The agent's token goes with the pod it authenticated. Every path here has just
// deleted that pod, or found it gone, and a token is good for exactly the pod it
// was minted for: nothing brings it back, a start writes a new one, and a stopped
// agent's token is no more useful than a destroyed one's, because its pod is gone
// either way. An agent that had no token is the usual case for a row an earlier
// daemon made, so the error is not one to report.
func (s *Server) clearPod(ctx context.Context, a state.Agent) error {
	_ = s.store.RemoveAgentToken(ctx, a.Project, a.Name)
	if a.Pod == "" && a.PodUID == "" && a.StatusDetail == "" {
		return nil
	}
	return s.store.SetAgentPod(ctx, a.Project, a.Name, "", "", "")
}

// adoptPod writes what the cluster says about an agent's pod into its row, and
// writes nothing when the row already says it.
func (s *Server) adoptPod(ctx context.Context, a state.Agent, pod k8s.Pod) error {
	detail := pod.Detail()
	if a.Pod == pod.Name && a.PodUID == string(pod.UID) && a.StatusDetail == detail {
		return nil
	}
	return s.store.SetAgentPod(ctx, a.Project, a.Name, pod.Name, string(pod.UID), detail)
}

// startPod makes the pod a running agent asks for, and records it. A failure here
// is kept on the row as the reason, and tried again next round: a node with no
// room this minute is not a reason to give up on the agent.
func (s *Server) startPod(ctx context.Context, cluster state.Cluster, client *k8s.Cluster, reg *state.Registry, a state.Agent) error {
	if cluster.Image == "" {
		return s.notePodFailure(ctx, a, fmt.Sprintf("cluster %s has no image: set one with agentbox cluster set %s --image <name>:<tag>", cluster.Name, cluster.Name))
	}
	if cluster.MasterURL == "" {
		// A pod whose agentd has nowhere to dial is a pod that starts, never
		// connects, and is replaced for the same reason next round.
		return s.notePodFailure(ctx, a, fmt.Sprintf("cluster %s has no master URL: set it with agentbox cluster set %s --master-url <url>", cluster.Name, cluster.Name))
	}
	token, err := newPodToken()
	if err != nil {
		return err
	}
	// The row is written before the pod, so the token it carries is one this
	// daemon knows from the moment the pod could first use it. A pod that isn't
	// made leaves no row behind: nothing would ever use it.
	if err := s.store.SetAgentToken(ctx, a.Project, a.Name, token, time.Time{}); err != nil {
		return err
	}
	pod, err := client.Create(ctx, s.podSpec(ctx, cluster, reg, a), token)
	if err != nil {
		// A create the API server completed can still come back as an error: a
		// timeout on the way out, a proxy that dropped the answer. The pod is
		// there and dials with the token this row just wrote, so it is adopted
		// rather than removed and made again: removing the token would leave
		// that pod asking with a token nothing knows, for as long as it runs.
		if made, readErr := client.Get(ctx, a.Project, a.Name); readErr == nil {
			return s.adoptPod(ctx, a, *made)
		} else if !errors.Is(readErr, k8s.ErrNotFound) {
			// The cluster can't say whether the pod exists. Its token stays:
			// a pod that does exist needs it, and a start next round writes a
			// new one over it either way.
			_ = s.notePodFailure(ctx, a, "the pod couldn't be created: "+err.Error())
			return err
		}
		// The row keeps the reason; the round goes on with its other agents.
		_ = s.store.RemoveAgentToken(ctx, a.Project, a.Name)
		_ = s.notePodFailure(ctx, a, "the pod couldn't be created: "+err.Error())
		return err
	}
	return s.adoptPod(ctx, a, *pod)
}

func (s *Server) notePodFailure(ctx context.Context, a state.Agent, detail string) error {
	if a.StatusDetail == detail {
		return nil
	}
	return s.store.SetAgentStatusDetail(ctx, a.Project, a.Name, detail)
}

// podSpec is the pod one agent of one cluster runs in.
func (s *Server) podSpec(ctx context.Context, cluster state.Cluster, reg *state.Registry, a state.Agent) k8s.Spec {
	workspace := a.Workspace
	if workspace == "" {
		workspace = k8s.WorkspaceDir
	}
	return k8s.Spec{
		Cluster:      cluster.Name,
		Project:      a.Project,
		Agent:        a.Name,
		Image:        imageRef(cluster, reg),
		FromRegistry: reg != nil,
		PullSecret:   s.pullSecretName(ctx, cluster),
		StorageClass: cluster.StorageClass,
		// MasterURL is both the spec's own field, which the doctor and the
		// pod's probes read, and an environment variable, because Env is the
		// only thing BuildPod puts in the container.
		MasterURL: cluster.MasterURL,
		Env: map[string]string{
			k8s.EnvRef:        a.Ref(),
			k8s.EnvBranch:     a.Branch,
			k8s.EnvBaseCommit: a.BaseCommit,
			k8s.EnvMasterURL:  cluster.MasterURL,
			k8s.EnvTokenFile:  k8s.TokenFile,
			k8s.EnvWorkspace:  workspace,
		},
	}
}

// newPodToken is what agentd opens its session with: 32 random bytes, base64url
// so it is a plain header value and a plain file. This machine keeps only its
// hash, in agent_tokens.
func newPodToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

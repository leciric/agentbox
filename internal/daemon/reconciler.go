package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"agentbox/internal/agent"
	"agentbox/internal/api"
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

// capacityEvery is how often every cluster's room is read, including one no
// agent runs in yet: the app shows that number beside the cluster when it offers
// to create an agent there, which is before any reconciler round would have
// looked.
const capacityEvery = 30 * time.Second

// capacityTimeout bounds one cluster's capacity read, so a cluster that stopped
// answering doesn't hold the others.
const capacityTimeout = 15 * time.Second

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

// watchCapacities reads every cluster's room now and then, so a cluster no agent
// runs in yet still says what it has. In-use clusters are measured every
// reconciler round as well, which is fresher than this.
func (s *Server) watchCapacities(ctx context.Context) {
	s.measureClusterCapacities(ctx)
	ticker := time.NewTicker(capacityEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.measureClusterCapacities(ctx)
		}
	}
}

// measureClusterCapacities is one pass over every configured cluster. A cluster
// that can't be reached is recorded with its error and keeps its last numbers,
// so the list says it is stale rather than empty.
func (s *Server) measureClusterCapacities(ctx context.Context) {
	clusters, err := s.store.Clusters(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("measuring clusters' capacity: %v", err)
		}
		return
	}
	for _, cluster := range clusters {
		if ctx.Err() != nil {
			return
		}
		client, err := s.clusterClient(cluster)
		if err != nil {
			s.recordClusterCapacity(cluster, k8s.Capacity{}, 0, err)
			continue
		}
		round, cancel := context.WithTimeout(ctx, capacityTimeout)
		room, err := client.Capacity(round)
		cancel()
		agents, agentsErr := s.store.AgentsOnCluster(ctx, cluster.Name)
		if agentsErr != nil {
			err = errors.Join(err, agentsErr)
		}
		running := 0
		for _, a := range agents {
			if podInfo(a).Running() {
				running++
			}
		}
		s.recordClusterCapacity(cluster, room, running, err)
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
		s.recordClusterCapacity(cluster, k8s.Capacity{}, 0, err)
		return err
	}
	agents, err := s.store.AgentsOnCluster(round, name)
	if err != nil {
		return err
	}
	s.measureCapacity(round, cluster, client, pods)
	return s.reconcileAgents(round, cluster, client, pods, agents)
}

// measureCapacity takes the cluster's room for the app and `agentbox cluster
// list`, and never fails the round: an agent that can't be placed is a problem
// the pod's own status says, and a capacity read is for showing, not deciding.
func (s *Server) measureCapacity(ctx context.Context, cluster state.Cluster, client *k8s.Cluster, pods []k8s.Pod) {
	running := 0
	for _, pod := range pods {
		if pod.Phase == corev1.PodRunning && !pod.Deleting {
			running++
		}
	}
	room, err := client.Capacity(ctx)
	s.recordClusterCapacity(cluster, room, running, err)
}

// recordClusterCapacity keeps what a look at a cluster found. A cluster that
// couldn't be measured keeps its last numbers, with the error and the time of the
// failed look, rather than reading as full.
func (s *Server) recordClusterCapacity(cluster state.Cluster, room k8s.Capacity, running int, err error) {
	now := time.Now().UTC()
	if err != nil {
		capacity, ok := s.clusterCapacity(cluster.Name)
		if !ok {
			capacity = api.ClusterCapacity{Agents: -1}
		}
		capacity.Error = err.Error()
		capacity.At = now
		s.setClusterCapacity(cluster.Name, capacity)
		return
	}
	s.setClusterCapacity(cluster.Name, capacityOf(cluster, room, running, now))
}

// clusterCapacity is how much room one cluster has, as its nodes and its own
// ceiling leave it. Agents is -1 when there is no ceiling to count: no MaxAgents,
// and no per-agent size to divide the room by.
func capacityOf(cluster state.Cluster, room k8s.Capacity, running int, now time.Time) api.ClusterCapacity {
	cpu, memory := room.CPU(), room.Memory()
	if budget, err := resource.ParseQuantity(cluster.CPU); cluster.CPU != "" && err == nil {
		cpu = smallest(cpu, budgetMinus(budget, room.RequestedCPU))
	}
	if budget, err := resource.ParseQuantity(cluster.Memory); cluster.Memory != "" && err == nil {
		memory = smallest(memory, budgetMinus(budget, room.RequestedMemory))
	}
	out := api.ClusterCapacity{
		Agents:  -1,
		Running: running,
		CPU:     cpu.String(),
		Memory:  memory.String(),
		At:      now,
	}
	if cluster.MaxAgents > 0 {
		out.Agents = max(0, cluster.MaxAgents-running)
	}
	if size, err := resource.ParseQuantity(cluster.AgentCPU); cluster.AgentCPU != "" && err == nil && size.MilliValue() > 0 {
		out.Agents = fewest(out.Agents, int(cpu.MilliValue()/size.MilliValue()))
	}
	if size, err := resource.ParseQuantity(cluster.AgentMemory); cluster.AgentMemory != "" && err == nil && size.Value() > 0 {
		out.Agents = fewest(out.Agents, int(memory.Value()/size.Value()))
	}
	return out
}

// budgetMinus is what a budget has left after the pods already ask for it,
// floored at zero.
func budgetMinus(budget, requested resource.Quantity) resource.Quantity {
	if budget.Cmp(requested) <= 0 {
		return resource.Quantity{}
	}
	left := budget.DeepCopy()
	left.Sub(requested)
	return left
}

// smallest is the smaller of two quantities, as a copy.
func smallest(a, b resource.Quantity) resource.Quantity {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// fewest narrows a count of agents to another, where -1 is no ceiling at all.
func fewest(a, b int) int {
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	default:
		return min(a, b)
	}
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
	here := make(map[string]bool, len(agents))
	for _, a := range agents {
		here[a.Ref()] = true
		if a.IsLead() {
			continue
		}
		pod, ok := byRef[a.Ref()]
		if err := s.reconcileAgent(ctx, cluster, client, reg, a, pod, ok); err != nil {
			problems = append(problems, a.Ref()+": "+err.Error())
		}
	}
	for _, pod := range pods {
		if pod.Ref() == "" || here[pod.Ref()] {
			continue
		}
		if err := s.removeStrayPod(ctx, cluster, client, pod); err != nil {
			problems = append(problems, pod.Ref()+": "+err.Error())
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
		//
		// The claim is deleted whether or not a pod is left: a create or a
		// fork that failed may have made one before its pod existed, and a
		// destroy whose own delete failed left one too. DeleteAll finds
		// nothing the second time, which is what makes asking every round
		// cheap.
		if err := client.DeleteAll(ctx, a.Project, a.Name, a.Claim); err != nil {
			return err
		}
		if a.Status == state.AgentCreating {
			// A create that never finished has nobody to archive it: its
			// job gave up and left the row asking for this, and a row left
			// here is an "incomplete" agent for good and a cluster that
			// can't be removed. Its machine is gone now, so the row goes
			// too, the way a failed create on this host removes its own.
			_ = s.store.RemoveAgentToken(ctx, a.Project, a.Name)
			if err := s.store.RemoveAgent(ctx, a.Project, a.Name); err != nil {
				return err
			}
			s.logf("removed %s, a create that failed in cluster %s, with its pod and claim", a.Ref(), a.Cluster)
			return nil
		}
		// Any other destroyed row is a destroy in progress, or one that
		// failed and will be asked again: that destroy archives it.
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

// removeStrayPod deletes a pod of this cluster whose agent's row says it runs
// in another one: a round that read the row before a move or its rollback
// changed it can have made it. Its token and claim are left alone, since the
// token row is the agent's live pod's and the claim may be the one it lives
// on. A pod with no row at all is not judged here: it is nothing this daemon
// made that it knows of.
func (s *Server) removeStrayPod(ctx context.Context, cluster state.Cluster, client *k8s.Cluster, pod k8s.Pod) error {
	row, err := s.store.Agent(ctx, pod.Project, pod.Agent)
	if err != nil {
		return nil
	}
	if !row.OnCluster() || row.Cluster == cluster.Name {
		return nil
	}
	if err := client.Delete(ctx, pod.Project, pod.Agent); err != nil {
		return err
	}
	s.logf("deleted %s's pod %s from cluster %s: the agent runs in cluster %s", pod.Ref(), pod.Name, cluster.Name, row.Cluster)
	return nil
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
	s.dropGatewaySession(a.Project, a.Name)
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
	// The round's row was read before the cluster was listed, which can be
	// seconds ago: a move that rolled back or a stop since then means this
	// cluster must not make a pod, nor write a token over the one the agent's
	// live pod elsewhere uses. So the row is read again right before both,
	// and the pod is made from what it says now (its claim, its image).
	cur, err := s.store.Agent(ctx, a.Project, a.Name)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.Cluster != cluster.Name || cur.DesiredState == state.DesiredStopped || cur.DesiredState == state.DesiredDestroyed {
		return nil
	}
	a = cur
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
	// The token is new, so a session the old one opened is not this pod's:
	// it goes before the pod that will use the new token connects.
	s.dropGatewaySession(a.Project, a.Name)
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
		s.dropGatewaySession(a.Project, a.Name)
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

// projectImageRef is the image a project's agents run when the project saved
// its own, and "" when it has none. It is read on every pod the reconciler
// makes, so a save reaches an agent the next time it starts; a running pod keeps
// the image it was made with until it is recreated.
func (s *Server) projectImageRef(ctx context.Context, project string) string {
	image, ok, err := s.store.ProjectImage(ctx, project)
	if err != nil || !ok {
		return ""
	}
	return image.Ref()
}

// podSpec is the pod one agent of one cluster runs in.
func (s *Server) podSpec(ctx context.Context, cluster state.Cluster, reg *state.Registry, a state.Agent) k8s.Spec {
	workspace := a.Workspace
	if workspace == "" {
		workspace = k8s.WorkspaceDir
	}
	// A restored agent runs the image its snapshot was taken with: the claim
	// holds the tools' state, not the tools, so half the machine would be new.
	// A project that saved its own image runs that, by digest when the save
	// recorded one; otherwise it runs the cluster's image.
	image := a.Image
	if image == "" {
		image = s.projectImageRef(ctx, a.Project)
	}
	if image == "" {
		image = imageRef(cluster, reg)
	}
	return k8s.Spec{
		Cluster:      cluster.Name,
		Project:      a.Project,
		Agent:        a.Name,
		Image:        image,
		FromRegistry: reg != nil,
		PullSecret:   s.pullSecretName(ctx, cluster),
		StorageClass: cluster.StorageClass,
		NodeSelector: cluster.Labels,
		Claim:        a.Claim,
		// The sizes are the cluster's, not the pod's: every agent of a
		// cluster is the same size, which is what makes its capacity a count.
		CPU:              cluster.AgentCPU,
		Memory:           cluster.AgentMemory,
		WorkspaceSize:    cluster.WorkspaceSize,
		EphemeralStorage: cluster.EphemeralStorage,
		// MasterURL is both the spec's own field, which the doctor and the
		// pod's probes read, and an environment variable, because Env is the
		// only thing BuildPod puts in the container.
		MasterURL: cluster.MasterURL,
		// A pod resolves what its node resolves, and this machine's private
		// git remote may be a name only this machine knows. Resolving it here
		// hands the pod an address instead.
		HostAliases: s.podHostAliases(ctx, a.Project),
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

// podHostAliases is where a pod finds its project's git remote: a cluster's
// pods resolve what its nodes resolve, and the remote's host may be a name only
// this machine knows, as a tailnet service name is. Empty when it needs nothing:
// an address in the URL, a profile the daemon can't resolve either, or a
// project with no remote at all.
func (s *Server) podHostAliases(ctx context.Context, project string) map[string]string {
	p, err := s.store.Project(ctx, project)
	if err != nil {
		return nil
	}
	remotes, err := s.store.ProjectGitRemotes(ctx, p.Name)
	if err != nil {
		return nil
	}
	r, ok := gitRemoteNamed(remotes, agentRemoteOrPrivate(p.AgentRemote))
	if !ok {
		return nil
	}
	resolved, err := agent.ResolveGitProfileURL(ctx, r)
	if err != nil {
		return nil
	}
	return agent.GitHostAlias(ctx, resolved)
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

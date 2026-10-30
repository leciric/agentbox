package api

import "time"

// The clusters and registries API: where agents run when they don't run on this
// machine, and where their images come from (K15). One rule shapes all of it, as
// with secrets: no response carries a credential. A cluster's kubeconfig and a
// registry's login go in once, are sealed, and are never read back.

// Cluster is one Kubernetes cluster agents can run in.
type Cluster struct {
	Name string `json:"name"`
	// Namespace is where its agents run. It is not a setting: the ServiceAccount
	// the daemon acts as is scoped to it.
	Namespace string `json:"namespace"`
	// StorageClass is what an agent's workspace claim asks for. Empty is the
	// cluster's default.
	StorageClass string `json:"storageClass,omitempty"`
	// Image is the agent image under the registry, like agentbox-agent:0.3.0.
	// An upgrade replaces it with the same name at a digest, and keeps the
	// one it replaced in PreviousImage.
	Image string `json:"image,omitempty"`
	// PreviousImage is the image this cluster ran before its last upgrade, for
	// a rollback. Empty until one happens.
	PreviousImage string `json:"previousImage,omitempty"`
	// Draining says this cluster takes no new agents: it keeps the ones it
	// has until they are stopped, moved or destroyed. Admission refuses it,
	// with itself as the reason.
	Draining bool `json:"draining,omitempty"`
	// MasterURL is where this daemon is reached from the cluster's pods: the
	// MagicDNS name through the tailnet, or http://host.k3d.internal:7782 for
	// a local k3d cluster. Absent means the user hasn't set it, and no agent
	// starts there until they do.
	MasterURL string `json:"masterURL,omitempty"`
	// Registry is the name in /v1/registries its images come from; empty for a
	// cluster whose image is imported into it.
	Registry string `json:"registry,omitempty"`
	// MaxAgents is the ceiling on agents here, zero for as many as the nodes
	// allow.
	MaxAgents int `json:"maxAgents"`
	// CPU and Memory are an optional budget for the cluster's agents, as
	// Kubernetes quantities.
	CPU    string            `json:"cpu,omitempty"`
	Memory string            `json:"memory,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	// AgentCPU and AgentMemory are what one agent's pod asks for, as
	// Kubernetes quantities. Absent asks for nothing in particular.
	AgentCPU    string `json:"agentCPU,omitempty"`
	AgentMemory string `json:"agentMemory,omitempty"`
	// WorkspaceSize is an agent's claim, as a Kubernetes quantity. Absent is
	// the daemon's own default (20Gi). A claim is made once, so a change here
	// reaches the next agent, not an existing one.
	WorkspaceSize string `json:"workspaceSize,omitempty"`
	// EphemeralStorage is how much local disk one pod may fill outside its
	// claim, as a Kubernetes quantity. Absent is the daemon's own default
	// (10Gi).
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
	// Tailnet is how its pods reach this machine and the private git remote:
	// operator, sidecar or none.
	Tailnet      string    `json:"tailnet"`
	Capabilities []string  `json:"capabilities,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	// LastSeen is when the daemon last reached the cluster, and LastError what
	// it said when it couldn't. Both absent mean it hasn't looked yet.
	LastSeen  *time.Time `json:"lastSeen,omitempty"`
	LastError string     `json:"lastError,omitempty"`
	// Capacity is how many agents fit, from the daemon's last look: absent until
	// it has taken one.
	Capacity *ClusterCapacity `json:"capacity,omitempty"`
}

// ClusterConfig is what a change to a cluster may touch: PUT /v1/clusters/{name}
// replaces all of it. How the cluster is reached, its name and its namespace are
// not here, because they are not settings.
type ClusterConfig struct {
	StorageClass     string            `json:"storageClass,omitempty"`
	Image            string            `json:"image,omitempty"`
	MasterURL        string            `json:"masterURL,omitempty"`
	Registry         string            `json:"registry,omitempty"`
	MaxAgents        int               `json:"maxAgents"`
	CPU              string            `json:"cpu,omitempty"`
	Memory           string            `json:"memory,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	AgentCPU         string            `json:"agentCPU,omitempty"`
	AgentMemory      string            `json:"agentMemory,omitempty"`
	WorkspaceSize    string            `json:"workspaceSize,omitempty"`
	EphemeralStorage string            `json:"ephemeralStorage,omitempty"`
	Tailnet          string            `json:"tailnet"`
	Capabilities     []string          `json:"capabilities,omitempty"`
}

// ClusterCapacity is how much room a cluster has, as its nodes say: what
// admission can place, not a guess from the rack's name.
type ClusterCapacity struct {
	// Agents is how many more agents fit now, after MaxAgents and the free
	// CPU and memory. -1 is a cluster with no ceiling it can count: no
	// MaxAgents, and no AgentCPU or AgentMemory to divide the room by.
	Agents int `json:"agents"`
	// Running is how many agents have a pod running now.
	Running int `json:"running"`
	// CPU and Memory are what is left to place agents with, after the pods
	// already in the namespace and the cluster's own budget, as Kubernetes
	// quantities.
	CPU    string    `json:"cpu,omitempty"`
	Memory string    `json:"memory,omitempty"`
	At     time.Time `json:"at"`
	// Error is why it couldn't be measured, when the cluster didn't answer.
	Error string `json:"error,omitempty"`
}

// AddClusterRequest is POST /v1/clusters: everything the daemon needs to apply
// AgentBox's access to a cluster and keep its own token.
//
// MasterURL is where the cluster's pods reach this daemon: an absolute http or
// https URL, trimmed. Empty leaves it unset, which is a cluster the doctor fails
// until the user sets one with `cluster set --master-url`.
type AddClusterRequest struct {
	Name             string            `json:"name"`
	Namespace        string            `json:"namespace,omitempty"`
	StorageClass     string            `json:"storageClass,omitempty"`
	Image            string            `json:"image,omitempty"`
	MasterURL        string            `json:"masterURL,omitempty"`
	Registry         string            `json:"registry,omitempty"`
	MaxAgents        int               `json:"maxAgents,omitempty"`
	CPU              string            `json:"cpu,omitempty"`
	Memory           string            `json:"memory,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	AgentCPU         string            `json:"agentCPU,omitempty"`
	AgentMemory      string            `json:"agentMemory,omitempty"`
	WorkspaceSize    string            `json:"workspaceSize,omitempty"`
	EphemeralStorage string            `json:"ephemeralStorage,omitempty"`
	Tailnet          string            `json:"tailnet,omitempty"`
	Capabilities     []string          `json:"capabilities,omitempty"`
	// Kubeconfig is the admin kubeconfig the user's kubectl uses, as its YAML.
	// The daemon applies the namespace, the ServiceAccount, the Role and its
	// binding with it, makes a token Secret, and keeps only the
	// ServiceAccount's kubeconfig, sealed.
	Kubeconfig string `json:"kubeconfig"`
}

// RotateClusterRequest is POST /v1/clusters/{name}/rotate: a new token for a
// cluster AgentBox already drives.
type RotateClusterRequest struct {
	// Kubeconfig is the admin kubeconfig again. Rotating deletes the token
	// Secret, and reaching the cluster to do it is what an admin kubeconfig is
	// for; the old token may be the thing that stopped working.
	Kubeconfig string `json:"kubeconfig"`
}

// ClusterReport is POST /v1/clusters/{name}/doctor: what every check found, in
// the order it looked.
type ClusterReport struct {
	// Server is the API server the check talked to.
	Server string         `json:"server,omitempty"`
	OK     bool           `json:"ok"`
	Checks []ClusterCheck `json:"checks"`
}

// ClusterCheck is one thing the doctor looked at. Skipped is a check the
// cluster's own configuration makes impossible, not a failure; Detail says what
// it found.
type ClusterCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ClusterDetail is GET /v1/clusters/{name}/detail: what the cluster holds right
// now, for its page. It is a snapshot rather than a watch: the page asks again.
type ClusterDetail struct {
	Cluster Cluster `json:"cluster"`
	// Error is why the lists from the cluster are empty, when it couldn't be
	// read. The cluster's own LastError is the doctor's, and is left alone: a
	// detail read is not a check of the image or the registry.
	Error  string         `json:"error,omitempty"`
	Pods   []ClusterPod   `json:"pods,omitempty"`
	Claims []ClusterClaim `json:"claims,omitempty"`
	Events []ClusterEvent `json:"events,omitempty"`
	Queue  []ClusterQueue `json:"queue,omitempty"`
}

// ClusterPod is one pod in the cluster's namespace, which is one agent's
// machine. Project and Agent are empty for a pod that isn't AgentBox's.
type ClusterPod struct {
	Name      string    `json:"name"`
	Project   string    `json:"project,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Phase     string    `json:"phase"`
	Ready     bool      `json:"ready"`
	Node      string    `json:"node,omitempty"`
	Image     string    `json:"image,omitempty"`
	Restarts  int       `json:"restarts,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// Reason and Message are the cluster's own words for what is wrong, when
	// something is: ImagePullBackOff, CrashLoopBackOff, a node with no room.
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// ClusterClaim is one workspace claim, which keeps an agent's clone and home.
// Storage is what it asks for, as the cluster reports it.
type ClusterClaim struct {
	Name      string    `json:"name"`
	Project   string    `json:"project,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Storage   string    `json:"storage,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ClusterEvent is one of the cluster's own events about an object in its
// namespace: why a pod didn't start, what a node ran out of.
type ClusterEvent struct {
	Type    string    `json:"type"`
	Reason  string    `json:"reason"`
	Object  string    `json:"object"`
	Message string    `json:"message"`
	Count   int       `json:"count,omitempty"`
	At      time.Time `json:"at"`
}

// ClusterQueue is one agent waiting for room in this cluster. Position is its
// place in the whole queue of its project, which is the order it will start in.
type ClusterQueue struct {
	Project  string `json:"project"`
	Agent    string `json:"agent"`
	Position int    `json:"position"`
	Reason   string `json:"reason,omitempty"`
}

// DrainClusterRequest is POST /v1/clusters/{name}/drain: stop taking new agents
// here, and either move what runs here or stop it. With neither, the cluster is
// only marked.
type DrainClusterRequest struct {
	// MoveTo moves every running agent here to that cluster, one at a time.
	MoveTo string `json:"moveTo,omitempty"`
	// KeepRunning drains the cluster without touching the agents on it: they
	// keep running until they are stopped or moved by hand.
	KeepRunning bool `json:"keepRunning,omitempty"`
}

// UpgradeClusterRequest is POST /v1/clusters/{name}/upgrade: run the cluster's
// image at a new digest, and recreate its pods one at a time. Rollback puts the
// image the cluster ran before the last upgrade back instead.
type UpgradeClusterRequest struct {
	// Digest is the sha256:... an upgrade moves to. Not used by a rollback.
	Digest string `json:"digest,omitempty"`
	// Rollback goes back to PreviousImage, which is what that field is for.
	Rollback bool `json:"rollback,omitempty"`
}

// Registry is one OCI registry. It never carries the login: HasCredential says
// whether there is one.
type Registry struct {
	Name string `json:"name"`
	// URL is the repository prefix, like hub.example.com/you.
	URL string `json:"url"`
	// Public says anyone may pull what is pushed here.
	Public bool `json:"public"`
	// HasCA says a private CA signed the registry, and HasCredential whether a
	// login is stored.
	HasCA         bool       `json:"hasCa"`
	HasCredential bool       `json:"hasCredential"`
	Insecure      bool       `json:"insecure"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastSeen      *time.Time `json:"lastSeen,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
}

// AddRegistryRequest is POST /v1/registries. Username and Token are empty for a
// registry that needs no login.
type AddRegistryRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Token    string `json:"token,omitempty"`
	Public   bool   `json:"public,omitempty"`
	// CA is the registry's PEM certificate, when a private CA signed it.
	CA       string `json:"ca,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
}

// RotateRegistryRequest is POST /v1/registries/{name}/rotate: a new login for a
// registry AgentBox already pushes to.
type RotateRegistryRequest struct {
	Username string `json:"username,omitempty"`
	Token    string `json:"token,omitempty"`
	// KeepLogin leaves the stored login as it is, for a request that only
	// changes the settings below.
	KeepLogin bool `json:"keepLogin,omitempty"`
	// NoLogin removes the login instead of replacing it, and every cluster's
	// pull secret with it. Without it, and without KeepLogin, a rotation needs
	// a token: an empty one is refused rather than taken to mean "no login".
	NoLogin bool `json:"noLogin,omitempty"`
	// URL, CA, Insecure and Public change the registry's settings when set and
	// leave them alone when absent. An empty CA takes the registry's CA away.
	URL      *string `json:"url,omitempty"`
	CA       *string `json:"ca,omitempty"`
	Insecure *bool   `json:"insecure,omitempty"`
	Public   *bool   `json:"public,omitempty"`
}

// RegistryTest is POST /v1/registries/{name}/test: whether the registry answers
// and accepts the login. It does not push anything.
type RegistryTest struct {
	OK bool `json:"ok"`
	// Detail is what answered, or what it said it didn't like.
	Detail string `json:"detail"`
}

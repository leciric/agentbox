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
	Image string `json:"image,omitempty"`
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
	// CPU and Memory are an optional budget, as Kubernetes quantities.
	CPU    string            `json:"cpu,omitempty"`
	Memory string            `json:"memory,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
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
	StorageClass string            `json:"storageClass,omitempty"`
	Image        string            `json:"image,omitempty"`
	MasterURL    string            `json:"masterURL,omitempty"`
	Registry     string            `json:"registry,omitempty"`
	MaxAgents    int               `json:"maxAgents"`
	CPU          string            `json:"cpu,omitempty"`
	Memory       string            `json:"memory,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Tailnet      string            `json:"tailnet"`
	Capabilities []string          `json:"capabilities,omitempty"`
}

// ClusterCapacity is how much room a cluster has, as its nodes say: what
// admission can place, not a guess from the rack's name.
type ClusterCapacity struct {
	// Agents is how many more agents fit now, after MaxAgents and the nodes'
	// free CPU and memory.
	Agents int `json:"agents"`
	// Running is how many agents it has now.
	Running int `json:"running"`
	// CPU and Memory are what its ready nodes can allocate, as Kubernetes
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
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace,omitempty"`
	StorageClass string            `json:"storageClass,omitempty"`
	Image        string            `json:"image,omitempty"`
	MasterURL    string            `json:"masterURL,omitempty"`
	Registry     string            `json:"registry,omitempty"`
	MaxAgents    int               `json:"maxAgents,omitempty"`
	CPU          string            `json:"cpu,omitempty"`
	Memory       string            `json:"memory,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Tailnet      string            `json:"tailnet,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
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
}

// RegistryTest is POST /v1/registries/{name}/test: whether the registry answers
// and accepts the login. It does not push anything.
type RegistryTest struct {
	OK bool `json:"ok"`
	// Detail is what answered, or what it said it didn't like.
	Detail string `json:"detail"`
}

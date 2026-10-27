package state

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"agentbox/internal/naming"
)

// How a cluster's pods reach this machine and the private git remote: through
// the Tailscale operator's egress proxy, through a sidecar of their own, or not
// at all, when both are addresses the cluster already reaches.
const (
	TailnetOperator = "operator"
	TailnetSidecar  = "sidecar"
	TailnetNone     = "none"
)

// Cluster is a Kubernetes cluster agents can run in, and the little that is
// AgentBox's own about it: which namespace, which image, how its pods reach the
// master.
//
// Kubeconfig is the sealed kubeconfig of a ServiceAccount scoped to Namespace,
// not an admin's: everything AgentBox does in a cluster goes through it.
type Cluster struct {
	Name string
	// Namespace is where its agents run. It is not editable after the cluster is
	// added: the Role and ServiceAccount that make the kubeconfig work live
	// there.
	Namespace string
	// StorageClass is what an agent's workspace claim asks for, like local-path
	// on k3d, or a class that can reschedule a pod on another node.
	StorageClass string
	// Image is the agent image's name under the registry, like
	// agentbox-agent:0.3.0.
	Image string
	// MasterURL is where this daemon's gateway is reached from the cluster's
	// pods: the MagicDNS name through the tailnet, or
	// http://host.k3d.internal:7782 for a local k3d cluster. Empty until the
	// user sets it, and no pod of this cluster starts until they do.
	MasterURL string
	// Registry is the name of the registry its images come from, empty for a
	// cluster that pulls an image imported into it instead (k3d image import).
	Registry string
	// MaxAgents is the ceiling on how many agents this cluster runs at once.
	// Zero is no ceiling of its own: the nodes' capacity is the limit.
	MaxAgents int
	// CPU and Memory are an optional budget for the cluster's agents, as
	// Kubernetes quantities (2, 500m; 8Gi). Empty is what the nodes have, less
	// a reserve. They are not one pod's size: AgentCPU and AgentMemory are.
	CPU    string
	Memory string
	// AgentCPU and AgentMemory are what one agent's pod asks for, as
	// Kubernetes quantities, and so what the scheduler places and the pod is
	// capped at. Empty asks for nothing in particular, which is only right in
	// a namespace whose limits say otherwise.
	AgentCPU    string
	AgentMemory string
	// WorkspaceSize is an agent's claim, as a Kubernetes quantity like 20Gi.
	// Empty is k8s.DefaultWorkspaceSize. A claim is made once and kept, so a
	// change here reaches the next agent, not an existing one.
	WorkspaceSize string
	// EphemeralStorage is how much local disk one pod may fill with what its
	// claim doesn't hold: its /tmp, /run and writable layer, as a Kubernetes
	// quantity. Empty is k8s.DefaultEphemeralStorage.
	EphemeralStorage string
	// Labels are placement constraints on nodes, like arch=amd64 or gpu=true.
	Labels map[string]string
	// Tailnet is TailnetOperator, TailnetSidecar or TailnetNone.
	Tailnet string
	// Capabilities are what agents in this cluster may ask for: kvm, gpu,
	// privileged, csi-snapshots. Placement reads them.
	Capabilities []string
	// Kubeconfig is the sealed kubeconfig of the namespace's ServiceAccount.
	Kubeconfig []byte
	// CreatedAt is when the user added it. LastSeen is when the daemon last
	// reached it, and LastError what it said when it couldn't.
	CreatedAt time.Time
	LastSeen  time.Time
	LastError string
}

// HasCapability reports whether agents here may ask for one of those things.
func (c Cluster) HasCapability(name string) bool {
	for _, capability := range c.Capabilities {
		if capability == name {
			return true
		}
	}
	return false
}

const clusterColumns = `name, namespace, storage_class, image, master_url, registry, max_agents, cpu, memory,
	agent_cpu, agent_memory, workspace_size, ephemeral_storage, labels, tailnet, capabilities, kubeconfig,
	created_at, last_seen, last_error`

// AddCluster records a cluster whose access `cluster add` has already applied
// and sealed. It never overwrites: a second add under the same name is an error,
// because the cluster that is there may have agents in it.
func (s *Store) AddCluster(ctx context.Context, c Cluster) error {
	if err := checkCluster(c); err != nil {
		return err
	}
	masterURL, err := clusterMasterURL(c.Name, c.MasterURL)
	if err != nil {
		return err
	}
	labels, capabilities, err := encodeClusterLists(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO clusters (`+clusterColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Namespace, c.StorageClass, c.Image, masterURL, c.Registry, c.MaxAgents, c.CPU, c.Memory,
		c.AgentCPU, c.AgentMemory, c.WorkspaceSize, c.EphemeralStorage,
		labels, c.Tailnet, capabilities, c.Kubeconfig, c.CreatedAt.Unix(), unixOrZero(c.LastSeen), c.LastError)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("cluster %s: %w", c.Name, ErrExists)
	}
	return err
}

// checkCluster refuses what no cluster could be: a name that isn't an
// identifier, no namespace to run in, no sealed access, a kind of tailnet
// plumbing that doesn't exist.
func checkCluster(c Cluster) error {
	if err := naming.Validate("cluster", c.Name, 63); err != nil {
		return err
	}
	if c.Namespace == "" {
		return fmt.Errorf("cluster %s needs a namespace to run its agents in", c.Name)
	}
	if len(c.Kubeconfig) == 0 {
		return fmt.Errorf("cluster %s has no sealed kubeconfig: `agentbox cluster add` writes one", c.Name)
	}
	switch c.Tailnet {
	case "", TailnetOperator, TailnetSidecar, TailnetNone:
	default:
		return fmt.Errorf("cluster %s's tailnet is %q: use %s, %s or %s", c.Name, c.Tailnet,
			TailnetOperator, TailnetSidecar, TailnetNone)
	}
	if _, err := clusterMasterURL(c.Name, c.MasterURL); err != nil {
		return err
	}
	if c.MaxAgents < 0 {
		return fmt.Errorf("cluster %s's max agents is %d: use 0 for as many as the nodes allow", c.Name, c.MaxAgents)
	}
	return checkClusterSizes(c)
}

// checkClusterSizes refuses a quantity a pod couldn't be given: a misspelled
// one, or nothing at all, which would make a claim or a pod with no room.
func checkClusterSizes(c Cluster) error {
	for _, q := range []struct{ flag, value string }{
		{"--cpu", c.CPU},
		{"--memory", c.Memory},
		{"--agent-cpu", c.AgentCPU},
		{"--agent-memory", c.AgentMemory},
		{"--workspace-size", c.WorkspaceSize},
		{"--ephemeral-storage", c.EphemeralStorage},
	} {
		if q.value == "" {
			continue
		}
		parsed, err := resource.ParseQuantity(q.value)
		if err != nil {
			return fmt.Errorf("cluster %s's %s %q: %w", c.Name, q.flag, q.value, err)
		}
		if parsed.Sign() <= 0 {
			return fmt.Errorf("cluster %s's %s is %q: use a size greater than zero", c.Name, q.flag, q.value)
		}
	}
	return nil
}

// clusterMasterURL checks where a cluster's pods reach this daemon, and returns
// it trimmed. Empty is a cluster whose user hasn't said yet: `cluster add` can
// record one before the address is known, and `cluster doctor` is what says it
// has to be set before an agent starts. It is an absolute address, because the
// daemon's own host name is only meaningful with a scheme and a port of its own;
// https is what the tailnet certificate is for, and http is a local test cluster.
//
// Pods append /v1/agent-gateway to whatever is stored, so the stored value is a
// base address and nothing else: a query, a fragment or a login would put their
// dial at the wrong path, and a login would be kept and printed as well.
func clusterMasterURL(name, raw string) (string, error) {
	url := strings.TrimSpace(raw)
	if url == "" {
		return "", nil
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		return "", fmt.Errorf("cluster %s's --master-url %q: %w", name, url, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("cluster %s's --master-url %q: use an absolute http or https URL, like https://monster.example.ts.net", name, url)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("cluster %s's --master-url %q: it has no host", name, url)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("cluster %s's --master-url %q: it has a user:password in it, which would be stored and printed", name, url)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("cluster %s's --master-url %q: it has a query, and pods append /v1/agent-gateway to the whole address, so they would dial the wrong path", name, url)
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("cluster %s's --master-url %q: it has a #fragment, and pods append /v1/agent-gateway to the whole address, so they would dial the wrong path", name, url)
	}
	// A trailing slash is how people copy an address, and the path pods append
	// already starts with one.
	return strings.TrimRight(url, "/"), nil
}

func encodeClusterLists(c Cluster) (labels, capabilities string, err error) {
	if len(c.Labels) > 0 {
		b, err := json.Marshal(c.Labels)
		if err != nil {
			return "", "", fmt.Errorf("cluster %s's labels: %w", c.Name, err)
		}
		labels = string(b)
	}
	if len(c.Capabilities) > 0 {
		b, err := json.Marshal(c.Capabilities)
		if err != nil {
			return "", "", fmt.Errorf("cluster %s's capabilities: %w", c.Name, err)
		}
		capabilities = string(b)
	}
	return labels, capabilities, nil
}

// Cluster is one cluster by name.
func (s *Store) Cluster(ctx context.Context, name string) (Cluster, error) {
	clusters, err := s.queryClusters(ctx, `WHERE name = ?`, name)
	if err != nil {
		return Cluster{}, err
	}
	if len(clusters) == 0 {
		return Cluster{}, fmt.Errorf("cluster %s: %w", name, ErrNotFound)
	}
	return clusters[0], nil
}

// Clusters is every cluster, by name.
func (s *Store) Clusters(ctx context.Context) ([]Cluster, error) {
	return s.queryClusters(ctx, `ORDER BY name`)
}

// ClustersOfRegistry is every cluster whose images come from one registry.
func (s *Store) ClustersOfRegistry(ctx context.Context, registry string) ([]Cluster, error) {
	return s.queryClusters(ctx, `WHERE registry = ? ORDER BY name`, registry)
}

func (s *Store) queryClusters(ctx context.Context, clause string, args ...any) ([]Cluster, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+clusterColumns+` FROM clusters `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var clusters []Cluster
	for rows.Next() {
		var c Cluster
		var labels, capabilities string
		var created, lastSeen int64
		if err := rows.Scan(&c.Name, &c.Namespace, &c.StorageClass, &c.Image, &c.MasterURL, &c.Registry, &c.MaxAgents,
			&c.CPU, &c.Memory, &c.AgentCPU, &c.AgentMemory, &c.WorkspaceSize, &c.EphemeralStorage,
			&labels, &c.Tailnet, &capabilities, &c.Kubeconfig,
			&created, &lastSeen, &c.LastError); err != nil {
			return nil, err
		}
		if labels != "" {
			if err := json.Unmarshal([]byte(labels), &c.Labels); err != nil {
				return nil, fmt.Errorf("cluster %s's labels: %w", c.Name, err)
			}
		}
		if capabilities != "" {
			if err := json.Unmarshal([]byte(capabilities), &c.Capabilities); err != nil {
				return nil, fmt.Errorf("cluster %s's capabilities: %w", c.Name, err)
			}
		}
		c.CreatedAt = time.Unix(created, 0)
		c.LastSeen = timeOrZero(lastSeen)
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

// SetClusterConfig writes what a change to a cluster may touch. Its name,
// namespace, sealed kubeconfig and creation stay: the first three are how it is
// reached, and the last is history.
func (s *Store) SetClusterConfig(ctx context.Context, c Cluster) error {
	if c.Namespace == "" {
		return fmt.Errorf("cluster %s needs a namespace to run its agents in", c.Name)
	}
	switch c.Tailnet {
	case "", TailnetOperator, TailnetSidecar, TailnetNone:
	default:
		return fmt.Errorf("cluster %s's tailnet is %q: use %s, %s or %s", c.Name, c.Tailnet,
			TailnetOperator, TailnetSidecar, TailnetNone)
	}
	if c.MaxAgents < 0 {
		return fmt.Errorf("cluster %s's max agents is %d: use 0 for as many as the nodes allow", c.Name, c.MaxAgents)
	}
	if err := checkClusterSizes(c); err != nil {
		return err
	}
	masterURL, err := clusterMasterURL(c.Name, c.MasterURL)
	if err != nil {
		return err
	}
	labels, capabilities, err := encodeClusterLists(c)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE clusters SET storage_class = ?, image = ?, master_url = ?, registry = ?, max_agents = ?, cpu = ?, memory = ?,
			agent_cpu = ?, agent_memory = ?, workspace_size = ?, ephemeral_storage = ?,
			labels = ?, tailnet = ?, capabilities = ? WHERE name = ?`,
		c.StorageClass, c.Image, masterURL, c.Registry, c.MaxAgents, c.CPU, c.Memory,
		c.AgentCPU, c.AgentMemory, c.WorkspaceSize, c.EphemeralStorage,
		labels, c.Tailnet, capabilities, c.Name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("cluster %s: %w", c.Name, ErrNotFound)
	}
	return nil
}

// SetClusterKubeconfig replaces a cluster's sealed access, which is what
// `agentbox cluster rotate` does.
func (s *Store) SetClusterKubeconfig(ctx context.Context, name string, kubeconfig []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE clusters SET kubeconfig = ? WHERE name = ?`, kubeconfig, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("cluster %s: %w", name, ErrNotFound)
	}
	return nil
}

// SetClusterHealth records what the last look at a cluster found. An empty
// errText is a cluster that answered.
func (s *Store) SetClusterHealth(ctx context.Context, name string, at time.Time, errText string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE clusters SET last_seen = ?, last_error = ? WHERE name = ?`,
		unixOrZero(at), errText, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("cluster %s: %w", name, ErrNotFound)
	}
	return nil
}

// RemoveCluster forgets one, unless agents still run in it: their pods and
// claims would be left with nothing that knows what they are.
func (s *Store) RemoveCluster(ctx context.Context, name string) error {
	agents, err := s.agentsOfCluster(ctx, name)
	if err != nil {
		return err
	}
	if len(agents) > 0 {
		return fmt.Errorf("cluster %s still runs %s: destroy %s first",
			name, plural(len(agents), "agent"), strings.Join(agents, ", "))
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM clusters WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("cluster %s: %w", name, ErrNotFound)
	}
	return nil
}

// agentsOfCluster names the agents still in a cluster, five at most: enough to
// say which, and never a list that buries the point of the error.
func (s *Store) agentsOfCluster(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project, name FROM agents WHERE cluster = ? ORDER BY project, name LIMIT 5`, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var agents []string
	for rows.Next() {
		var project, agent string
		if err := rows.Scan(&project, &agent); err != nil {
			return nil, err
		}
		agents = append(agents, project+"/"+agent)
	}
	return agents, rows.Err()
}

// plural is "1 agent" or "3 agents".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	// Registry is the name of the registry its images come from, empty for a
	// cluster that pulls an image imported into it instead (k3d image import).
	Registry string
	// MaxAgents is the ceiling on how many agents this cluster runs at once.
	// Zero is no ceiling of its own: the nodes' capacity is the limit.
	MaxAgents int
	// CPU and Memory are an optional budget, as Kubernetes quantities (2,
	// 500m; 8Gi). Empty is what the nodes have, less a reserve.
	CPU    string
	Memory string
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

const clusterColumns = `name, namespace, storage_class, image, registry, max_agents, cpu, memory, labels, tailnet, capabilities, kubeconfig, created_at, last_seen, last_error`

// AddCluster records a cluster whose access `cluster add` has already applied
// and sealed. It never overwrites: a second add under the same name is an error,
// because the cluster that is there may have agents in it.
func (s *Store) AddCluster(ctx context.Context, c Cluster) error {
	if err := checkCluster(c); err != nil {
		return err
	}
	labels, capabilities, err := encodeClusterLists(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO clusters (`+clusterColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Namespace, c.StorageClass, c.Image, c.Registry, c.MaxAgents, c.CPU, c.Memory,
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
	if c.MaxAgents < 0 {
		return fmt.Errorf("cluster %s's max agents is %d: use 0 for as many as the nodes allow", c.Name, c.MaxAgents)
	}
	return nil
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
		if err := rows.Scan(&c.Name, &c.Namespace, &c.StorageClass, &c.Image, &c.Registry, &c.MaxAgents,
			&c.CPU, &c.Memory, &labels, &c.Tailnet, &capabilities, &c.Kubeconfig,
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
	labels, capabilities, err := encodeClusterLists(c)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE clusters SET storage_class = ?, image = ?, registry = ?, max_agents = ?, cpu = ?, memory = ?,
			labels = ?, tailnet = ?, capabilities = ? WHERE name = ?`,
		c.StorageClass, c.Image, c.Registry, c.MaxAgents, c.CPU, c.Memory,
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

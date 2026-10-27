package state_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentbox/internal/state"
)

func clusterFixture() state.Cluster {
	return state.Cluster{
		Name:             "k3d",
		Namespace:        "agentbox",
		StorageClass:     "local-path",
		Image:            "agentbox-agent:dev",
		MasterURL:        "https://monster.example.ts.net",
		MaxAgents:        4,
		CPU:              "2",
		Memory:           "8Gi",
		AgentCPU:         "2",
		AgentMemory:      "8Gi",
		WorkspaceSize:    "40Gi",
		EphemeralStorage: "10Gi",
		Labels:           map[string]string{"arch": "amd64", "gpu": "true"},
		Tailnet:          state.TailnetNone,
		Capabilities:     []string{"kvm", "gpu"},
		Kubeconfig:       []byte("sealed kubeconfig"),
		CreatedAt:        time.Now().Truncate(time.Second),
	}
}

func TestClusterRoundTrips(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	want := clusterFixture()
	if err := st.AddCluster(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := st.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Labels, want.Labels) {
		t.Errorf("labels = %v, want %v", got.Labels, want.Labels)
	}
	if !reflect.DeepEqual(got.Capabilities, want.Capabilities) {
		t.Errorf("capabilities = %v, want %v", got.Capabilities, want.Capabilities)
	}
	for _, tc := range []struct {
		name      string
		got, want any
	}{
		{"namespace", got.Namespace, want.Namespace},
		{"storage class", got.StorageClass, want.StorageClass},
		{"image", got.Image, want.Image},
		{"master url", got.MasterURL, want.MasterURL},
		{"max agents", got.MaxAgents, want.MaxAgents},
		{"cpu", got.CPU, want.CPU},
		{"memory", got.Memory, want.Memory},
		{"agent cpu", got.AgentCPU, want.AgentCPU},
		{"agent memory", got.AgentMemory, want.AgentMemory},
		{"workspace size", got.WorkspaceSize, want.WorkspaceSize},
		{"ephemeral storage", got.EphemeralStorage, want.EphemeralStorage},
		{"tailnet", got.Tailnet, want.Tailnet},
		{"kubeconfig", string(got.Kubeconfig), string(want.Kubeconfig)},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("created at = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if !got.LastSeen.IsZero() {
		t.Errorf("last seen = %v, want never", got.LastSeen)
	}
	if !got.HasCapability("kvm") || got.HasCapability("gpu-off") {
		t.Errorf("HasCapability disagrees with %v", got.Capabilities)
	}
	clusters, err := st.Clusters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Name != "k3d" {
		t.Errorf("Clusters() = %+v, want just k3d", clusters)
	}
}

func TestClusterRefusesWhatCannotWork(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		change  func(*state.Cluster)
		wantErr string
	}{
		{name: "no name", change: func(c *state.Cluster) { c.Name = "" }, wantErr: "invalid cluster name"},
		{name: "a name with a space", change: func(c *state.Cluster) { c.Name = "my cluster" }, wantErr: "invalid cluster name"},
		{name: "no namespace", change: func(c *state.Cluster) { c.Namespace = "" }, wantErr: "needs a namespace"},
		{name: "no kubeconfig", change: func(c *state.Cluster) { c.Kubeconfig = nil }, wantErr: "no sealed kubeconfig"},
		{name: "an invented tailnet", change: func(c *state.Cluster) { c.Tailnet = "vpn" }, wantErr: "tailnet is \"vpn\""},
		{name: "negative agents", change: func(c *state.Cluster) { c.MaxAgents = -1 }, wantErr: "max agents is -1"},
		{name: "an agent cpu that isn't a quantity", change: func(c *state.Cluster) { c.AgentCPU = "two" }, wantErr: "--agent-cpu"},
		{name: "an agent memory that isn't a quantity", change: func(c *state.Cluster) { c.AgentMemory = "lots" }, wantErr: "--agent-memory"},
		{name: "a workspace of nothing", change: func(c *state.Cluster) { c.WorkspaceSize = "0" }, wantErr: "greater than zero"},
		{name: "an ephemeral storage that isn't a quantity", change: func(c *state.Cluster) { c.EphemeralStorage = "plenty" }, wantErr: "--ephemeral-storage"},
		{name: "a master url with no scheme", change: func(c *state.Cluster) { c.MasterURL = "master.example.com" }, wantErr: "--master-url"},
		{name: "a master url that isn't http", change: func(c *state.Cluster) { c.MasterURL = "ftp://master.example.com" }, wantErr: "--master-url"},
		{name: "a master url with no host", change: func(c *state.Cluster) { c.MasterURL = "https://" }, wantErr: "--master-url"},
		{name: "a master url with a query", change: func(c *state.Cluster) { c.MasterURL = "https://master.example.test?x=1" }, wantErr: `--master-url "https://master.example.test?x=1": it has a query`},
		{name: "a master url with a fragment", change: func(c *state.Cluster) { c.MasterURL = "https://master.example.test#x" }, wantErr: `--master-url "https://master.example.test#x": it has a #fragment`},
		{name: "a master url with a login", change: func(c *state.Cluster) { c.MasterURL = "https://user:pass@master.example.test" }, wantErr: `--master-url "https://user:pass@master.example.test": it has a user:password`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := clusterFixture()
			tc.change(&cluster)
			err := st.AddCluster(ctx, cluster)
			if err == nil {
				t.Fatal("AddCluster accepted it")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("AddCluster() = %v, want it to say %q", err, tc.wantErr)
			}
		})
	}
	// Twice: the cluster that is there may have agents in it.
	if err := st.AddCluster(ctx, clusterFixture()); err != nil {
		t.Fatal(err)
	}
	if err := st.AddCluster(ctx, clusterFixture()); !errors.Is(err, state.ErrExists) {
		t.Errorf("a second add = %v, want ErrExists", err)
	}
	if _, err := st.Cluster(ctx, "nas"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Cluster(nas) = %v, want ErrNotFound", err)
	}
}

func TestClusterMasterURLTrimsATrailingSlash(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	// A trailing slash is how people copy an address, and pods append their own
	// path to it, which starts with one.
	added := clusterFixture()
	added.MasterURL = "https://master.example.test/"
	if err := st.AddCluster(ctx, added); err != nil {
		t.Fatal(err)
	}
	got, err := st.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if got.MasterURL != "https://master.example.test" {
		t.Errorf("master url = %q, want the trailing slash gone", got.MasterURL)
	}
	// A change goes through the same check.
	got.MasterURL = "http://host.k3d.internal:7782/"
	if err := st.SetClusterConfig(ctx, got); err != nil {
		t.Fatal(err)
	}
	changed, err := st.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if changed.MasterURL != "http://host.k3d.internal:7782" {
		t.Errorf("master url = %q, want the trailing slash gone", changed.MasterURL)
	}
}

func TestClusterConfigIsWhatAChangeCanTouch(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	added := clusterFixture()
	if err := st.AddCluster(ctx, added); err != nil {
		t.Fatal(err)
	}
	changed := added
	changed.Namespace = "somewhere-else"   // not editable
	changed.Kubeconfig = []byte("another") // not editable
	changed.Image = "agentbox-agent:1.2.3"
	changed.MaxAgents = 9
	changed.StorageClass = "longhorn"
	changed.AgentCPU = "4"
	changed.AgentMemory = "16Gi"
	changed.WorkspaceSize = "80Gi"
	changed.EphemeralStorage = "20Gi"
	// Spaces around an address someone pasted are not part of it, and plain
	// http is what a local test cluster uses.
	changed.MasterURL = "  http://host.k3d.internal:7782  "
	changed.Labels = map[string]string{"arch": "arm64"}
	changed.Capabilities = nil
	if err := st.SetClusterConfig(ctx, changed); err != nil {
		t.Fatal(err)
	}
	got, err := st.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Namespace != added.Namespace || string(got.Kubeconfig) != string(added.Kubeconfig) {
		t.Errorf("a config change rewrote how the cluster is reached: %q, %q", got.Namespace, got.Kubeconfig)
	}
	if got.Image != changed.Image || got.MaxAgents != changed.MaxAgents || got.StorageClass != changed.StorageClass {
		t.Errorf("config = %q, %d, %q, want %q, %d, %q",
			got.Image, got.MaxAgents, got.StorageClass, changed.Image, changed.MaxAgents, changed.StorageClass)
	}
	if got.MasterURL != "http://host.k3d.internal:7782" {
		t.Errorf("master url = %q, want the pasted address trimmed", got.MasterURL)
	}
	if got.AgentCPU != "4" || got.AgentMemory != "16Gi" || got.WorkspaceSize != "80Gi" || got.EphemeralStorage != "20Gi" {
		t.Errorf("sizes = %q, %q, %q, %q, want 4, 16Gi, 80Gi, 20Gi",
			got.AgentCPU, got.AgentMemory, got.WorkspaceSize, got.EphemeralStorage)
	}
	if !reflect.DeepEqual(got.Labels, changed.Labels) || got.Capabilities != nil {
		t.Errorf("labels, capabilities = %v, %v, want %v, none", got.Labels, got.Capabilities, changed.Labels)
	}
	if err := st.SetClusterConfig(ctx, state.Cluster{Name: "nas", Namespace: "agentbox"}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("changing a cluster that isn't there = %v, want ErrNotFound", err)
	}
}

func TestClusterAccessAndHealthAreWrittenSeparately(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.AddCluster(ctx, clusterFixture()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClusterKubeconfig(ctx, "k3d", []byte("rotated")); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Truncate(time.Second)
	if err := st.SetClusterHealth(ctx, "k3d", seen, "no route to the API server"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Cluster(ctx, "k3d")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Kubeconfig) != "rotated" {
		t.Errorf("kubeconfig = %q, want the rotated one", got.Kubeconfig)
	}
	if !got.LastSeen.Equal(seen) || got.LastError != "no route to the API server" {
		t.Errorf("health = %v, %q, want %v, %q", got.LastSeen, got.LastError, seen, "no route to the API server")
	}
	// A doctor that passes clears the error.
	if err := st.SetClusterHealth(ctx, "k3d", seen, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Cluster(ctx, "k3d")
	if got.LastError != "" {
		t.Errorf("last error = %q after a passing doctor, want none", got.LastError)
	}
	for _, err := range []error{
		st.SetClusterKubeconfig(ctx, "nas", []byte("x")),
		st.SetClusterHealth(ctx, "nas", seen, ""),
		st.RemoveCluster(ctx, "nas"),
	} {
		if !errors.Is(err, state.ErrNotFound) {
			t.Errorf("%v, want ErrNotFound", err)
		}
	}
}

func TestRemoveClusterRefusesWhileAgentsRunThere(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.AddCluster(ctx, clusterFixture()); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveCluster(ctx, "k3d"); err != nil {
		t.Fatalf("removing an empty cluster: %v", err)
	}
	if err := st.AddCluster(ctx, clusterFixture()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	if err := st.AddProject(ctx, state.Project{Name: "pawly", Root: t.TempDir(), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", AI: "claude",
		Branch: "agentbox/agent-01", BaseRef: "main", BaseCommit: "abc", Worktree: "/tmp/w",
		Status: "running", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE agents SET cluster = 'k3d' WHERE project = 'pawly'`); err != nil {
		t.Fatal(err)
	}
	err := st.RemoveCluster(ctx, "k3d")
	if err == nil {
		t.Fatal("removed a cluster agents run in")
	}
	if !strings.Contains(err.Error(), "pawly/agent-01") {
		t.Errorf("RemoveCluster() = %v, want it to name the agent", err)
	}
}

func registryFixture() state.Registry {
	return state.Registry{
		Name:           "forgejo",
		URL:            "hub.example.com/h4ad",
		SealedUsername: []byte("sealed user"),
		SealedSecret:   []byte("sealed token"),
		CA:             []byte("-----BEGIN CERTIFICATE-----"),
		CreatedAt:      time.Now().Truncate(time.Second),
	}
}

func TestRegistryRoundTrips(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	want := registryFixture()
	if err := st.AddRegistry(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := st.Registry(ctx, "forgejo")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != want.URL || !got.HasCredential() || got.Public || got.Insecure {
		t.Errorf("registry = %+v, want %+v", got, want)
	}
	if string(got.CA) != string(want.CA) {
		t.Errorf("CA = %q, want %q", got.CA, want.CA)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.LastSeen.IsZero() {
		t.Errorf("created, last seen = %v, %v, want %v, never", got.CreatedAt, got.LastSeen, want.CreatedAt)
	}
	if err := st.AddRegistry(ctx, want); !errors.Is(err, state.ErrExists) {
		t.Errorf("a second add = %v, want ErrExists", err)
	}
	if _, err := st.Registry(ctx, "dockerhub"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Registry(dockerhub) = %v, want ErrNotFound", err)
	}
}

func TestRegistryURLCannotBeAnAddress(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	for _, tc := range []struct{ url, wantErr string }{
		{"", "needs a url"},
		{"https://hub.example.com/h4ad", "leave the scheme off"},
		{"http://127.0.0.1:5000", "leave the scheme off"},
	} {
		reg := registryFixture()
		reg.URL = tc.url
		err := st.AddRegistry(ctx, reg)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("AddRegistry(%q) = %v, want it to say %q", tc.url, err, tc.wantErr)
		}
	}
	// A trailing slash is not a difference: it is how people copy a prefix.
	reg := registryFixture()
	reg.URL = "hub.example.com/h4ad/"
	if err := st.AddRegistry(ctx, reg); err != nil {
		t.Fatal(err)
	}
	got, err := st.Registry(ctx, "forgejo")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "hub.example.com/h4ad" {
		t.Errorf("url = %q, want the trailing slash gone", got.URL)
	}
}

func TestRegistryHealthAndCredential(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.AddRegistry(ctx, registryFixture()); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Truncate(time.Second)
	if err := st.SetRegistryHealth(ctx, "forgejo", seen, "401 Unauthorized"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRegistryCredential(ctx, "forgejo", []byte("new user"), []byte("new token")); err != nil {
		t.Fatal(err)
	}
	got, err := st.Registry(ctx, "forgejo")
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastSeen.Equal(seen) || got.LastError != "401 Unauthorized" {
		t.Errorf("health = %v, %q", got.LastSeen, got.LastError)
	}
	if string(got.SealedUsername) != "new user" || string(got.SealedSecret) != "new token" {
		t.Errorf("credential = %q, %q, want the rotated one", got.SealedUsername, got.SealedSecret)
	}
	for _, err := range []error{
		st.SetRegistryHealth(ctx, "dockerhub", seen, ""),
		st.SetRegistryCredential(ctx, "dockerhub", nil, nil),
		st.RemoveRegistry(ctx, "dockerhub"),
	} {
		if !errors.Is(err, state.ErrNotFound) {
			t.Errorf("%v, want ErrNotFound", err)
		}
	}
}

func TestRegistryCannotBeRemovedWhileAClusterPullsFromIt(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.AddRegistry(ctx, registryFixture()); err != nil {
		t.Fatal(err)
	}
	cluster := clusterFixture()
	cluster.Registry = "forgejo"
	if err := st.AddCluster(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	err := st.RemoveRegistry(ctx, "forgejo")
	if err == nil {
		t.Fatal("removed the registry a cluster pulls from")
	}
	if !strings.Contains(err.Error(), "k3d") {
		t.Errorf("RemoveRegistry() = %v, want it to name the cluster", err)
	}
	clusters, err := st.ClustersOfRegistry(ctx, "forgejo")
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Name != "k3d" {
		t.Errorf("ClustersOfRegistry() = %+v, want just k3d", clusters)
	}
}

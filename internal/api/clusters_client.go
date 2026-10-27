package api

import (
	"context"
	"net/http"
	"net/url"
)

// Clusters is every cluster, by name.
func (c *Client) Clusters(ctx context.Context) ([]Cluster, error) {
	var out []Cluster
	return out, c.do(ctx, http.MethodGet, "/v1/clusters", nil, &out)
}

// Cluster is one, by name.
func (c *Client) Cluster(ctx context.Context, name string) (Cluster, error) {
	var out Cluster
	return out, c.do(ctx, http.MethodGet, "/v1/clusters/"+url.PathEscape(name), nil, &out)
}

// AddCluster applies AgentBox's access to a cluster with the admin kubeconfig in
// the request, and returns what the daemon kept.
func (c *Client) AddCluster(ctx context.Context, req AddClusterRequest) (Cluster, error) {
	var out Cluster
	return out, c.do(ctx, http.MethodPost, "/v1/clusters", req, &out)
}

// SetClusterConfig replaces what a change to a cluster may touch.
func (c *Client) SetClusterConfig(ctx context.Context, name string, cfg ClusterConfig) (Cluster, error) {
	var out Cluster
	return out, c.do(ctx, http.MethodPut, "/v1/clusters/"+url.PathEscape(name), cfg, &out)
}

// RemoveCluster forgets one, unless agents run there.
func (c *Client) RemoveCluster(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/clusters/"+url.PathEscape(name), nil, nil)
}

// DoctorCluster checks a cluster with the daemon's own access: whether it
// answers, has room, can make the claim an agent's workspace lives on, and can
// pull the image.
func (c *Client) DoctorCluster(ctx context.Context, name string) (ClusterReport, error) {
	var out ClusterReport
	return out, c.do(ctx, http.MethodPost, "/v1/clusters/"+url.PathEscape(name)+"/doctor", nil, &out)
}

// RotateCluster makes a cluster a new token and keeps it, sealed.
func (c *Client) RotateCluster(ctx context.Context, name string, req RotateClusterRequest) (Cluster, error) {
	var out Cluster
	return out, c.do(ctx, http.MethodPost, "/v1/clusters/"+url.PathEscape(name)+"/rotate", req, &out)
}

// ClusterDetail reads what a cluster holds now: its pods, claims, recent events
// and the agents waiting for room.
func (c *Client) ClusterDetail(ctx context.Context, name string) (ClusterDetail, error) {
	var out ClusterDetail
	return out, c.do(ctx, http.MethodGet, "/v1/clusters/"+url.PathEscape(name)+"/detail", nil, &out)
}

// DrainCluster starts a job that stops a cluster taking new agents, and either
// moves the agents running there or stops them.
func (c *Client) DrainCluster(ctx context.Context, name string, req DrainClusterRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/clusters/"+url.PathEscape(name)+"/drain", req, &out)
}

// UndrainCluster lets a draining cluster take new agents again.
func (c *Client) UndrainCluster(ctx context.Context, name string) (Cluster, error) {
	var out Cluster
	return out, c.do(ctx, http.MethodPost, "/v1/clusters/"+url.PathEscape(name)+"/undrain", nil, &out)
}

// UpgradeCluster starts a job that runs a cluster's image at a new digest and
// recreates its pods one at a time.
func (c *Client) UpgradeCluster(ctx context.Context, name string, req UpgradeClusterRequest) (Job, error) {
	var out Job
	return out, c.do(ctx, http.MethodPost, "/v1/clusters/"+url.PathEscape(name)+"/upgrade", req, &out)
}

// Registries is every registry, by name.
func (c *Client) Registries(ctx context.Context) ([]Registry, error) {
	var out []Registry
	return out, c.do(ctx, http.MethodGet, "/v1/registries", nil, &out)
}

// Registry is one, by name.
func (c *Client) Registry(ctx context.Context, name string) (Registry, error) {
	var out Registry
	return out, c.do(ctx, http.MethodGet, "/v1/registries/"+url.PathEscape(name), nil, &out)
}

// AddRegistry records a registry and seals its login.
func (c *Client) AddRegistry(ctx context.Context, req AddRegistryRequest) (Registry, error) {
	var out Registry
	return out, c.do(ctx, http.MethodPost, "/v1/registries", req, &out)
}

// RemoveRegistry forgets one, unless a cluster pulls from it.
func (c *Client) RemoveRegistry(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/registries/"+url.PathEscape(name), nil, nil)
}

// TestRegistry asks a registry whether it answers and accepts the stored login.
func (c *Client) TestRegistry(ctx context.Context, name string) (RegistryTest, error) {
	var out RegistryTest
	return out, c.do(ctx, http.MethodPost, "/v1/registries/"+url.PathEscape(name)+"/test", nil, &out)
}

// RotateRegistry replaces a registry's login and rewrites the pull secret of
// every cluster that pulls from it.
func (c *Client) RotateRegistry(ctx context.Context, name string, req RotateRegistryRequest) (Registry, error) {
	var out Registry
	return out, c.do(ctx, http.MethodPost, "/v1/registries/"+url.PathEscape(name)+"/rotate", req, &out)
}

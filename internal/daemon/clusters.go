package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/k8s"
	"agentbox/internal/registry"
	"agentbox/internal/state"
)

// Clusters and registries: where agents run when they don't run on this machine,
// and where the images they run come from.
//
// A cluster's kubeconfig and a registry's login reach this file in a request,
// are sealed by the same key agent secrets are, and never leave again: every
// answer here says whether a credential is stored, not what it is.

func registryInfo(reg state.Registry) api.Registry {
	out := api.Registry{
		Name:          reg.Name,
		URL:           reg.URL,
		Public:        reg.Public,
		HasCA:         len(reg.CA) > 0,
		HasCredential: reg.HasCredential(),
		Insecure:      reg.Insecure,
		CreatedAt:     reg.CreatedAt,
		LastError:     reg.LastError,
	}
	if !reg.LastSeen.IsZero() {
		seen := reg.LastSeen
		out.LastSeen = &seen
	}
	return out
}

func clusterInfo(c state.Cluster) api.Cluster {
	out := api.Cluster{
		Name:         c.Name,
		Namespace:    c.Namespace,
		StorageClass: c.StorageClass,
		Image:        c.Image,
		Registry:     c.Registry,
		MaxAgents:    c.MaxAgents,
		CPU:          c.CPU,
		Memory:       c.Memory,
		Labels:       c.Labels,
		Tailnet:      c.Tailnet,
		Capabilities: c.Capabilities,
		CreatedAt:    c.CreatedAt,
		LastError:    c.LastError,
	}
	if !c.LastSeen.IsZero() {
		seen := c.LastSeen
		out.LastSeen = &seen
	}
	return out
}

// openValue is one sealed value, opened. Nothing is sealed when it is empty, so
// an empty value is one that was never set.
func (s *Server) openValue(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	return s.secrets().Open(sealed)
}

// sealValue is the other direction, and empty stays empty rather than becoming
// the ciphertext of nothing.
func (s *Server) sealValue(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	return s.secrets().Seal(value)
}

// credential opens a registry's login.
func (s *Server) credential(reg state.Registry) (username, token string, err error) {
	if username, err = s.openValue(reg.SealedUsername); err != nil {
		return "", "", fmt.Errorf("registry %s's username: %w", reg.Name, err)
	}
	if token, err = s.openValue(reg.SealedSecret); err != nil {
		return "", "", fmt.Errorf("registry %s's token: %w", reg.Name, err)
	}
	return username, token, nil
}

// clusterClient is the client for one cluster's own access, kept between calls:
// the reconciler asks every few seconds, and a clientset built each time makes a
// new HTTP transport each time. The key is the sealed kubeconfig, so a rotation
// is a different client rather than a stale one, and the old one is left to the
// garbage collector along with its idle connections.
func (s *Server) clusterClient(c state.Cluster) (*k8s.Cluster, error) {
	key := c.Name + "\x00" + c.Namespace + "\x00" + string(c.Kubeconfig)
	s.clusters.Lock()
	if client, ok := s.clusters.byKey[key]; ok {
		s.clusters.Unlock()
		return client, nil
	}
	s.clusters.Unlock()
	kubeconfig, err := s.openValue(c.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("cluster %s's kubeconfig: %w", c.Name, err)
	}
	if kubeconfig == "" {
		return nil, fmt.Errorf("cluster %s has no kubeconfig: run agentbox cluster rotate", c.Name)
	}
	cluster, err := k8s.New(k8s.Config{Name: c.Name, Namespace: c.Namespace, Kubeconfig: []byte(kubeconfig)})
	if err != nil {
		return nil, err
	}
	s.clusters.Lock()
	defer s.clusters.Unlock()
	if s.clusters.byKey == nil {
		s.clusters.byKey = map[string]*k8s.Cluster{}
	}
	// One client per cluster, whatever it has been rotated to: the entries of
	// an older kubeconfig are dropped rather than kept for a rotation that
	// won't come back.
	for old := range s.clusters.byKey {
		if strings.HasPrefix(old, c.Name+"\x00") {
			delete(s.clusters.byKey, old)
		}
	}
	s.clusters.byKey[key] = cluster
	return cluster, nil
}

// clusterClients is the clients the daemon holds, by cluster and by the sealed
// kubeconfig they were built from.
type clusterClients struct {
	sync.Mutex
	byKey map[string]*k8s.Cluster
}

// writePullSecret writes a registry's login into a cluster's namespace, where a
// pod's image pull reads it. A registry with no login leaves nothing behind: the
// node pulls the image itself, as it pulls any public one.
func (s *Server) writePullSecret(ctx context.Context, c state.Cluster, reg state.Registry) error {
	username, token, err := s.credential(reg)
	if err != nil {
		return err
	}
	if token == "" {
		return nil
	}
	cluster, err := s.clusterClient(c)
	if err != nil {
		return err
	}
	secret, err := k8s.PullSecret(reg.Name, reg.URL, username, token)
	if err != nil {
		return err
	}
	if err := cluster.ApplyPullSecret(ctx, secret); err != nil {
		return fmt.Errorf("cluster %s: %w", c.Name, err)
	}
	return nil
}

// imageRef is the reference a pod pulls: the registry's prefix and the image's
// name under it, or the image alone when it was imported into the cluster.
func imageRef(c state.Cluster, reg *state.Registry) string {
	if reg == nil {
		return c.Image
	}
	return strings.TrimSuffix(reg.URL, "/") + "/" + c.Image
}

// registryFor is the registry a request names, and nil when it names none.
func (s *Server) registryFor(ctx context.Context, name string) (*state.Registry, error) {
	if name == "" {
		return nil, nil
	}
	reg, err := s.store.Registry(ctx, name)
	if err != nil {
		return nil, err
	}
	return &reg, nil
}

// pullSecretName is the secret a cluster's pods pull with, "" when its registry
// needs no login.
func (s *Server) pullSecretName(ctx context.Context, c state.Cluster) string {
	reg, err := s.registryFor(ctx, c.Registry)
	if err != nil || reg == nil || !reg.HasCredential() {
		return ""
	}
	return k8s.PullSecretName(reg.Name)
}

func (s *Server) listClusters(w http.ResponseWriter, r *http.Request) error {
	clusters, err := s.store.Clusters(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.Cluster, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, clusterInfo(c))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) getCluster(w http.ResponseWriter, r *http.Request) error {
	c, err := s.store.Cluster(r.Context(), r.PathValue("name"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, clusterInfo(c))
}

// addCluster applies AgentBox's access to a cluster with the admin kubeconfig the
// request carries, keeps the ServiceAccount's token sealed, and drops the
// admin's.
func (s *Server) addCluster(w http.ResponseWriter, r *http.Request) error {
	var req api.AddClusterRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	// Everything that can be refused is refused before the cluster is touched.
	if _, err := s.store.Cluster(ctx, req.Name); err == nil {
		return fmt.Errorf("cluster %s: %w", req.Name, state.ErrExists)
	} else if !errors.Is(err, state.ErrNotFound) {
		return err
	}
	reg, err := s.registryFor(ctx, req.Registry)
	if err != nil {
		return err
	}
	namespace := req.Namespace
	if namespace == "" {
		namespace = k8s.DefaultNamespace
	}
	result, err := k8s.Add(ctx, k8s.AddOptions{Name: req.Name, Namespace: namespace, Kubeconfig: []byte(req.Kubeconfig)})
	if err != nil {
		return err
	}
	sealed, err := s.sealValue(string(result.Kubeconfig))
	if err != nil {
		return err
	}
	cluster := state.Cluster{
		Name:         req.Name,
		Namespace:    namespace,
		StorageClass: req.StorageClass,
		Image:        req.Image,
		Registry:     req.Registry,
		MaxAgents:    req.MaxAgents,
		CPU:          req.CPU,
		Memory:       req.Memory,
		Labels:       req.Labels,
		Tailnet:      req.Tailnet,
		Capabilities: req.Capabilities,
		Kubeconfig:   sealed,
		CreatedAt:    time.Now(),
		LastSeen:     time.Now(),
	}
	if reg != nil {
		// Before the row: a cluster that can't pull its image is one the user
		// should be able to add again, and a row they have to remove first.
		if err := s.writePullSecret(ctx, cluster, *reg); err != nil {
			return fmt.Errorf("applying the pull secret: %w", err)
		}
	}
	if err := s.store.AddCluster(ctx, cluster); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, clusterInfo(cluster))
}

// setClusterConfig replaces what a change to a cluster may touch, and makes sure
// a new registry's login reached the namespace.
func (s *Server) setClusterConfig(w http.ResponseWriter, r *http.Request) error {
	var cfg api.ClusterConfig
	if err := readJSON(r, &cfg); err != nil {
		return err
	}
	ctx := r.Context()
	cluster, err := s.store.Cluster(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	reg, err := s.registryFor(ctx, cfg.Registry)
	if err != nil {
		return err
	}
	changedRegistry := cfg.Registry != cluster.Registry
	cluster.StorageClass = cfg.StorageClass
	cluster.Image = cfg.Image
	cluster.Registry = cfg.Registry
	cluster.MaxAgents = cfg.MaxAgents
	cluster.CPU = cfg.CPU
	cluster.Memory = cfg.Memory
	cluster.Labels = cfg.Labels
	cluster.Tailnet = cfg.Tailnet
	cluster.Capabilities = cfg.Capabilities
	if err := s.store.SetClusterConfig(ctx, cluster); err != nil {
		return err
	}
	if changedRegistry && reg != nil {
		if err := s.writePullSecret(ctx, cluster, *reg); err != nil {
			return fmt.Errorf("the cluster's config is saved, but its pull secret isn't: %w", err)
		}
	}
	return writeJSON(w, http.StatusOK, clusterInfo(cluster))
}

func (s *Server) removeCluster(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.RemoveCluster(r.Context(), r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// doctorCluster checks a cluster with the daemon's own access, and records what
// it found as the cluster's health.
func (s *Server) doctorCluster(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cluster, err := s.store.Cluster(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	client, err := s.clusterClient(cluster)
	if err != nil {
		return err
	}
	reg, err := s.registryFor(ctx, cluster.Registry)
	if err != nil {
		return err
	}
	report := k8s.Doctor{
		Cluster:      client,
		StorageClass: cluster.StorageClass,
		Image:        imageRef(cluster, reg),
		PullSecret:   s.pullSecretName(ctx, cluster),
	}.Run(ctx)
	health := ""
	if report.Failed() {
		health = firstFailure(report)
	}
	if err := s.store.SetClusterHealth(ctx, cluster.Name, time.Now(), health); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, clusterReport(report))
}

func clusterReport(report k8s.Report) api.ClusterReport {
	out := api.ClusterReport{Server: report.Server, OK: !report.Failed(), Checks: make([]api.ClusterCheck, 0, len(report.Checks))}
	for _, check := range report.Checks {
		out.Checks = append(out.Checks, api.ClusterCheck{Name: check.Name, OK: check.OK, Skipped: check.Skipped, Detail: check.Detail})
	}
	return out
}

// firstFailure is the one thing to keep as a cluster's health: the first check
// that failed, in the doctor's own order.
func firstFailure(report k8s.Report) string {
	for _, check := range report.Checks {
		if !check.OK && !check.Skipped {
			return check.Name + ": " + check.Detail
		}
	}
	return ""
}

// rotateCluster gives a cluster a new token with the admin kubeconfig the request
// carries, and keeps it sealed in place of the old one.
func (s *Server) rotateCluster(w http.ResponseWriter, r *http.Request) error {
	var req api.RotateClusterRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	cluster, err := s.store.Cluster(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	result, err := k8s.Rotate(ctx, k8s.AddOptions{Name: cluster.Name, Namespace: cluster.Namespace, Kubeconfig: []byte(req.Kubeconfig)})
	if err != nil {
		return err
	}
	sealed, err := s.sealValue(string(result.Kubeconfig))
	if err != nil {
		return err
	}
	if err := s.store.SetClusterKubeconfig(ctx, cluster.Name, sealed); err != nil {
		return err
	}
	cluster.Kubeconfig = sealed
	return writeJSON(w, http.StatusOK, clusterInfo(cluster))
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) error {
	registries, err := s.store.Registries(r.Context())
	if err != nil {
		return err
	}
	out := make([]api.Registry, 0, len(registries))
	for _, reg := range registries {
		out = append(out, registryInfo(reg))
	}
	return writeJSON(w, http.StatusOK, out)
}

func (s *Server) getRegistry(w http.ResponseWriter, r *http.Request) error {
	reg, err := s.store.Registry(r.Context(), r.PathValue("name"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, registryInfo(reg))
}

func (s *Server) addRegistry(w http.ResponseWriter, r *http.Request) error {
	var req api.AddRegistryRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	reg, err := s.registryFrom(req.Name, req.URL, req.Username, req.Token, req.Public, req.CA, req.Insecure)
	if err != nil {
		return err
	}
	if err := s.store.AddRegistry(r.Context(), reg); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, registryInfo(reg))
}

// registryFrom is a registry with its login sealed, the way both add and rotate
// write one.
func (s *Server) registryFrom(name, url, username, token string, public bool, ca string, insecure bool) (state.Registry, error) {
	if token == "" && username != "" {
		return state.Registry{}, errors.New("a registry login needs a token: pipe one in with --token-file, or leave the username out for a registry that needs no login")
	}
	sealedUsername, err := s.sealValue(username)
	if err != nil {
		return state.Registry{}, err
	}
	sealedToken, err := s.sealValue(token)
	if err != nil {
		return state.Registry{}, err
	}
	return state.Registry{
		Name:           name,
		URL:            url,
		SealedUsername: sealedUsername,
		SealedSecret:   sealedToken,
		Public:         public,
		CA:             []byte(ca),
		Insecure:       insecure,
		CreatedAt:      time.Now(),
	}, nil
}

func (s *Server) removeRegistry(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.RemoveRegistry(r.Context(), r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// testRegistry asks a registry whether it answers and whether the login it holds
// is accepted, and records the answer as its health.
func (s *Server) testRegistry(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	reg, err := s.store.Registry(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	username, token, err := s.credential(reg)
	if err != nil {
		return err
	}
	result := registry.Client{
		URL:      reg.URL,
		Username: username,
		Token:    token,
		CA:       reg.CA,
		Insecure: reg.Insecure,
	}.Check(ctx)
	health := ""
	if !result.OK {
		health = result.Detail
	}
	if err := s.store.SetRegistryHealth(ctx, reg.Name, time.Now(), health); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, api.RegistryTest{OK: result.OK, Detail: result.Detail})
}

// rotateRegistry replaces a registry's login, and rewrites the pull secret of
// every cluster that pulls from it.
func (s *Server) rotateRegistry(w http.ResponseWriter, r *http.Request) error {
	var req api.RotateRegistryRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	reg, err := s.store.Registry(ctx, r.PathValue("name"))
	if err != nil {
		return err
	}
	username, token := req.Username, req.Token
	if token == "" && username != "" {
		return errors.New("a registry login needs a token: pipe one in with --token-file, or leave the username out for a registry that needs no login")
	}
	sealedUsername, err := s.sealValue(username)
	if err != nil {
		return err
	}
	sealedToken, err := s.sealValue(token)
	if err != nil {
		return err
	}
	if err := s.store.SetRegistryCredential(ctx, reg.Name, sealedUsername, sealedToken); err != nil {
		return err
	}
	reg.SealedUsername, reg.SealedSecret = sealedUsername, sealedToken
	clusters, err := s.store.ClustersOfRegistry(ctx, reg.Name)
	if err != nil {
		return err
	}
	var failed []string
	for _, cluster := range clusters {
		if err := s.writePullSecret(ctx, cluster, reg); err != nil {
			failed = append(failed, cluster.Name+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the registry's login is rotated, but the pull secret couldn't be written for %s", strings.Join(failed, "; "))
	}
	return writeJSON(w, http.StatusOK, registryInfo(reg))
}

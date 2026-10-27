// Package k8s is a Kubernetes cluster as AgentBox uses it: a client built from
// the namespace-scoped kubeconfig `agentbox cluster add` sealed, and the
// objects AgentBox needs in that namespace to run agents as pods.
//
// The kubeconfig this package is handed is already open. Which cluster a call is
// about, and sealing its credential, belong to internal/state; nothing here
// reads state.db, so a test gives it a fake clientset and no cluster exists
// anywhere.
package k8s

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// DefaultNamespace is where AgentBox runs agents when a cluster doesn't
	// name another.
	DefaultNamespace = "agentbox"

	// defaultQPS and defaultBurst are above client-go's 5 and 10 because the
	// daemon handles every project here: it lists pods for all of them, and
	// boots several agents at once.
	defaultQPS   float32 = 50
	defaultBurst int     = 100
)

// Config is what New builds a Cluster from.
type Config struct {
	// Name is the cluster as the user named it: k3d, nas, hetzner.
	Name string
	// Namespace is where this cluster's agents run. Empty is DefaultNamespace.
	Namespace string
	// Kubeconfig is the kubeconfig `cluster add` sealed, opened, scoped to
	// the namespace's ServiceAccount rather than cluster-admin.
	Kubeconfig []byte
	// QPS and Burst cap the client's request rate. Zero takes the defaults.
	QPS   float32
	Burst int
}

// Cluster is one Kubernetes cluster's namespace, with the client that drives it.
type Cluster struct {
	name      string
	namespace string
	rest      *rest.Config
	client    k8sclient.Interface
}

// New builds the client for one cluster from its kubeconfig.
func New(cfg Config) (*Cluster, error) {
	if err := checkName(cfg.Name); err != nil {
		return nil, err
	}
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = DefaultNamespace
	}
	if problems := validation.IsDNS1123Label(namespace); len(problems) > 0 {
		return nil, fmt.Errorf("the namespace %q of cluster %s can't be used: %s", namespace, cfg.Name, strings.Join(problems, "; "))
	}
	if len(cfg.Kubeconfig) == 0 {
		return nil, fmt.Errorf("cluster %s has no kubeconfig: run agentbox cluster add again", cfg.Name)
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("the kubeconfig of cluster %s can't be read: %w", cfg.Name, err)
	}
	if err := normalize(cfg.Name, restConfig); err != nil {
		return nil, err
	}
	restConfig.QPS = cfg.QPS
	if restConfig.QPS == 0 {
		restConfig.QPS = defaultQPS
	}
	restConfig.Burst = cfg.Burst
	if restConfig.Burst == 0 {
		restConfig.Burst = defaultBurst
	}
	restConfig.UserAgent = "agentbox"
	client, err := k8sclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("cluster %s: %w", cfg.Name, err)
	}
	return &Cluster{name: cfg.Name, namespace: namespace, rest: restConfig, client: client}, nil
}

// FromClient is a Cluster over a clientset that already exists, which is how a
// test drives one against k8s.io/client-go/kubernetes/fake.
func FromClient(name, namespace string, client k8sclient.Interface) *Cluster {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	return &Cluster{name: name, namespace: namespace, client: client}
}

// Name is the cluster as the user named it.
func (c *Cluster) Name() string { return c.name }

// Namespace is where this cluster's agents run.
func (c *Cluster) Namespace() string { return c.namespace }

// Client is the typed clientset every operation here goes through.
func (c *Cluster) Client() k8sclient.Interface { return c.client }

// RESTConfig is the configuration the clientset was built from, for the calls
// that aren't in the clientset: a pod's exec, or a log stream. It is a copy, so
// a caller changing an APIPath doesn't change this cluster's.
func (c *Cluster) RESTConfig() (*rest.Config, error) {
	if c.rest == nil {
		return nil, errors.New("cluster " + c.name + " was built from a clientset, so it has no REST config")
	}
	return rest.CopyConfig(c.rest), nil
}

// checkName refuses what could never be a pod label: every object and pod this
// cluster holds is labelled with the cluster's name.
func checkName(name string) error {
	switch {
	case name == "":
		return errors.New("a cluster needs a name, like k3d")
	case len(name) > 63:
		return fmt.Errorf("the cluster name %q is longer than 63 characters, which no label can hold", name)
	}
	if problems := validation.IsValidLabelValue(name); len(problems) > 0 {
		return fmt.Errorf("the cluster name %q can't be a label: %s", name, strings.Join(problems, "; "))
	}
	return nil
}

// normalize rewrites a kubeconfig's server address into one this host can dial,
// and refuses an API server that isn't https. The token is sealed on the master
// (see internal/state), which is worth nothing if the request that follows
// carries it over http. client-go is worse than that: it drops a token mailed to
// a plain http server without saying so, and the 401 that follows has no reason
// in it.
func normalize(name string, restConfig *rest.Config) error {
	server, err := url.Parse(restConfig.Host)
	if err != nil {
		return fmt.Errorf("the server address %q of cluster %s isn't a URL: %w", restConfig.Host, name, err)
	}
	if server.Scheme != "https" || server.Host == "" {
		return fmt.Errorf("the API server of cluster %s is %q: AgentBox needs its https address, because the token it seals would travel in the clear", name, restConfig.Host)
	}
	// k3d publishes the API server on a wildcard and writes that address into
	// its kubeconfig, which kubectl accepts and a client can't dial.
	switch server.Hostname() {
	case "0.0.0.0":
		server.Host = net.JoinHostPort("127.0.0.1", server.Port())
	case "::":
		server.Host = net.JoinHostPort("::1", server.Port())
	default:
		return nil
	}
	restConfig.Host = server.String()
	return nil
}

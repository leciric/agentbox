package k8s

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	// TokenSecretName is the long-lived token Secret `cluster add` makes for the
	// ServiceAccount. A TokenRequest token would expire within the hour, and
	// refreshing it would mean keeping an admin kubeconfig or running a
	// token-minter in the cluster; this one is static, and `cluster rotate`
	// replaces it.
	TokenSecretName = "agentbox-token"
	// tokenWait is how long a token Secret has for the cluster's controller to
	// fill it in.
	tokenWait = 60 * time.Second
	// tokenPoll is how often that Secret is read again while it is empty.
	tokenPoll = 500 * time.Millisecond
)

// Admin is a cluster reached with the kubeconfig the user's kubectl uses, which
// is cluster-admin far too often. It exists for the length of one command:
// `cluster add` applies what AgentBox needs and makes itself a token of its own,
// and the admin kubeconfig is kept nowhere.
type Admin struct {
	client k8sclient.Interface
	rest   *rest.Config
}

// NewAdmin reads an admin kubeconfig: `~/.kube/config`, or whatever the user
// pointed at.
func NewAdmin(kubeconfig []byte) (*Admin, error) {
	restConfig, err := configFrom(kubeconfig)
	if err != nil {
		return nil, err
	}
	client, err := k8sclient.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return &Admin{client: client, rest: restConfig}, nil
}

// AdminFromClient is an Admin over a clientset that already exists, for a test
// driving one against k8s.io/client-go/kubernetes/fake. restConfig is what the
// kubeconfig it writes is built from: the API server's address and certificate
// authority.
func AdminFromClient(client k8sclient.Interface, restConfig *rest.Config) *Admin {
	return &Admin{client: client, rest: restConfig}
}

// Client is the admin clientset, for the reads only an admin can make: the
// nodes, and a storage class the namespaced token can't see.
func (a *Admin) Client() k8sclient.Interface { return a.client }

// Server is the API server the kubeconfig points at, as this host dials it.
func (a *Admin) Server() string { return a.rest.Host }

// AddAccess applies the namespace, the ServiceAccount, the Role and bindings
// that let it run agents, and the read-only ClusterRole placement needs. It is
// safe on a cluster AgentBox is already set up in.
func (a *Admin) AddAccess(ctx context.Context, namespace string) error {
	return NewAccess(namespace).Apply(ctx, a.client)
}

// Token is the ServiceAccount's token: the one already in its Secret, or a
// Secret that is made now. The cluster's controller fills it in, which is why
// this waits.
func (a *Admin) Token(ctx context.Context, namespace string) ([]byte, error) {
	secrets := a.client.CoreV1().Secrets(namespace)
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      TokenSecretName,
			Namespace: namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: ServiceAccountName,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	created, err := secrets.Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = secrets.Get(ctx, TokenSecretName, metav1.GetOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("the token Secret %s/%s: %w", namespace, TokenSecretName, err)
	}
	deadline := time.Now().Add(tokenWait)
	for {
		if token := created.Data[corev1.ServiceAccountTokenKey]; len(token) > 0 {
			return token, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the token Secret %s/%s is still empty after %s: this cluster doesn't fill token Secrets in, so make one by hand with kubectl create token",
				namespace, TokenSecretName, tokenWait)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(tokenPoll):
		}
		if created, err = secrets.Get(ctx, TokenSecretName, metav1.GetOptions{}); err != nil {
			return nil, fmt.Errorf("reading the token Secret %s/%s: %w", namespace, TokenSecretName, err)
		}
	}
}

// RotateToken replaces the ServiceAccount's token: the old Secret is deleted and
// a new one made. Anything holding the old token stops being able to reach the
// cluster, which is the point.
func (a *Admin) RotateToken(ctx context.Context, namespace string) ([]byte, error) {
	err := a.client.CoreV1().Secrets(namespace).Delete(ctx, TokenSecretName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("deleting the old token Secret %s/%s: %w", namespace, TokenSecretName, err)
	}
	return a.Token(ctx, namespace)
}

// Kubeconfig is the kubeconfig one of those tokens opens the namespace with: the
// same API server and certificate authority the admin kubeconfig named, the
// namespace, and the token. It is what the daemon seals.
func (a *Admin) Kubeconfig(namespace string, token []byte) ([]byte, error) {
	if len(token) == 0 {
		return nil, errors.New("a kubeconfig needs the ServiceAccount's token, and there is none")
	}
	ca, err := a.caData()
	if err != nil {
		return nil, err
	}
	const name = "agentbox"
	return clientcmd.Write(clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{name: {
			Server:                   a.rest.Host,
			CertificateAuthorityData: ca,
			InsecureSkipTLSVerify:    a.rest.Insecure,
		}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{name: {Token: string(token)}},
		Contexts:       map[string]*clientcmdapi.Context{name: {Cluster: name, AuthInfo: name, Namespace: namespace}},
		CurrentContext: name,
	})
}

// caData is the certificate authority the admin kubeconfig trusts the API server
// with: its bytes, the file it names, or none when the config skips verification
// or the cluster's certificate is from a public authority.
func (a *Admin) caData() ([]byte, error) {
	tls := a.rest.TLSClientConfig
	switch {
	case len(tls.CAData) > 0:
		return tls.CAData, nil
	case tls.CAFile != "":
		ca, err := os.ReadFile(tls.CAFile)
		if err != nil {
			return nil, fmt.Errorf("the kubeconfig's certificate authority: %w", err)
		}
		return ca, nil
	}
	return nil, nil
}

// AddOptions is what `cluster add` and `cluster rotate` need: the cluster as the
// user named it, the namespace to run agents in, and the admin kubeconfig to
// reach it with once.
type AddOptions struct {
	Name       string
	Namespace  string
	Kubeconfig []byte
}

// AddResult is what a successful add leaves behind: the kubeconfig of the
// namespace's ServiceAccount, which the daemon seals, and where the cluster
// answered.
type AddResult struct {
	Kubeconfig []byte
	Server     string
}

// Add applies AgentBox's access to a cluster, makes it a token of its own, and
// returns the kubeconfig that token opens the namespace with.
func Add(ctx context.Context, opts AddOptions) (AddResult, error) {
	return withAdmin(ctx, opts, func(admin *Admin) ([]byte, error) {
		if err := admin.AddAccess(ctx, opts.Namespace); err != nil {
			return nil, err
		}
		return admin.Token(ctx, opts.Namespace)
	})
}

// Rotate replaces the token of a cluster AgentBox already drives. The Role and
// the namespace's objects are untouched: agents running there keep running, and
// only the credential the daemon holds changes.
func Rotate(ctx context.Context, opts AddOptions) (AddResult, error) {
	return withAdmin(ctx, opts, func(admin *Admin) ([]byte, error) {
		return admin.RotateToken(ctx, opts.Namespace)
	})
}

// withAdmin is the shape both commands have: reach the cluster with the admin
// kubeconfig, do what the command does, and write the kubeconfig for the token it
// ended with.
func withAdmin(ctx context.Context, opts AddOptions, do func(*Admin) ([]byte, error)) (AddResult, error) {
	admin, err := NewAdmin(opts.Kubeconfig)
	if err != nil {
		return AddResult{}, fmt.Errorf("cluster %s: %w", opts.Name, err)
	}
	token, err := do(admin)
	if err != nil {
		return AddResult{}, fmt.Errorf("cluster %s: %w", opts.Name, err)
	}
	kubeconfig, err := admin.Kubeconfig(opts.Namespace, token)
	if err != nil {
		return AddResult{}, fmt.Errorf("cluster %s: %w", opts.Name, err)
	}
	return AddResult{Kubeconfig: kubeconfig, Server: admin.Server()}, nil
}

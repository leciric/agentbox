package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/api"
	"agentbox/internal/k8s"
)

// rotateFixture is a registry with a login and a cluster that pulls from it,
// its pull secret already in the cluster's namespace.
func rotateFixture(t *testing.T) (*Server, *fake.Clientset) {
	t.Helper()
	srv := reconcilerServer(t)
	ctx := context.Background()
	reg, err := srv.registryFrom("forgejo", "hub.example.com/you", "you", "old-token", false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddRegistry(ctx, reg); err != nil {
		t.Fatal(err)
	}
	secret, err := k8s.PullSecret(reg.Name, reg.URL, "you", "old-token")
	if err != nil {
		t.Fatal(err)
	}
	secret.Namespace = "agentbox"
	client := fake.NewClientset(secret)
	cluster := reconcilerCluster()
	cluster.Registry = reg.Name
	clusterWithFakeClient(t, srv, cluster, client)
	return srv, client
}

func rotate(t *testing.T, srv *Server, req api.RotateRegistryRequest) error {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/registries/forgejo/rotate", strings.NewReader(string(body)))
	r.SetPathValue("name", "forgejo")
	return srv.rotateRegistry(httptest.NewRecorder(), r)
}

func storedLogin(t *testing.T, srv *Server) (string, string) {
	t.Helper()
	reg, err := srv.store.Registry(context.Background(), "forgejo")
	if err != nil {
		t.Fatal(err)
	}
	username, token, err := srv.credential(reg)
	if err != nil {
		t.Fatal(err)
	}
	return username, token
}

func pullSecret(t *testing.T, client *fake.Clientset) (*corev1.Secret, error) {
	t.Helper()
	return client.CoreV1().Secrets("agentbox").Get(context.Background(), k8s.PullSecretName("forgejo"), metav1.GetOptions{})
}

// TestRegistryRotateWithNoTokenIsRefused is `registry rotate forgejo` with no
// flags: it used to store an empty login, leave the old pull secret and say
// "Rotated". Now nothing changes and the error names both ways forward.
func TestRegistryRotateWithNoTokenIsRefused(t *testing.T) {
	srv, client := rotateFixture(t)
	for _, req := range []api.RotateRegistryRequest{{}, {Username: "you"}} {
		err := rotate(t, srv, req)
		if err == nil || !strings.Contains(err.Error(), "--token-stdin") || !strings.Contains(err.Error(), "--no-login") {
			t.Fatalf("rotate(%+v) = %v, want a refusal naming --token-stdin and --no-login", req, err)
		}
	}
	if username, token := storedLogin(t, srv); username != "you" || token != "old-token" {
		t.Errorf("the stored login = %q/%q, want the old one kept", username, token)
	}
	if _, err := pullSecret(t, client); err != nil {
		t.Errorf("the pull secret: %v, want it kept", err)
	}
}

// TestRegistryRotateNoLoginDeletesThePullSecrets is the explicit way to a
// registry that needs no login: the login is gone, and so is every cluster's
// pull secret, which would otherwise keep pulling with the old one.
func TestRegistryRotateNoLoginDeletesThePullSecrets(t *testing.T) {
	srv, client := rotateFixture(t)
	if err := rotate(t, srv, api.RotateRegistryRequest{NoLogin: true}); err != nil {
		t.Fatalf("rotate(no login) = %v", err)
	}
	if username, token := storedLogin(t, srv); username != "" || token != "" {
		t.Errorf("the stored login = %q/%q, want none", username, token)
	}
	if _, err := pullSecret(t, client); !apierrors.IsNotFound(err) {
		t.Errorf("the pull secret: %v, want it deleted", err)
	}
	// A second removal finds nothing to delete, and that is not an error.
	if err := rotate(t, srv, api.RotateRegistryRequest{NoLogin: true}); err != nil {
		t.Errorf("rotate(no login) again = %v", err)
	}
	if err := rotate(t, srv, api.RotateRegistryRequest{NoLogin: true, Token: "t"}); err == nil {
		t.Error("rotate(no login with a token) = nil, want a refusal")
	}
}

func TestRegistryRotateRewritesThePullSecret(t *testing.T) {
	srv, client := rotateFixture(t)
	if err := rotate(t, srv, api.RotateRegistryRequest{Username: "you", Token: "new-token"}); err != nil {
		t.Fatalf("rotate() = %v", err)
	}
	if _, token := storedLogin(t, srv); token != "new-token" {
		t.Errorf("the stored token = %q, want new-token", token)
	}
	secret, err := pullSecret(t, client)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secret.Data[corev1.DockerConfigJsonKey]), "eW91Om5ldy10b2tlbg") { // base64 "you:new-token"
		t.Errorf("the pull secret = %s, want the new login in it", secret.Data[corev1.DockerConfigJsonKey])
	}
}

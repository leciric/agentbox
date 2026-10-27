package k8s_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"

	"agentbox/internal/k8s"
)

// fakeAdmin is an Admin whose cluster fills its token Secrets in, which is what
// a real controller manager does: the fake clientset only stores what it is
// given, so the reactor plays the controller's part.
func fakeAdmin(t *testing.T) (*fake.Clientset, *k8s.Admin) {
	t.Helper()
	client := fake.NewClientset()
	tokens := 0
	client.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		secret, ok := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		if !ok || secret.Type != corev1.SecretTypeServiceAccountToken {
			return false, nil, nil
		}
		tokens++
		secret.Data = map[string][]byte{corev1.ServiceAccountTokenKey: []byte(fmt.Sprintf("token-%d", tokens))}
		return false, nil, nil
	})
	return client, k8s.AdminFromClient(client, &rest.Config{
		Host:            "https://10.0.0.5:6443",
		TLSClientConfig: rest.TLSClientConfig{CAData: []byte("the cluster's CA")},
	})
}

func TestAddAccessCreatesWhatTheDaemonActsAs(t *testing.T) {
	client, admin := fakeAdmin(t)
	ctx := context.Background()
	if err := admin.AddAccess(ctx, "agentbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().ServiceAccounts("agentbox").Get(ctx, k8s.ServiceAccountName, getOptions()); err != nil {
		t.Errorf("the ServiceAccount: %v", err)
	}
	if _, err := client.RbacV1().ClusterRoles().Get(ctx, k8s.ReaderName("agentbox"), getOptions()); err != nil {
		t.Errorf("the read-only ClusterRole: %v", err)
	}
	// Twice is what a re-run of `cluster add` does.
	if err := admin.AddAccess(ctx, "agentbox"); err != nil {
		t.Errorf("adding access again: %v", err)
	}
}

func TestTokenMakesASecretOfItsOwn(t *testing.T) {
	client, admin := fakeAdmin(t)
	ctx := context.Background()
	token, err := admin.Token(ctx, "agentbox")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(token), "token-1"; got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
	secret, err := client.CoreV1().Secrets("agentbox").Get(ctx, k8s.TokenSecretName, getOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := secret.Annotations[corev1.ServiceAccountNameKey], k8s.ServiceAccountName; got != want {
		t.Errorf("the token Secret is for %q, want %q", got, want)
	}
	if secret.Type != corev1.SecretTypeServiceAccountToken {
		t.Errorf("the token Secret is of type %q", secret.Type)
	}
	// A second call reads the token that is there rather than making another.
	again, err := admin.Token(ctx, "agentbox")
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(token) {
		t.Errorf("the second token = %q, want the first, %q", again, token)
	}
}

func TestRotateTokenReplacesTheOneTheDaemonHolds(t *testing.T) {
	_, admin := fakeAdmin(t)
	ctx := context.Background()
	first, err := admin.Token(ctx, "agentbox")
	if err != nil {
		t.Fatal(err)
	}
	second, err := admin.RotateToken(ctx, "agentbox")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Errorf("rotating kept the same token %q", first)
	}
	if string(second) != "token-2" {
		t.Errorf("rotated token = %q, want %q", second, "token-2")
	}
}

func TestKubeconfigIsScopedToTheNamespace(t *testing.T) {
	_, admin := fakeAdmin(t)
	got, err := admin.Kubeconfig("agents", []byte("the-token"))
	if err != nil {
		t.Fatal(err)
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(got)
	if err != nil {
		t.Fatalf("the kubeconfig it wrote can't be read: %v\n%s", err, got)
	}
	if restConfig.Host != "https://10.0.0.5:6443" {
		t.Errorf("server = %q, want the admin kubeconfig's", restConfig.Host)
	}
	if restConfig.BearerToken != "the-token" {
		t.Errorf("token = %q, want the ServiceAccount's", restConfig.BearerToken)
	}
	if string(restConfig.CAData) != "the cluster's CA" {
		t.Errorf("CA = %q, want the admin kubeconfig's", restConfig.CAData)
	}
	// The context is what names the namespace, and it is the one New reads.
	written, err := clientcmd.Load(got)
	if err != nil {
		t.Fatal(err)
	}
	if got := written.Contexts[written.CurrentContext].Namespace; got != "agents" {
		t.Errorf("namespace = %q, want agents", got)
	}
	if _, err := admin.Kubeconfig("agents", nil); err == nil {
		t.Error("a kubeconfig with no token was written")
	}
}

func TestKubeconfigKeepsASkippedCertificateCheck(t *testing.T) {
	admin := k8s.AdminFromClient(fake.NewClientset(), &rest.Config{
		Host:            "https://10.0.0.5:6443",
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	})
	got, err := admin.Kubeconfig("agentbox", []byte("the-token"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "insecure-skip-tls-verify: true") {
		t.Errorf("the kubeconfig lost the skipped check:\n%s", got)
	}
}

package k8s_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

// TestAddAccessLabelsItsNamespaceForPodSecurity is what keeps the sealed
// credential, which can create pods, from creating a privileged one.
func TestAddAccessLabelsItsNamespaceForPodSecurity(t *testing.T) {
	client, admin := fakeAdmin(t)
	ctx := context.Background()
	if err := admin.AddAccess(ctx, "agentbox"); err != nil {
		t.Fatal(err)
	}
	namespace, err := client.CoreV1().Namespaces().Get(ctx, "agentbox", getOptions())
	if err != nil {
		t.Fatal(err)
	}
	// Baseline, not restricted: the init container that chowns a claim runs as root.
	if got := namespace.Labels[k8s.LabelPodSecurity]; got != "baseline" {
		t.Errorf("%s = %q, want baseline", k8s.LabelPodSecurity, got)
	}
	if got := namespace.Labels[k8s.LabelManaged]; got != "true" {
		t.Errorf("%s = %q, want true", k8s.LabelManaged, got)
	}
}

// TestAddAccessLabelsANamespaceItMadeBefore is a cluster added before the label:
// `cluster rotate` applies the access again, and the namespace is AgentBox's.
func TestAddAccessLabelsANamespaceItMadeBefore(t *testing.T) {
	client, admin := fakeAdmin(t)
	ctx := context.Background()
	old := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "agentbox", Labels: map[string]string{k8s.LabelManaged: "true"}}}
	if _, err := client.CoreV1().Namespaces().Create(ctx, old, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := admin.AddAccess(ctx, "agentbox"); err != nil {
		t.Fatal(err)
	}
	namespace, err := client.CoreV1().Namespaces().Get(ctx, "agentbox", getOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got := namespace.Labels[k8s.LabelPodSecurity]; got != "baseline" {
		t.Errorf("%s = %q, want baseline", k8s.LabelPodSecurity, got)
	}
}

// TestAddAccessNeverRelabelsAForeignNamespace is `cluster add --namespace
// default`: AgentBox didn't make it, so it is refused rather than relabelled,
// and nothing is applied in it. One that already enforces pod security is fine.
func TestAddAccessNeverRelabelsAForeignNamespace(t *testing.T) {
	client, admin := fakeAdmin(t)
	ctx := context.Background()
	foreign := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", Labels: map[string]string{"team": "web"}}}
	if _, err := client.CoreV1().Namespaces().Create(ctx, foreign, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	err := admin.AddAccess(ctx, "default")
	if !errors.Is(err, k8s.ErrForeignNamespace) {
		t.Fatalf("adding access to default = %v, want ErrForeignNamespace", err)
	}
	if !strings.Contains(err.Error(), "kubectl label namespace default pod-security.kubernetes.io/enforce=baseline") {
		t.Errorf("the error = %q, want the command that labels it", err)
	}
	namespace, err := client.CoreV1().Namespaces().Get(ctx, "default", getOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(namespace.Labels) != 1 || namespace.Labels["team"] != "web" {
		t.Errorf("the foreign namespace's labels = %v, want them untouched", namespace.Labels)
	}
	if _, err := client.CoreV1().ServiceAccounts("default").Get(ctx, k8s.ServiceAccountName, getOptions()); err == nil {
		t.Error("a ServiceAccount was made in a namespace that was refused")
	}

	// The owner labels it themselves; then it is accepted, and left as they set it.
	namespace.Labels[k8s.LabelPodSecurity] = "restricted"
	if _, err := client.CoreV1().Namespaces().Update(ctx, namespace, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := admin.AddAccess(ctx, "default"); err != nil {
		t.Fatalf("adding access to a labelled foreign namespace: %v", err)
	}
	namespace, err = client.CoreV1().Namespaces().Get(ctx, "default", getOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got := namespace.Labels[k8s.LabelPodSecurity]; got != "restricted" {
		t.Errorf("%s = %q, want the owner's restricted", k8s.LabelPodSecurity, got)
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
	secret, err := client.CoreV1().Secrets("agentbox").Get(ctx, k8s.ServiceAccountTokenSecret, getOptions())
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

// TestRotateTokenInvalidatesEveryCredentialOfTheServiceAccount is the point of
// rotation: not just the Secret AgentBox made, but the ServiceAccount itself and
// every token Secret pointing at it, including one the ServiceAccount created
// for itself.
func TestRotateTokenInvalidatesEveryCredentialOfTheServiceAccount(t *testing.T) {
	client, admin := fakeAdmin(t)
	// A real API server gives each ServiceAccount its own UID, and that UID is
	// what a token carries: a new one is what makes the old tokens invalid.
	var uids int
	client.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		account := action.(k8stesting.CreateAction).GetObject().(*corev1.ServiceAccount)
		uids++
		account.UID = types.UID(fmt.Sprintf("uid-%d", uids))
		return false, nil, nil
	})
	ctx := context.Background()
	if err := admin.AddAccess(ctx, "agentbox"); err != nil {
		t.Fatal(err)
	}
	before, err := client.CoreV1().ServiceAccounts("agentbox").Get(ctx, k8s.ServiceAccountName, getOptions())
	if err != nil {
		t.Fatal(err)
	}
	first, err := admin.Token(ctx, "agentbox")
	if err != nil {
		t.Fatal(err)
	}
	// A second token Secret the ServiceAccount made for itself: the old
	// rotation left it, and the new ServiceAccount would have adopted it.
	ownToken := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "agentbox-own-token",
			Namespace:   "agentbox",
			Annotations: map[string]string{corev1.ServiceAccountNameKey: k8s.ServiceAccountName},
		},
		Type: corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{corev1.ServiceAccountTokenKey: []byte("a-token-the-account-made")},
	}
	if _, err := client.CoreV1().Secrets("agentbox").Create(ctx, ownToken, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	// Another ServiceAccount's token is not this one's to rotate.
	otherToken := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "another-account-token",
			Namespace:   "agentbox",
			Annotations: map[string]string{corev1.ServiceAccountNameKey: "someone-else"},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	if _, err := client.CoreV1().Secrets("agentbox").Create(ctx, otherToken, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	second, err := admin.RotateToken(ctx, "agentbox")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Errorf("rotating kept the same token %q", first)
	}

	after, err := client.CoreV1().ServiceAccounts("agentbox").Get(ctx, k8s.ServiceAccountName, getOptions())
	if err != nil {
		t.Fatalf("the ServiceAccount was not made again: %v", err)
	}
	if before.UID == after.UID {
		t.Errorf("the ServiceAccount kept UID %q, so an old token is still valid", after.UID)
	}

	secrets, err := client.CoreV1().Secrets("agentbox").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if secret.Annotations[corev1.ServiceAccountNameKey] != k8s.ServiceAccountName {
			continue
		}
		if secret.Name == k8s.ServiceAccountTokenSecret {
			continue
		}
		t.Errorf("the token Secret %q of the old ServiceAccount survived rotation", secret.Name)
	}
	if _, err := client.CoreV1().Secrets("agentbox").Get(ctx, k8s.ServiceAccountTokenSecret, getOptions()); err != nil {
		t.Errorf("the new token Secret is missing: %v", err)
	}
	if _, err := client.CoreV1().Secrets("agentbox").Get(ctx, "another-account-token", getOptions()); err != nil {
		t.Errorf("rotation deleted another ServiceAccount's token: %v", err)
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

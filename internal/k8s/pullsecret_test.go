package k8s_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/k8s"
)

// dockerConfig is a pull secret's data, decoded.
func dockerConfig(t *testing.T, secret *corev1.Secret) map[string]map[string]string {
	t.Helper()
	var config struct {
		Auths map[string]map[string]string `json:"auths"`
	}
	if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil {
		t.Fatalf("the pull secret isn't a docker config: %v", err)
	}
	return config.Auths
}

func TestPullSecretFilesTheLoginUnderTheHost(t *testing.T) {
	secret, err := k8s.PullSecret("forgejo", "hub.example.com/h4ad/", "h4ad", "a-token")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := secret.Name, "agentbox-forgejo-pull"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("type = %q, want a docker config", secret.Type)
	}
	auths := dockerConfig(t, secret)
	// The host, not the repository prefix: that is how a node looks a
	// credential up for the image it pulls.
	entry, ok := auths["hub.example.com"]
	if !ok {
		t.Fatalf("the config files the login under %v, want hub.example.com", auths)
	}
	want := base64.StdEncoding.EncodeToString([]byte("h4ad:a-token"))
	if entry["auth"] != want {
		t.Errorf("auth = %q, want %q", entry["auth"], want)
	}
}

func TestPullSecretGoesIntoTheNamespace(t *testing.T) {
	client := fake.NewClientset()
	cluster := k8s.FromClient("k3d", "agents", client)
	ctx := context.Background()
	first, err := k8s.PullSecret("forgejo", "hub.example.com/h4ad", "h4ad", "first-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := cluster.ApplyPullSecret(ctx, first); err != nil {
		t.Fatal(err)
	}
	live, err := client.CoreV1().Secrets("agents").Get(ctx, first.Name, getOptions())
	if err != nil {
		t.Fatalf("the secret isn't in the cluster's namespace: %v", err)
	}
	if auths := dockerConfig(t, live); auths["hub.example.com"]["auth"] == "" {
		t.Errorf("the secret in the cluster has no login: %v", auths)
	}
	// A rotated login replaces it rather than making a second one.
	second, err := k8s.PullSecret("forgejo", "hub.example.com/h4ad", "h4ad", "second-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := cluster.ApplyPullSecret(ctx, second); err != nil {
		t.Fatal(err)
	}
	secrets, err := client.CoreV1().Secrets("agents").List(ctx, listOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets.Items) != 1 {
		t.Errorf("the namespace has %d secrets, want the one pull secret", len(secrets.Items))
	}
	live, _ = client.CoreV1().Secrets("agents").Get(ctx, second.Name, getOptions())
	want := base64.StdEncoding.EncodeToString([]byte("h4ad:second-token"))
	if got := dockerConfig(t, live)["hub.example.com"]["auth"]; got != want {
		t.Errorf("auth = %q, want the rotated one", got)
	}
}

func TestPullSecretNeedsAHost(t *testing.T) {
	if _, err := k8s.PullSecret("forgejo", "/h4ad", "h4ad", "token"); err == nil {
		t.Error("a registry url with no host made a pull secret")
	}
}

package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PullSecretName is the secret a namespace pulls one registry's images with. It
// carries the registry's name, so a cluster that moves to another registry leaves
// the old login in place rather than overwriting it with nothing, and the user
// can still pull the images that are up there.
func PullSecretName(registry string) string { return "agentbox-" + registry + "-pull" }

// PullSecret is the docker config a node reads to pull one registry's images: the
// registry's host, and the login for it, base64'd the way a docker config holds
// it. It never leaves the cluster's namespace, and it is the one copy of the
// credential outside this machine.
func PullSecret(registry, url, username, token string) (*corev1.Secret, error) {
	host, err := pullKey(url)
	if err != nil {
		return nil, err
	}
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
	body, err := json.Marshal(map[string]any{
		"auths": map[string]map[string]string{host: {"auth": auth}},
	})
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   PullSecretName(registry),
			Labels: map[string]string{LabelManaged: "true"},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: body},
	}, nil
}

// pullKey is what a docker config files a login under: the registry's host. A
// node looks a credential up by the host of the image it pulls, not by the
// repository prefix AgentBox pushes under.
func pullKey(url string) (string, error) {
	host, _, _ := strings.Cut(strings.TrimSuffix(strings.TrimSpace(url), "/"), "/")
	if host == "" {
		return "", fmt.Errorf("the registry url %q has no host", url)
	}
	return host, nil
}

// ApplyPullSecret creates or replaces the pull secret in the cluster's namespace.
func (c *Cluster) ApplyPullSecret(ctx context.Context, secret *corev1.Secret) error {
	secret.Namespace = c.namespace
	secrets := c.client.CoreV1().Secrets(c.namespace)
	live, err := secrets.Get(ctx, secret.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the pull secret %s: %w", secret.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the pull secret %s: %w", secret.Name, err)
	}
	secret.ResourceVersion = live.ResourceVersion
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the pull secret %s: %w", secret.Name, err)
	}
	return nil
}

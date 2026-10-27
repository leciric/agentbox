package k8s

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

const (
	// ServiceAccountName is the identity the master drives a cluster with. The
	// kubeconfig `cluster add` seals is this ServiceAccount's token, never the
	// admin's, so nothing outside this namespace is reachable with it.
	ServiceAccountName = "agentbox"
	// RoleName is the Role that identity is bound to.
	RoleName = "agentbox"
	// LabelManaged marks the objects AgentBox owns in a cluster.
	LabelManaged = "agentbox/managed"
)

// Access is what AgentBox needs in a cluster: the namespace its agents run in,
// the ServiceAccount the master acts as, a Role and RoleBinding that let it run
// agents as pods and nothing else, and one read-only ClusterRole for the
// cluster-scoped things placement and the split-DNS check cannot do without.
//
// There is no cluster-admin and no write outside the namespace. Node, storage
// class and DNSConfig reads are the exception a namespaced Role cannot grant:
// how much room a cluster has, whether the storage class agents ask for exists,
// and where the operator sends a tailnet name are properties of the cluster
// itself.
type Access struct {
	Namespace      *corev1.Namespace
	ServiceAccount *corev1.ServiceAccount
	Role           *rbacv1.Role
	RoleBinding    *rbacv1.RoleBinding
	Reader         *rbacv1.ClusterRole
	ReaderBinding  *rbacv1.ClusterRoleBinding
}

// ReaderName is the read-only ClusterRole's name. It carries the namespace
// because it is cluster-scoped: two AgentBox installs, or two clusters in one
// Kubernetes cluster, must not overwrite each other's.
func ReaderName(namespace string) string { return "agentbox-" + namespace + "-reader" }

// NewAccess is the objects `cluster add` applies, for the namespace the
// cluster's agents run in.
func NewAccess(namespace string) Access {
	labels := map[string]string{LabelManaged: "true"}
	return Access{
		Namespace: &corev1.Namespace{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
			ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: labels},
		},
		ServiceAccount: &corev1.ServiceAccount{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
			ObjectMeta: metav1.ObjectMeta{Name: ServiceAccountName, Namespace: namespace, Labels: labels},
		},
		Role: &rbacv1.Role{
			TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"},
			ObjectMeta: metav1.ObjectMeta{Name: RoleName, Namespace: namespace, Labels: labels},
			Rules:      agentRules(),
		},
		RoleBinding: &rbacv1.RoleBinding{
			TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
			ObjectMeta: metav1.ObjectMeta{Name: RoleName, Namespace: namespace, Labels: labels},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     RoleName,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      ServiceAccountName,
				Namespace: namespace,
			}},
		},
		Reader: &rbacv1.ClusterRole{
			TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
			ObjectMeta: metav1.ObjectMeta{Name: ReaderName(namespace), Labels: labels},
			Rules:      readerRules(),
		},
		ReaderBinding: &rbacv1.ClusterRoleBinding{
			TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
			ObjectMeta: metav1.ObjectMeta{Name: ReaderName(namespace), Labels: labels},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     ReaderName(namespace),
			},
			Subjects: []rbacv1.Subject{{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      ServiceAccountName,
				Namespace: namespace,
			}},
		},
	}
}

// agentRules is every resource type the runtime touches, by verb. Nothing here
// names a resource by name, because create can't be limited that way: what keeps
// one cluster to AgentBox's own objects is the namespace, and the labels
// every object AgentBox makes carries.
func agentRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			// A machine is a pod: create, start, stop, delete, adopt.
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get", "list", "watch", "create", "patch", "delete"},
		},
		{
			// The bootstrap before the gateway is up, and a log a failed
			// start is diagnosed from.
			APIGroups: []string{""},
			Resources: []string{"pods/exec"},
			Verbs:     []string{"create"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods/log"},
			Verbs:     []string{"get"},
		},
		{
			// An agent's disk across restarts and rescheduling.
			APIGroups: []string{""},
			Resources: []string{"persistentvolumeclaims"},
			Verbs:     []string{"get", "list", "watch", "create", "patch", "delete"},
		},
		{
			// The registry pull secret, and one token per pod start.
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"get", "list", "create", "update", "delete"},
		},
		{
			// What the cluster and the app say happened to a pod. The page's
			// detail reads them too, which is why a cluster added before this
			// rule needs `cluster add` run again.
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"get", "list", "create", "patch"},
		},
		{
			// The policy that keeps anything in the namespace from reaching
			// an agent's pod. Nothing removes it, so no delete: a cluster
			// that is removed leaves one policy behind in a namespace the
			// user can delete.
			APIGroups: []string{"networking.k8s.io"},
			Resources: []string{"networkpolicies"},
			Verbs:     []string{"get", "create", "update"},
		},
	}
}

// readerRules is what placement needs that a Role cannot give: how much room
// the nodes have, what they are labelled, whether the storage class an agent
// asks for exists, and the operator's split DNS, which is cluster-scoped and
// which `cluster doctor` reads to say a tailnet name resolves. Read only.
func readerRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"storage.k8s.io"},
			Resources: []string{"storageclasses"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			// The split-DNS check: a DNSConfig is cluster-scoped, so the
			// namespace's Role can't grant it.
			APIGroups: []string{"tailscale.com"},
			Resources: []string{"dnsconfigs"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
}

// Apply creates the namespace, ServiceAccount, Role and RoleBinding, and
// replaces what it finds when it already exists, so that running it again
// corrects a Role an older AgentBox wrote. The namespace is only created: its
// finalizers belong to whatever else runs in it.
func (a Access) Apply(ctx context.Context, client k8sclient.Interface) error {
	if _, err := client.CoreV1().Namespaces().Create(ctx, a.Namespace, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating the namespace %s: %w", a.Namespace.Name, err)
	}
	if err := applyServiceAccount(ctx, client, a.ServiceAccount); err != nil {
		return err
	}
	if err := applyRole(ctx, client, a.Role); err != nil {
		return err
	}
	if err := applyRoleBinding(ctx, client, a.RoleBinding); err != nil {
		return err
	}
	if err := applyClusterRole(ctx, client, a.Reader); err != nil {
		return err
	}
	return applyClusterRoleBinding(ctx, client, a.ReaderBinding)
}

// YAML renders the objects as the documents a user applies by hand, for
// clusters where they would rather not hand AgentBox an admin kubeconfig.
func (a Access) YAML() (string, error) {
	var out strings.Builder
	for _, object := range []any{a.Namespace, a.ServiceAccount, a.Role, a.RoleBinding, a.Reader, a.ReaderBinding} {
		body, err := yaml.Marshal(object)
		if err != nil {
			return "", err
		}
		out.WriteString("---\n")
		out.Write(body)
	}
	return out.String(), nil
}

// The five apply functions below are the same create-or-replace spelled out once
// per kind: client-go's typed clients share no interface to write it once.

func applyServiceAccount(ctx context.Context, client k8sclient.Interface, want *corev1.ServiceAccount) error {
	accounts := client.CoreV1().ServiceAccounts(want.Namespace)
	live, err := accounts.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := accounts.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the ServiceAccount %s: %w", want.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the ServiceAccount %s: %w", want.Name, err)
	}
	want.ResourceVersion = live.ResourceVersion
	if _, err := accounts.Update(ctx, want, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the ServiceAccount %s: %w", want.Name, err)
	}
	return nil
}

func applyRole(ctx context.Context, client k8sclient.Interface, want *rbacv1.Role) error {
	roles := client.RbacV1().Roles(want.Namespace)
	live, err := roles.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := roles.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the Role %s: %w", want.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the Role %s: %w", want.Name, err)
	}
	want.ResourceVersion = live.ResourceVersion
	if _, err := roles.Update(ctx, want, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the Role %s: %w", want.Name, err)
	}
	return nil
}

func applyRoleBinding(ctx context.Context, client k8sclient.Interface, want *rbacv1.RoleBinding) error {
	bindings := client.RbacV1().RoleBindings(want.Namespace)
	live, err := bindings.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := bindings.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the RoleBinding %s: %w", want.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the RoleBinding %s: %w", want.Name, err)
	}
	// A binding's roleRef can't be changed, only its subjects, so an existing
	// binding that points somewhere else has to go.
	if live.RoleRef != want.RoleRef {
		if err := bindings.Delete(ctx, want.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting the RoleBinding %s that points at another Role: %w", want.Name, err)
		}
		if _, err := bindings.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the RoleBinding %s: %w", want.Name, err)
		}
		return nil
	}
	want.ResourceVersion = live.ResourceVersion
	if _, err := bindings.Update(ctx, want, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the RoleBinding %s: %w", want.Name, err)
	}
	return nil
}

func applyClusterRole(ctx context.Context, client k8sclient.Interface, want *rbacv1.ClusterRole) error {
	roles := client.RbacV1().ClusterRoles()
	live, err := roles.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := roles.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the ClusterRole %s: %w", want.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the ClusterRole %s: %w", want.Name, err)
	}
	want.ResourceVersion = live.ResourceVersion
	if _, err := roles.Update(ctx, want, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the ClusterRole %s: %w", want.Name, err)
	}
	return nil
}

func applyClusterRoleBinding(ctx context.Context, client k8sclient.Interface, want *rbacv1.ClusterRoleBinding) error {
	bindings := client.RbacV1().ClusterRoleBindings()
	live, err := bindings.Get(ctx, want.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := bindings.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the ClusterRoleBinding %s: %w", want.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the ClusterRoleBinding %s: %w", want.Name, err)
	}
	if live.RoleRef != want.RoleRef {
		if err := bindings.Delete(ctx, want.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting the ClusterRoleBinding %s that points at another ClusterRole: %w", want.Name, err)
		}
		if _, err := bindings.Create(ctx, want, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating the ClusterRoleBinding %s: %w", want.Name, err)
		}
		return nil
	}
	want.ResourceVersion = live.ResourceVersion
	if _, err := bindings.Update(ctx, want, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating the ClusterRoleBinding %s: %w", want.Name, err)
	}
	return nil
}

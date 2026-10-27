package k8s_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	"agentbox/internal/k8s"
)

// kubeconfig is a kubeconfig for one cluster, as `kubectl config view` would
// write one for a ServiceAccount token.
func kubeconfig(server string) []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- name: agentbox
  cluster:
    server: ` + server + `
    insecure-skip-tls-verify: true
users:
- name: agentbox
  user:
    token: the-service-account-token
contexts:
- name: agentbox
  context:
    cluster: agentbox
    user: agentbox
    namespace: agentbox
current-context: agentbox
`)
}

func TestNewReadsTheSealedKubeconfig(t *testing.T) {
	cluster, err := k8s.New(k8s.Config{Name: "k3d", Kubeconfig: kubeconfig("https://127.0.0.1:6443")})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cluster.Name(), "k3d"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := cluster.Namespace(), k8s.DefaultNamespace; got != want {
		t.Errorf("Namespace() = %q, want %q", got, want)
	}
	restConfig, err := cluster.RESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := restConfig.Host, "https://127.0.0.1:6443"; got != want {
		t.Errorf("Host = %q, want %q", got, want)
	}
	if got, want := restConfig.BearerToken, "the-service-account-token"; got != want {
		t.Errorf("BearerToken = %q, want %q", got, want)
	}
	if restConfig.QPS != 50 || restConfig.Burst != 100 {
		t.Errorf("QPS, Burst = %v, %d, want 50, 100", restConfig.QPS, restConfig.Burst)
	}
	// The copy is the caller's: changing it must not reach the cluster.
	restConfig.Host = "https://elsewhere.invalid"
	again, err := cluster.RESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := again.Host, "https://127.0.0.1:6443"; got != want {
		t.Errorf("Host after the caller changed its copy = %q, want %q", got, want)
	}
}

func TestNewKeepsTheNamespaceItIsGiven(t *testing.T) {
	cluster, err := k8s.New(k8s.Config{Name: "nas", Namespace: "agents", Kubeconfig: kubeconfig("https://10.0.0.5:6443")})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cluster.Namespace(), "agents"; got != want {
		t.Errorf("Namespace() = %q, want %q", got, want)
	}
}

func TestNewDialsTheAddressK3dWrites(t *testing.T) {
	for _, tc := range []struct{ server, want string }{
		{"https://0.0.0.0:43551", "https://127.0.0.1:43551"},
		{"https://[::]:43551", "https://[::1]:43551"},
		{"https://10.0.0.5:6443", "https://10.0.0.5:6443"},
	} {
		cluster, err := k8s.New(k8s.Config{Name: "k3d", Kubeconfig: kubeconfig(tc.server)})
		if err != nil {
			t.Fatalf("%s: %v", tc.server, err)
		}
		restConfig, err := cluster.RESTConfig()
		if err != nil {
			t.Fatal(err)
		}
		if restConfig.Host != tc.want {
			t.Errorf("%s: Host = %q, want %q", tc.server, restConfig.Host, tc.want)
		}
	}
}

func TestNewRefusesAKubeconfigItCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  k8s.Config
		wantErr string
	}{
		{
			name:    "no name",
			config:  k8s.Config{Kubeconfig: kubeconfig("https://127.0.0.1:6443")},
			wantErr: "a cluster needs a name",
		},
		{
			name:    "a name no label can hold",
			config:  k8s.Config{Name: "my cluster", Kubeconfig: kubeconfig("https://127.0.0.1:6443")},
			wantErr: "can't be a label",
		},
		{
			name:    "no kubeconfig",
			config:  k8s.Config{Name: "k3d"},
			wantErr: "no kubeconfig",
		},
		{
			name:    "not a kubeconfig",
			config:  k8s.Config{Name: "k3d", Kubeconfig: []byte("lol")},
			wantErr: "can't be read",
		},
		{
			name:    "a namespace that isn't a label",
			config:  k8s.Config{Name: "k3d", Namespace: "Agent Box", Kubeconfig: kubeconfig("https://127.0.0.1:6443")},
			wantErr: "can't be used",
		},
		{
			name:    "an API server over http",
			config:  k8s.Config{Name: "k3d", Kubeconfig: kubeconfig("http://127.0.0.1:6443")},
			wantErr: "https address",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := k8s.New(tc.config)
			if err == nil {
				t.Fatal("New() accepted it")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("New() = %v, want it to say %q", err, tc.wantErr)
			}
		})
	}
}

func TestApplyCreatesTheNamespaceIdentityAndRole(t *testing.T) {
	client := fake.NewClientset()
	access := k8s.NewAccess("agentbox")
	if err := access.Apply(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), "agentbox", getOptions()); err != nil {
		t.Errorf("the namespace: %v", err)
	}
	account, err := client.CoreV1().ServiceAccounts("agentbox").Get(context.Background(), k8s.ServiceAccountName, getOptions())
	if err != nil {
		t.Fatalf("the ServiceAccount: %v", err)
	}
	if got, want := account.Labels[k8s.LabelManaged], "true"; got != want {
		t.Errorf("%s = %q, want %q", k8s.LabelManaged, got, want)
	}
	role, err := client.RbacV1().Roles("agentbox").Get(context.Background(), k8s.RoleName, getOptions())
	if err != nil {
		t.Fatalf("the Role: %v", err)
	}
	if !reflect.DeepEqual(role.Rules, access.Role.Rules) {
		t.Errorf("the Role's rules = %+v, want %+v", role.Rules, access.Role.Rules)
	}
	binding, err := client.RbacV1().RoleBindings("agentbox").Get(context.Background(), k8s.RoleName, getOptions())
	if err != nil {
		t.Fatalf("the RoleBinding: %v", err)
	}
	if got, want := binding.RoleRef.Name, k8s.RoleName; got != want {
		t.Errorf("bound to %q, want %q", got, want)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Name != k8s.ServiceAccountName {
		t.Errorf("subjects = %+v, want only %s", binding.Subjects, k8s.ServiceAccountName)
	}
}

func TestApplyCorrectsWhatAnOlderAgentBoxWrote(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	stale := k8s.NewAccess("agentbox")
	stale.Role.Rules = []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}}
	// A binding pointing at another Role can't be updated, only replaced.
	stale.RoleBinding.RoleRef.Name = "old"
	if err := stale.Apply(ctx, client); err != nil {
		t.Fatal(err)
	}
	if err := k8s.NewAccess("agentbox").Apply(ctx, client); err != nil {
		t.Fatal(err)
	}
	role, err := client.RbacV1().Roles("agentbox").Get(ctx, k8s.RoleName, getOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := role.Rules, k8s.NewAccess("agentbox").Role.Rules; !reflect.DeepEqual(got, want) {
		t.Errorf("the Role still says %+v, want %+v", got, want)
	}
	binding, err := client.RbacV1().RoleBindings("agentbox").Get(ctx, k8s.RoleName, getOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := binding.RoleRef.Name, k8s.RoleName; got != want {
		t.Errorf("bound to %q, want %q", got, want)
	}
}

func TestApplyLeavesTheNamespaceAsItFoundIt(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	if err := k8s.NewAccess("agentbox").Apply(ctx, client); err != nil {
		t.Fatal(err)
	}
	// Something else in the namespace owns a finalizer on it.
	namespace, err := client.CoreV1().Namespaces().Get(ctx, "agentbox", getOptions())
	if err != nil {
		t.Fatal(err)
	}
	namespace.Spec.Finalizers = []corev1.FinalizerName{"example.com/keep-me"}
	if _, err := client.CoreV1().Namespaces().Update(ctx, namespace, updateOptions()); err != nil {
		t.Fatal(err)
	}
	if err := k8s.NewAccess("agentbox").Apply(ctx, client); err != nil {
		t.Fatal(err)
	}
	after, err := client.CoreV1().Namespaces().Get(ctx, "agentbox", getOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Spec.Finalizers) != 1 || after.Spec.Finalizers[0] != "example.com/keep-me" {
		t.Errorf("finalizers = %v, want the one that was there", after.Spec.Finalizers)
	}
}

func TestAccessYAMLIsWhatAKubectlUserApplies(t *testing.T) {
	access := k8s.NewAccess("agentbox")
	manifest, err := access.YAML()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, document := range strings.Split(manifest, "\n---\n") {
		if strings.TrimSpace(document) == "" {
			continue
		}
		var header struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
		}
		if err := yaml.Unmarshal([]byte(document), &header); err != nil {
			t.Fatalf("a document is not YAML: %v\n%s", err, document)
		}
		if header.APIVersion == "" || header.Kind == "" {
			t.Errorf("a document has no apiVersion or kind:\n%s", document)
		}
		kinds = append(kinds, header.Kind)
		if header.Kind != "Role" {
			continue
		}
		var role rbacv1.Role
		if err := yaml.Unmarshal([]byte(document), &role); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(role.Rules, access.Role.Rules) {
			t.Errorf("the Role document's rules = %+v, want %+v", role.Rules, access.Role.Rules)
		}
	}
	want := []string{"Namespace", "ServiceAccount", "Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("documents = %v, want %v", kinds, want)
	}
}

func TestFromClientHasNoKubeconfigToRead(t *testing.T) {
	cluster := k8s.FromClient("k3d", "", fake.NewClientset())
	if got, want := cluster.Namespace(), k8s.DefaultNamespace; got != want {
		t.Errorf("Namespace() = %q, want %q", got, want)
	}
	if _, err := cluster.RESTConfig(); err == nil {
		t.Error("RESTConfig() returned one for a cluster built from a clientset")
	}
}

func getOptions() metav1.GetOptions { return metav1.GetOptions{} }

func updateOptions() metav1.UpdateOptions { return metav1.UpdateOptions{} }

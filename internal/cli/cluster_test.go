package cli_test

// The names here stay short: each test's daemon listens on a socket under its own
// temporary HOME, and a unix socket path is 107 bytes at most.
import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRegistry is a registry that answers a bearer challenge, so `registry add`
// followed by `registry test` has something real to talk to.
func fakeRegistry(t *testing.T, username, token string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != username || pass != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"a-session"}`))
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer a-session" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+server.URL+`/token",service="fake"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestRegistryLifecycle(t *testing.T) {
	isolate(t)
	startDaemon(t)
	server := fakeRegistry(t, "me", "a-token")
	url := strings.TrimPrefix(server.URL, "http://") + "/me"

	out, err := run(t, "a-token\n", "registry", "add", "local", "--url", url,
		"--username", "me", "--token-stdin", "--insecure")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Added registry local ("+url+")", "private")

	out, err = run(t, "", "registry", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "local", url, "private", "sealed", "never")

	out, err = run(t, "", "registry", "test", "local")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "local:", "accepted the login")

	// The health of the test is in the list, not only in the answer.
	out, err = run(t, "", "registry", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "just now")

	if _, err := run(t, "", "registry", "remove", "local"); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "", "registry", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No registries")
}

func TestRegistryWrongLogin(t *testing.T) {
	isolate(t)
	startDaemon(t)
	server := fakeRegistry(t, "me", "a-token")
	url := strings.TrimPrefix(server.URL, "http://") + "/me"
	if _, err := run(t, "the-wrong-token\n", "registry", "add", "local", "--url", url,
		"--username", "me", "--token-stdin", "--insecure"); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "", "registry", "test", "local")
	if err == nil || !strings.Contains(err.Error(), "refused the stored login") {
		t.Errorf("registry test with a wrong token = %v, want it to say the login was refused", err)
	}
	if err != nil && strings.Contains(err.Error(), "the-wrong-token") {
		t.Errorf("the error quotes the credential: %v", err)
	}
}

func TestRegistryTakesNoTokenArg(t *testing.T) {
	isolate(t)
	startDaemon(t)
	// A third argument is someone putting the token in their shell history.
	_, err := run(t, "", "registry", "add", "local", "--url", "hub.example.com/me", "the-token")
	if err == nil {
		t.Fatal("registry add took a token as an argument")
	}
}

func TestClusterListWhenNone(t *testing.T) {
	isolate(t)
	startDaemon(t)
	out, err := run(t, "", "cluster", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No clusters", "agentbox cluster add")
}

// kubeconfigFile is an admin kubeconfig pointing at an address nothing answers
// on, so a cluster command fails fast instead of needing a cluster.
func kubeconfigFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	body := `apiVersion: v1
kind: Config
clusters:
- name: dead
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
users:
- name: admin
  user:
    token: a-token
contexts:
- name: dead
  context:
    cluster: dead
    user: admin
current-context: dead
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClusterAddNoKubeconfig(t *testing.T) {
	isolate(t)
	startDaemon(t)
	t.Setenv("KUBECONFIG", "")
	_, err := run(t, "", "cluster", "add", "k3d")
	if err == nil || !strings.Contains(err.Error(), "reading the admin kubeconfig") {
		t.Errorf("cluster add with no kubeconfig = %v, want it to say where it looked", err)
	}
}

func TestClusterAddUnreachable(t *testing.T) {
	isolate(t)
	startDaemon(t)
	_, err := run(t, "", "cluster", "add", "k3d", "--kubeconfig", kubeconfigFile(t))
	if err == nil {
		t.Fatal("cluster add succeeded against an address nothing answers on")
	}
	mustContain(t, err.Error(), "cluster k3d", "connect: connection refused")
}

// TestClusterDrainRefusesBothFlags: moving the agents and keeping them running
// are two different drains, and the command says so before it reaches a daemon.
func TestClusterDrainRefusesBothFlags(t *testing.T) {
	isolate(t)
	_, err := run(t, "", "cluster", "drain", "k3d", "--move-to", "e2e", "--keep-running")
	if err == nil || !strings.Contains(err.Error(), "--move-to and --keep-running can't be used together") {
		t.Errorf("cluster drain with both flags = %v, want it to refuse them", err)
	}
}

// TestClusterUpgradeRefusesNoChoice: an upgrade is a digest or a rollback, and
// neither is a sentence rather than a job.
func TestClusterUpgradeRefusesNoChoice(t *testing.T) {
	isolate(t)
	if _, err := run(t, "", "cluster", "upgrade", "k3d"); err == nil ||
		!strings.Contains(err.Error(), "pass --digest sha256:...") {
		t.Errorf("cluster upgrade with no flags = %v, want it to ask for one", err)
	}
	if _, err := run(t, "", "cluster", "upgrade", "k3d", "--digest", "sha256:abc", "--rollback"); err == nil ||
		!strings.Contains(err.Error(), "--digest and --rollback can't be used together") {
		t.Errorf("cluster upgrade with both flags = %v, want it to refuse them", err)
	}
}

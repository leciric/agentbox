package cli_test

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/sshconfig"
)

// sshProxyDaemon serves what ssh-proxy asks the daemon: its version, every
// agent, and starting one. It answers with agents, and records the actions.
func sshProxyDaemon(t *testing.T, agents []api.Agent) *[]string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var actions []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.VersionInfo{Version: "dev"})
	})
	mux.HandleFunc("GET /v1/agents", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(agents)
	})
	mux.HandleFunc("POST /v1/agents/{project}/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		actions = append(actions, r.PathValue("action")+" "+r.PathValue("project")+"/"+r.PathValue("name"))
		mu.Unlock()
		for _, a := range agents {
			if a.Ref == r.PathValue("project")+"/"+r.PathValue("name") {
				a.State = "running"
				_ = json.NewEncoder(w).Encode(a)
				return
			}
		}
		http.NotFound(w, r)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("AGENTBOX_SOCKET", sock)
	// Wherever the tests run, this is a host: not an agent's machine.
	t.Setenv("AGENTBOX_IN_AGENT_MARKER", filepath.Join(t.TempDir(), "none"))
	t.Setenv("AGENTBOX_IN_AGENT_SOCKET", filepath.Join(t.TempDir(), "none"))
	return &actions
}

// fakeIncusOnPath puts an incus first on PATH that prints its arguments, one
// a line, then what came in on stdin: what ssh-proxy hands sshd.
func fakeIncusOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf 'arg:%s\\n' \"$a\"; done\ncat\n"
	if err := os.WriteFile(filepath.Join(dir, "incus"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func testKey(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := sshconfig.Ensure(sshconfig.Files{Dir: dir, UserConfig: filepath.Join(dir, "user-config")}, "/usr/bin/agentbox", "dev"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestSSHProxyStartsAStoppedAgentAndRunsSSHDInItsMachine(t *testing.T) {
	isolate(t)
	actions := sshProxyDaemon(t, []api.Agent{
		{Ref: "pawly/agent-01", Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", State: "running"},
		{Ref: "pawly/agent-02", Project: "pawly", Name: "agent-02", Instance: "ab-pawly-agent-02", State: "stopped"},
	})
	fakeIncusOnPath(t)
	key := testKey(t)
	out, err := run(t, "ssh bytes", "ssh-proxy", "--key", key, "ab-pawly-agent-02.agentbox")
	if err != nil {
		t.Fatalf("ssh-proxy: %v\n%s", err, out)
	}
	if strings.Join(*actions, ",") != "start pawly/agent-02" {
		t.Errorf("actions = %v, want the stopped agent started", *actions)
	}
	args := strings.Split(strings.TrimSuffix(strings.SplitAfter(out, "\narg:"+key+"\n")[0], "\n"), "\n")
	if len(args) < 7 || strings.Join(args[:6], "|") != "arg:exec|arg:-T|arg:ab-pawly-agent-02|arg:--|arg:sh|arg:-c" {
		t.Fatalf("incus ran with %q", out)
	}
	mustContain(t, out, "arg:"+sshconfig.ServerScript, "arg:"+key+"\narg:", "ssh bytes")
}

func TestSSHProxyLeavesARunningAgentAlone(t *testing.T) {
	isolate(t)
	actions := sshProxyDaemon(t, []api.Agent{{Ref: "pawly/agent-01", Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", State: "running"}})
	fakeIncusOnPath(t)
	if out, err := run(t, "", "ssh-proxy", "--key", testKey(t), "ab-pawly-agent-01.agentbox"); err != nil {
		t.Fatalf("ssh-proxy: %v\n%s", err, out)
	}
	if len(*actions) != 0 {
		t.Errorf("actions = %v, want none", *actions)
	}
}

func TestSSHProxyRefusesWhatIsntAnAgentsMachine(t *testing.T) {
	isolate(t)
	sshProxyDaemon(t, []api.Agent{{Ref: "pawly/agent-01", Instance: "ab-pawly-agent-01", State: "running"}})
	key := testKey(t)
	for _, tc := range []struct{ host, key, want string }{
		{"github.com", key, "isn't an agent's machine"},
		{"ab-pawly-agent-09.agentbox", key, "no agent's machine is called ab-pawly-agent-09"},
		{"ab-pawly-agent-01.agentbox", "not a key", "--key must be one public key"},
		{"ab-pawly-agent-01.agentbox", key + "\n" + key, "--key must be one public key"},
	} {
		out, err := run(t, "", "ssh-proxy", "--key", tc.key, tc.host)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ssh-proxy %s: %v, want %q\n%s", tc.host, err, tc.want, out)
		}
	}
}

func TestSSHConfigWritesAndRemovesTheInclude(t *testing.T) {
	home := isolate(t)
	out, err := run(t, "", "ssh-config", "--json")
	if err != nil {
		t.Fatalf("ssh-config: %v\n%s", err, out)
	}
	var setup sshconfig.Setup
	if err := json.Unmarshal([]byte(out), &setup); err != nil || setup.HostSuffix != ".agentbox" || setup.User == "" {
		t.Fatalf("ssh-config --json = %q (%v)", out, err)
	}
	user := filepath.Join(home, ".ssh", "config")
	b, err := os.ReadFile(user)
	if err != nil || !strings.Contains(string(b), `Include "`+filepath.Join(home, ".local", "share", "agentbox", "ssh", "config")+`"`) {
		t.Fatalf("~/.ssh/config = %q (%v)", b, err)
	}
	if out, err := run(t, "", "ssh-config", "--remove"); err != nil {
		t.Fatalf("ssh-config --remove: %v\n%s", err, out)
	}
	if _, err := os.Stat(user); !os.IsNotExist(err) {
		t.Errorf("--remove left the ~/.ssh/config it made: %v", err)
	}
}

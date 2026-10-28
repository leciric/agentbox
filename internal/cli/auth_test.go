package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/credentials"
	"agentbox/internal/opencode"
)

func TestAuthSaysHowToInstallAMissingTool(t *testing.T) {
	isolate(t)
	startDaemon(t)
	t.Setenv("PATH", t.TempDir())

	_, err := run(t, "", "auth", "codex")
	if err == nil || !strings.Contains(err.Error(), "the Codex CLI on this machine, and it isn't installed") || !strings.Contains(err.Error(), "npm install -g @openai/codex") {
		t.Errorf("auth codex without codex: err = %v", err)
	}

}

// The VM `agentbox auth opencode` runs in has no OpenCode on PATH, which used
// to end the login before it began. It runs AgentBox's own pinned copy, the
// one opencode.Installer downloads into the tools directory (faked here, as a
// download leaves it), against AgentBox's own data directory.
func TestAuthOpenCodeRunsItsOwnCopy(t *testing.T) {
	home := isolate(t)
	startDaemon(t)
	t.Setenv("PATH", t.TempDir())
	own := opencode.Installer{Dir: filepath.Join(home, ".local", "share", "agentbox", "tools")}.Path()
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$@\" >\"${0%/*}/args\"\necho \"$XDG_CONFIG_HOME\" >\"${0%/*}/confighome\"\nprintf '{\"anthropic\":{\"type\":\"api\",\"key\":\"k\"}}' >\"$XDG_DATA_HOME/opencode/auth.json\"\n"
	if err := os.WriteFile(own, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "opencode", "--provider", "anthropic", "--account", "work")
	if err != nil {
		t.Fatalf("auth opencode: %v\n%s", err, out)
	}
	if args, _ := os.ReadFile(filepath.Join(filepath.Dir(own), "args")); strings.TrimSpace(string(args)) != "auth login --provider anthropic" {
		t.Errorf("ran opencode %q", args)
	}
	creds := credentials.Store{Dir: filepath.Join(home, ".config", "agentbox", "credentials")}
	// The login must not read the host's opencode.json: this test's XDG home
	// has one, and a config written for the other release is one this OpenCode
	// refuses. It reads an empty config directory of AgentBox's instead.
	configHome, _ := os.ReadFile(filepath.Join(filepath.Dir(own), "confighome"))
	if got := strings.TrimSpace(string(configHome)); !strings.HasPrefix(got, creds.Dir) {
		t.Errorf("the login's XDG_CONFIG_HOME = %q, want one under %s", got, creds.Dir)
	}
	// Kept as the provider's account of that name, and nothing else.
	if ok, _ := creds.HasToolAccount("opencode", "anthropic", "work"); !ok || !creds.HasOpenCodeLogin(nil) {
		t.Errorf("no anthropic account \"work\" after: %s", out)
	}
	if entries, _ := os.ReadDir(creds.Dir); len(entries) != 1 {
		t.Errorf("the login's scratch directory was left behind: %v", entries)
	}
}

// fakeAccountDaemon stands in for another machine's daemon: it answers its
// version and settings, records each login saved to it, and says which account
// each tool uses by default.
type fakeAccountDaemon struct {
	URL   string
	mu    sync.Mutex
	saved map[string][]api.ToolAccountRequest
}

func newFakeAccountDaemon(t *testing.T) *fakeAccountDaemon {
	t.Helper()
	d := &fakeAccountDaemon{saved: map[string][]api.ToolAccountRequest{}}
	json200 := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, _ *http.Request) { json200(w, `{"version":"9.9.9"}`) })
	mux.HandleFunc("GET /v1/settings", func(w http.ResponseWriter, _ *http.Request) { json200(w, `{}`) })
	mux.HandleFunc("POST /v1/auth/accounts/{tool}", func(w http.ResponseWriter, r *http.Request) {
		var req api.ToolAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		d.mu.Lock()
		d.saved[r.PathValue("tool")] = append(d.saved[r.PathValue("tool")], req)
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/auth", func(w http.ResponseWriter, _ *http.Request) {
		json200(w, `{"codexAccounts":[{"tool":"codex","name":"work","default":true}],"openCodeAccounts":[{"tool":"opencode","provider":"anthropic","name":"work","default":true}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	d.URL = srv.URL
	return d
}

// at makes d the active profile, called "nas".
func (d *fakeAccountDaemon) at(t *testing.T) {
	t.Helper()
	if _, err := run(t, "", "profile", "add", "nas", d.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "", "use", "nas"); err != nil {
		t.Fatal(err)
	}
}

func (d *fakeAccountDaemon) requests(tool string) []api.ToolAccountRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]api.ToolAccountRequest(nil), d.saved[tool]...)
}

// TestAuthOpenCodeStoresTheLoginOnTheProfile: the login has to run where the
// terminal and the browser are, but the account belongs to the daemon that
// holds the accounts and gives them to agents. With a profile active, that is
// the profile's daemon, and this machine keeps nothing.
func TestAuthOpenCodeStoresTheLoginOnTheProfile(t *testing.T) {
	home := isolate(t)
	noProfiles(t)
	d := newFakeAccountDaemon(t)
	d.at(t)
	t.Setenv("PATH", t.TempDir())
	own := opencode.Installer{Dir: filepath.Join(home, ".local", "share", "agentbox", "tools")}.Path()
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '{\"anthropic\":{\"type\":\"api\",\"key\":\"k\"}}' >\"$XDG_DATA_HOME/opencode/auth.json\"\n"
	if err := os.WriteFile(own, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "opencode", "--provider", "anthropic", "--account", "work")
	if err != nil {
		t.Fatalf("auth opencode: %v\n%s", err, out)
	}
	got := d.requests("opencode")
	if len(got) != 1 || got[0].Provider != "anthropic" || got[0].Account != "work" || string(got[0].Auth) != `{"type":"api","key":"k"}` {
		t.Errorf("the daemon was sent %+v", got)
	}
	mustContain(t, out, `Saved the OpenCode account "anthropic/work" for agents on `+d.URL)
	creds := credentials.Store{Dir: filepath.Join(home, ".config", "agentbox", "credentials")}
	if ok, _ := creds.HasToolAccount("opencode", "anthropic", "work"); ok {
		t.Error("the login was also stored on this machine")
	}
}

// TestAuthCodexStoresTheLoginOnTheProfile is the same for Codex's own sign-in,
// which has no API key to send and is stored as the file it wrote.
func TestAuthCodexStoresTheLoginOnTheProfile(t *testing.T) {
	home := isolate(t)
	noProfiles(t)
	d := newFakeAccountDaemon(t)
	d.at(t)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	script := "#!/bin/sh\nprintf '{\"auth_mode\":\"chatgpt\",\"tokens\":{\"id_token\":\"t\"}}' >\"$CODEX_HOME/auth.json\"\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "", "auth", "codex", "--account", "work")
	if err != nil {
		t.Fatalf("auth codex: %v\n%s", err, out)
	}
	got := d.requests("codex")
	if len(got) != 1 || got[0].Account != "work" || !strings.Contains(string(got[0].Auth), `"chatgpt"`) {
		t.Errorf("the daemon was sent %+v", got)
	}
	mustContain(t, out, `Saved the Codex account "work" for agents on `+d.URL)
	creds := credentials.Store{Dir: filepath.Join(home, ".config", "agentbox", "credentials")}
	if ok, _ := creds.HasToolAccount("codex", "", "work"); ok {
		t.Error("the login was also stored on this machine")
	}
}

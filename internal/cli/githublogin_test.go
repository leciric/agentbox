package cli_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAuthGitHubLogin signs in with no token: the command prints the code
// the daemon's gh prints, opens the page to enter it on, and stores the token
// gh hands over once it is entered. gh and the page are fakes on PATH, the
// page's xdg-open writing down what it was given.
func TestAuthGitHubLogin(t *testing.T) {
	home := isolate(t)
	bin := t.TempDir()
	opened := filepath.Join(bin, "opened")
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\necho \"$1\" > "+opened+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gh := `#!/bin/sh
case "$1 $2" in
"auth login")
	echo "! First copy your one-time code: WDJB-MJHT" >&2
	echo "Open this URL to continue in your web browser: https://github.com/login/device" >&2
	sleep 1
	echo gho_fromgh >"$GH_CONFIG_DIR/token" ;;
"auth token") cat "$GH_CONFIG_DIR/token" ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer api.Close()
	t.Setenv("AGENTBOX_GITHUB_API", api.URL)
	startDaemon(t)

	out, err := run(t, "", "auth", "github")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "WDJB-MJHT", "https://github.com/login/device", `Saved the GitHub account "default" for agents, as octocat`)
	token, err := os.ReadFile(filepath.Join(home, ".config", "agentbox", "credentials", "github", "default.token"))
	if err != nil || strings.TrimSpace(string(token)) != "gho_fromgh" {
		t.Errorf("stored token = %q, %v", token, err)
	}
	// xdg-open runs in the background: give it a moment to write.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, _ := os.ReadFile(opened); strings.TrimSpace(string(b)) == "https://github.com/login/device" {
			return
		}
	}
	t.Error("the verification page wasn't opened")
}

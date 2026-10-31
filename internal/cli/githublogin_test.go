package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestAuthGitHubDeviceFlow signs in with no gh and no token: the command
// prints the code, opens the page to enter it on, and stores the token
// GitHub hands over once it is entered. The page is a fake xdg-open writing
// down what it was given.
func TestAuthGitHubDeviceFlow(t *testing.T) {
	home := isolate(t)
	bin := t.TempDir()
	opened := filepath.Join(bin, "opened")
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\necho \"$1\" > "+opened+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var polls atomic.Int32
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-1", "user_code": "WDJB-MJHT", "verification_uri": "https://github.com/login/device",
				"expires_in": 900, "interval": 1,
			})
		case "/login/oauth/access_token":
			if polls.Add(1) == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "gho_fromdevice"})
		case "/user":
			_, _ = w.Write([]byte(`{"login":"octocat"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	t.Setenv("AGENTBOX_GITHUB_WEB", gh.URL)
	t.Setenv("AGENTBOX_GITHUB_API", gh.URL)
	t.Setenv("AGENTBOX_GITHUB_CLIENT_ID", "Iv1.test")
	startDaemon(t)

	out, err := run(t, "", "auth", "github")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "WDJB-MJHT", "https://github.com/login/device", `Saved the GitHub account "default" for agents, as octocat`)
	token, err := os.ReadFile(filepath.Join(home, ".config", "agentbox", "credentials", "github", "default.token"))
	if err != nil || strings.TrimSpace(string(token)) != "gho_fromdevice" {
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

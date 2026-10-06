package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/hostos"

	_ "modernc.org/sqlite"
)

// Importing straight from an installed browser: the daemon finds the user's
// browsers under the host home shared into the VM, reads a profile's cookies,
// and seals them, with no cookie value in the answer. This uses a Firefox
// fixture, whose store needs no keyring.
func TestBrowserCookiesFromBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv(hostos.Env, "linux") // make hostos.Home() return the fixture home
	t.Setenv(hostos.HomeEnv, home)
	writeFirefoxFixture(t, home)

	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	list, err := d.client.BrowserProfiles(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	var ff *api.BrowserProfile
	for i := range list.Profiles {
		if list.Profiles[i].Engine == "firefox" {
			ff = &list.Profiles[i]
		}
	}
	if ff == nil {
		t.Fatalf("firefox not found among %+v", list.Profiles)
	}
	if ff.Keyring != "" {
		t.Errorf("firefox should need no keyring, got %q", ff.Keyring)
	}

	info, err := d.client.ImportFromBrowser(ctx, "hello-stack", ff.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Imported || info.Cookies != 2 || info.Source == "" || info.ImportedAt == nil {
		t.Fatalf("import = %+v", info)
	}
	if len(info.Sites) == 0 {
		t.Errorf("no per-site counts: %+v", info)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "secretvalue") {
		t.Errorf("the answer carries a cookie value: %s", raw)
	}

	// An unknown profile is refused, not a panic.
	if _, err := d.client.ImportFromBrowser(ctx, "hello-stack", "nope", ""); err == nil {
		t.Error("importing an unknown profile should fail")
	}
}

func writeFirefoxFixture(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, ".mozilla", "firefox")
	profDir := filepath.Join(root, "p1.default")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := "[Profile0]\nName=default\nIsRelative=1\nPath=p1.default\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(profDir, "cookies.sqlite")
	conn, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TABLE moz_cookies (host TEXT, name TEXT, value TEXT, path TEXT,
		expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER, sameSite INTEGER, originAttributes TEXT DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(48 * time.Hour).Unix()
	for _, r := range [][]any{
		{".github.com", "sess", "secretvalue1", "/", future, 1, 1, 1, ""},
		{"linear.app", "tok", "secretvalue2", "/", future, 1, 0, 0, ""},
	} {
		if _, err := conn.Exec(`INSERT INTO moz_cookies (host,name,value,path,expiry,isSecure,isHttpOnly,sameSite,originAttributes) VALUES (?,?,?,?,?,?,?,?,?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
}

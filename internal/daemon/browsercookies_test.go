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

// Importing cookies the desktop app read and decrypted on the host (a Windows
// app-bound browser): the daemon validates and seals them, dropping an expired
// one, and carries no cookie value in the answer.
func TestBrowserCookiesImportDecrypted(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	cookies := []api.ImportedCookie{
		{Domain: ".github.com", Name: "sess", Value: "secretvalue1", Path: "/", Expires: time.Now().Add(time.Hour).Unix(), Secure: true, HTTPOnly: true, SameSite: "Lax"},
		{Domain: "linear.app", Name: "tok", Value: "secretvalue2", Path: "/"},
		{Domain: "old.example", Name: "gone", Value: "x", Path: "/", Expires: time.Now().Add(-time.Hour).Unix()},
	}
	info, err := d.client.ImportDecryptedCookies(ctx, "hello-stack", "Google Chrome — Person 1", cookies)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Imported || info.Cookies != 2 || info.Source != "Google Chrome — Person 1" || info.ImportedAt == nil {
		t.Fatalf("import = %+v", info)
	}
	if len(info.Sites) != 2 {
		t.Errorf("per-site counts = %+v", info.Sites)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "secretvalue") {
		t.Errorf("the answer carries a cookie value: %s", raw)
	}

	// No cookies that haven't expired is an error, not an empty import.
	expired := []api.ImportedCookie{{Domain: "x.example", Name: "a", Value: "b", Path: "/", Expires: time.Now().Add(-time.Hour).Unix()}}
	if _, err := d.client.ImportDecryptedCookies(ctx, "hello-stack", "src", expired); err == nil {
		t.Error("importing only expired cookies should fail")
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

// On Windows, Chrome and Edge are listed as app-bound profiles the desktop app
// reads on the host, wherever the VM finds the Windows home.
func TestBrowserCookieProfilesOnWindows(t *testing.T) {
	win := t.TempDir()
	t.Setenv(hostos.Env, "windows")
	t.Setenv(hostos.HomeEnv, win)
	for _, b := range []struct{ root, name string }{
		{"AppData/Local/Google/Chrome/User Data", "Google Chrome"},
		{"AppData/Local/Microsoft/Edge/User Data", "Microsoft Edge"},
	} {
		dir := filepath.Join(win, filepath.FromSlash(b.root), "Default", "Network")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// A Cookies database is what makes a profile discoverable.
		if err := os.WriteFile(filepath.Join(dir, "Cookies"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}
	list, err := d.client.BrowserProfiles(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Profiles) != 2 {
		t.Fatalf("profiles = %+v", list.Profiles)
	}
	for _, p := range list.Profiles {
		if !p.AppBound {
			t.Errorf("%s profile should be app-bound: %+v", p.Browser, p)
		}
		if p.ProfileDir != "Default" {
			t.Errorf("%s profileDir = %q, want Default", p.Browser, p.ProfileDir)
		}
	}
	if list.Profiles[0].Browser != "chrome" || list.Profiles[1].Browser != "edge" {
		t.Errorf("profiles = %+v", list.Profiles)
	}
}

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/cursor"
)

// fakeCursor stands in for the adapter's host commands with a shell script:
// "models" lists two models, "check-key" takes the key "good" and writes it
// where it was told, and "login" names a page and finishes. useCursor hands it
// to a daemon.
//
// It is written before the daemon starts: a daemon's background work forks
// (the fake incus), and a child forked while the script is still open for
// writing holds it open, so running it fails with "text file busy" — the
// sign-in then fails rather than finishing.
func fakeCursor(t *testing.T) cursor.Helper {
	t.Helper()
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	script := `#!/bin/sh
case "$2" in
  models) echo '[{"value":"composer-2","name":"Composer 2","efforts":[]},{"value":"claude-opus-5-5","name":"Claude Opus 5.5","efforts":["low","high"]}]' ;;
  check-key)
    key=$(cat)
    [ "$key" = good ] || { echo "Cursor refused the sign-in (Invalid User API Key)" >&2; exit 1; }
    echo '{"version":1,"backendUrl":"https://api2.cursor.sh","apiKey":"good","createdAtMs":1,"email":"dev@example.com"}' > "$3"
    echo '{"email":"dev@example.com"}' ;;
  login)
    echo '{"loginUrl":"https://cursor.com/loginDeepControl?challenge=x"}'
    echo '{"version":1,"backendUrl":"https://api2.cursor.sh","apiKey":"minted","createdAtMs":1,"apiKeyExpiresAtMs":4102444800000,"email":"web@example.com"}' > "$3"
    echo '{"email":"web@example.com"}' ;;
esac
`
	if err := os.WriteFile(node, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cursor.Helper{Node: node, Script: filepath.Join(dir, cursor.ScriptName), SDK: dir}
}

func useCursor(d testDaemon, helper cursor.Helper) {
	d.srv.cursorHelperFor = func(context.Context) (cursor.Helper, error) { return helper, nil }
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("%s didn't happen", what)
}

// An API key Cursor knows signs agents in: Setup and the auth status say so,
// whose account it is, and the model menu is asked for with each model's
// effort levels. A key Cursor refuses is refused, and signing out forgets
// the sign-in and the menu.
func TestCursorSignInWithAKey(t *testing.T) {
	helper := fakeCursor(t)
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	useCursor(d, helper)
	ctx := context.Background()

	if c := setupCheck(t, d, "cursor"); c.Status != api.SetupOptional || c.Fix != "agentbox auth cursor" {
		t.Errorf("before signing in: %+v", c)
	}
	if _, err := d.client.SaveCursorKey(ctx, "bad"); err == nil || !strings.Contains(err.Error(), "Invalid User API Key") {
		t.Errorf("a key Cursor refuses: %v", err)
	}
	if _, err := d.client.SaveCursorKey(ctx, "  "); err == nil {
		t.Error("an empty key was taken")
	}
	email, err := d.client.SaveCursorKey(ctx, "good\n")
	if err != nil || email != "dev@example.com" {
		t.Fatalf("SaveCursorKey = %q, %v", email, err)
	}
	if c := setupCheck(t, d, "cursor"); c.Status != api.SetupOK || !strings.Contains(c.Detail, "dev@example.com") {
		t.Errorf("after signing in: %+v", c)
	}
	auth, err := d.client.Auth(ctx)
	if err != nil || !auth.Cursor || auth.CursorEmail != "dev@example.com" {
		t.Errorf("Auth = %+v, %v", auth, err)
	}
	eventually(t, "the model menu arriving", func() bool {
		s, err := d.client.Settings(ctx)
		return err == nil && s.CursorReady && len(s.CursorModelChoices) == 2 && strings.Join(s.CursorModelChoices[1].Efforts, ",") == "low,high"
	})

	if err := d.client.RemoveCursorLogin(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := d.client.Settings(ctx)
	if err != nil || s.CursorReady || len(s.CursorModelChoices) != 0 {
		t.Errorf("after signing out: ready %v, menu %+v, %v", s.CursorReady, s.CursorModelChoices, err)
	}
}

// Cursor's browser sign-in names the page to open, then finishes by itself.
func TestCursorBrowserSignIn(t *testing.T) {
	helper := fakeCursor(t)
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	useCursor(d, helper)
	ctx := context.Background()
	if status, err := d.client.CursorLoginStatus(ctx); err != nil || status.State != api.CursorLoginIdle {
		t.Fatalf("before starting: %+v, %v", status, err)
	}
	if _, err := d.client.StartCursorLogin(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the sign-in finishing", func() bool {
		status, err := d.client.CursorLoginStatus(ctx)
		return err == nil && status.State == api.CursorLoginDone && status.Email == "web@example.com"
	})
	login, ok := d.srv.manager(nil).Creds.CursorLogin()
	if !ok || login.APIKey != "minted" || login.Expiry.IsZero() {
		t.Errorf("the stored sign-in: %+v, %v", login, ok)
	}
}

// New Cursor agents' model and effort are Settings'; choosing a model alone
// clears an effort that belonged to the last one.
func TestCursorDefaultsInSettings(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	out, err := patchSettings(t, d, `{"defaultCursorModel":"claude-opus-5-5","defaultCursorEffort":"high"}`)
	if err != nil || out.DefaultCursorModel != "claude-opus-5-5" || out.DefaultCursorEffort != "high" {
		t.Fatalf("both: %+v, %v", out, err)
	}
	out, err = patchSettings(t, d, `{"defaultCursorModel":"composer-2"}`)
	if err != nil || out.DefaultCursorModel != "composer-2" || out.DefaultCursorEffort != "" {
		t.Fatalf("a model alone: model %q effort %q, %v", out.DefaultCursorModel, out.DefaultCursorEffort, err)
	}
}

func TestCursorChoices(t *testing.T) {
	got := cursorChoices([]cursor.Model{{Value: ""}, {Value: "default", Name: "Auto"}, {Value: "x", Efforts: []string{"low"}}})
	if len(got) != 2 || got[0].Name != "Auto" || got[1].Name != "x" || got[1].Efforts[0] != "low" {
		t.Errorf("cursorChoices = %+v", got)
	}
}

func TestAgentFeatureCountsCursor(t *testing.T) {
	if got := agentFeature("cursor", api.FeatureAgentCreateClaude, api.FeatureAgentCreateCodex, api.FeatureAgentCreateOpenCode, api.FeatureAgentCreateCursor); got != api.FeatureAgentCreateCursor {
		t.Errorf("agentFeature(cursor) = %q", got)
	}
	if got := usageTool("cursor"); got != "cursor" {
		t.Errorf("usageTool(cursor) = %q", got)
	}
	if key, _ := usageModel("composer-2"); key != "composer_2" {
		t.Errorf("usageModel(composer-2) = %q", key)
	}
}

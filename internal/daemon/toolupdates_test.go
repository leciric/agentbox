package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// fakeNPM answers npm's latest tag with versions, by package.
func fakeNPM(t *testing.T, versions map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := versions[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/latest")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"name":"x","version":"` + v + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var newerClaude = map[string]string{"@anthropic-ai/claude-code": "99.1.0", "@agentclientprotocol/claude-agent-acp": "99.0.0"}

// movedTools are the pinned tools with Claude Code and its adapter at
// newerClaude's versions.
func movedTools() []image.Tool {
	return image.Overrides{"claude": "99.1.0", "npm:@agentclientprotocol/claude-agent-acp": "99.0.0"}.Apply(image.ToolsFor(image.Components{}))
}

// waitToolUpdate polls Settings until the check that is running has ended.
func waitToolUpdate(t *testing.T, d testDaemon) api.ToolAutoUpdate {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s, err := d.client.Settings(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if u := s.ClaudeCodeUpdate; !u.Checking && u.CheckedAt != nil {
			return u
		}
		if time.Now().After(deadline) {
			t.Fatalf("the check never ended: %+v", s.ClaudeCodeUpdate)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Turned on, the daemon finds newer versions on npm and moves the base image
// to them in place, records them without touching tools.txt, and Setup calls
// the image up to date with them. Turned off, it forgets them.
func TestKeepClaudeCodeUpToDate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	pinned := image.ToolsFor(image.Components{})
	moved := movedTools()
	extra := withNext + `  rename) [ "$2" = agentbox-base-next ] && touch "$INCUS_LOG.updated" ;;
`
	d := startTestDaemon(t, root, toolsIncus(image.Version, image.Components{}, image.ToolsVersion(pinned), toolSpecs(pinned), extra,
		imageConfig(image.Version, image.Components{}, image.ToolsVersion(moved), toolSpecs(moved))),
		testConfig{npmRegistry: fakeNPM(t, newerClaude).URL})

	s, err := d.client.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pin := image.Overrides(nil).Version("claude")
	// Nobody chose: it's on, and the first check ran as the daemon started.
	if u := s.ClaudeCodeUpdate; !u.On || u.Pinned != pin {
		t.Fatalf("by default: %+v", u)
	}
	u := waitToolUpdate(t, d)
	if u.Installed != "99.1.0" || u.Latest != "99.1.0" || u.Error != "" || u.Pinned != pin {
		t.Errorf("after the update: %+v", u)
	}
	log := incusLog(t, root)
	for _, want := range []string{"copy agentbox-base/ready agentbox-base-next", "rename agentbox-base-next agentbox-base"} {
		if !ranLine(log, want) {
			t.Errorf("the update never ran incus %s:\n%s", want, strings.Join(log, "\n"))
		}
	}
	if o, _ := d.srv.store.ToolOverrides(ctx); o["claude"] != "99.1.0" || o["npm:@agentclientprotocol/claude-agent-acp"] != "99.0.0" {
		t.Errorf("recorded %v", o)
	}
	if got := setupCheck(t, d, "image"); got.Status != api.SetupOK {
		t.Errorf("Setup after the update: %+v", got)
	}
	d.srv.jobs.wait()

	// Checking again finds nothing newer: no second update.
	before := len(incusLog(t, root))
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{CheckClaudeCode: true}); err != nil {
		t.Fatal(err)
	}
	if u := waitToolUpdate(t, d); u.Error != "" || u.Installed != "99.1.0" {
		t.Errorf("checking again: %+v", u)
	}
	if after := incusLog(t, root); ranLine(after[before:], "copy ") {
		t.Error("checking again updated the image again")
	}

	off := false
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{ClaudeCodeAutoUpdate: &off}); err != nil {
		t.Fatal(err)
	}
	if o, _ := d.srv.store.ToolOverrides(ctx); len(o) != 0 {
		t.Errorf("turned off, it still runs %v", o)
	}
}

// When the new versions fail their checks, the base keeps the ones it has,
// nothing is recorded, Settings says why, and Setup asks for no rebuild.
func TestAFailedClaudeCodeUpdateKeepsTheBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	pinned := image.ToolsFor(image.Components{})
	extra := withNext + `  exec) echo "claude@99.1.0 doesn't work" >&2; exit 1 ;;
`
	d := startTestDaemon(t, root, toolsIncus(image.Version, image.Components{}, image.ToolsVersion(pinned), toolSpecs(pinned), extra, ""),
		testConfig{npmRegistry: fakeNPM(t, newerClaude).URL})
	on := true
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{ClaudeCodeAutoUpdate: &on}); err != nil {
		t.Fatal(err)
	}
	u := waitToolUpdate(t, d)
	if u.Error == "" || u.Installed != image.Overrides(nil).Version("claude") || u.Latest != "99.1.0" {
		t.Errorf("after the update failed: %+v", u)
	}
	if o, _ := d.srv.store.ToolOverrides(ctx); len(o) != 0 {
		t.Errorf("a failed update recorded %v", o)
	}
	for _, l := range incusLog(t, root) {
		if strings.HasPrefix(l, "rename agentbox-base") {
			t.Errorf("the base image was swapped: incus %s", l)
		}
	}
	if got := setupCheck(t, d, "image"); got.Status != api.SetupOK {
		t.Errorf("Setup after a failed update: %+v", got)
	}
	d.srv.jobs.wait()
}

// npm out of reach is a failed check, said in Settings, and changes nothing.
func TestClaudeCodeCheckOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), imageIncus(image.Version, image.Components{}))
	on := true
	if _, err := d.client.UpdateSettings(ctx, api.UpdateSettingsRequest{ClaudeCodeAutoUpdate: &on}); err != nil {
		t.Fatal(err)
	}
	if u := waitToolUpdate(t, d); !strings.Contains(u.Error, "asking npm for the newest @anthropic-ai/claude-code") || u.Latest != "" {
		t.Errorf("offline: %+v", u)
	}
}

// A user who turned it off keeps it off when the daemon starts: no check runs.
func TestClaudeCodeAutoUpdateOffSurvivesStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	pinned := image.ToolsFor(image.Components{})
	d := startTestDaemon(t, root, toolsIncus(image.Version, image.Components{}, image.ToolsVersion(pinned), toolSpecs(pinned), withNext, ""),
		testConfig{npmRegistry: fakeNPM(t, newerClaude).URL, setup: func(srv *Server) {
			if err := srv.store.SetFlag(ctx, state.SettingClaudeCodeAutoUpdate, false); err != nil {
				t.Error(err)
			}
		}})
	s, err := d.client.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u := s.ClaudeCodeUpdate; u.On || u.Checking {
		t.Fatalf("explicitly off: %+v", u)
	}
	if got := d.srv.beginToolCheck(ctx, claudeAutoUpdate); got {
		t.Error("a check began while it's off")
	}
}

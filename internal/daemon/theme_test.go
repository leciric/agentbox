package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/omarchy"
	"agentbox/internal/state"
)

func getTheme(t *testing.T, d testDaemon) api.Theme {
	t.Helper()
	w := httptest.NewRecorder()
	if err := d.srv.themeStatus(w, httptest.NewRequest(http.MethodGet, "/v1/theme", nil)); err != nil {
		t.Fatal(err)
	}
	var out api.Theme
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func patchTheme(t *testing.T, d testDaemon, body string) api.Theme {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/v1/theme", strings.NewReader(body))
	if err := d.srv.updateTheme(w, r); err != nil {
		t.Fatal(err)
	}
	var out api.Theme
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// installTheme puts a colors.toml where Omarchy keeps the current theme, and
// points the daemon's watcher at that home.
func installTheme(t *testing.T, d testDaemon, home, name, colors string) {
	t.Helper()
	theme := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.MkdirAll(theme, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(theme, "colors.toml"), []byte(colors), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(theme), "theme.name"), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	d.srv.themes = omarchy.NewWatcher(home)
}

const tokyoNight = `
mode = "dark"
accent = "#7aa2f7"
muted = "#414868"
background = "#1a1b26"
lighter_background = "#24283b"
foreground = "#a9b1d6"
`

// The case almost every machine is in: no Omarchy. The daemon reports no theme
// and keeps its own colours, and nothing about that is an error.
func TestThemeWithoutOmarchyIsAgentBoxsOwn(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	d.srv.themes = omarchy.NewWatcher(t.TempDir())

	theme := getTheme(t, d)
	if theme.Available || theme.Applied() {
		t.Errorf("a theme was reported on a machine with none: %+v", theme)
	}
	if theme.Appearance != api.AppearanceFollow {
		t.Errorf("appearance is %q, want following the host by default", theme.Appearance)
	}
	if got := d.srv.desktopTheme(); got != agent.BrandDesktopTheme {
		t.Errorf("agents would be painted %+v, want AgentBox's own", got)
	}
}

// A daemon whose watcher never started at all — no home directory to read —
// answers the same way rather than falling over.
func TestThemeWithoutAWatcher(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	d.srv.themes = nil
	if theme := getTheme(t, d); theme.Available {
		t.Errorf("a theme was reported with no watcher: %+v", theme)
	}
	if got := d.srv.desktopTheme(); got != agent.BrandDesktopTheme {
		t.Errorf("agents would be painted %+v, want AgentBox's own", got)
	}
}

func TestThemeIsReadAndFollowed(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	installTheme(t, d, t.TempDir(), "tokyo-night", tokyoNight)

	theme := getTheme(t, d)
	want := api.Theme{
		Appearance: api.AppearanceFollow, Available: true, Name: "tokyo-night", Mode: "dark",
		Background: "#1a1b26", Surface: "#24283b", Foreground: "#a9b1d6",
		Muted: "#414868", Accent: "#7aa2f7",
	}
	if theme != want {
		t.Errorf("theme %+v, want %+v", theme, want)
	}
	if !theme.Applied() {
		t.Error("a theme that is there and followed should be applied")
	}

	// And the agents' desktops are painted in it.
	desktop := d.srv.desktopTheme()
	if desktop.Name != "tokyo-night" || desktop.Accent != "#7aa2f7" || desktop.Surface != "#24283b" {
		t.Errorf("agents would be painted %+v", desktop)
	}
	if !strings.Contains(string(desktop.File()), "theme_accent=#7aa2f7") {
		t.Errorf("the file agents get is\n%s", desktop.File())
	}
}

// Pinning the app keeps AgentBox's own look on a machine that does have a
// theme — and the theme is still reported, so the app can offer it back.
func TestFollowingTheHostsThemeCanBeTurnedOff(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	installTheme(t, d, t.TempDir(), "tokyo-night", tokyoNight)

	off := patchTheme(t, d, `{"appearance":"dark"}`)
	if off.Appearance != api.AppearanceDark || off.Applied() {
		t.Errorf("after pinning it to dark: %+v", off)
	}
	if !off.Available || off.Name != "tokyo-night" {
		t.Errorf("the theme it found should still be reported: %+v", off)
	}
	if got := d.srv.desktopTheme(); got != agent.BrandDesktopTheme {
		t.Errorf("agents would be painted %+v, want AgentBox's own", got)
	}
	if got := getTheme(t, d); got.Appearance != api.AppearanceDark {
		t.Errorf("the setting didn't stick: %q", got.Appearance)
	}

	on := patchTheme(t, d, `{"appearance":"follow"}`)
	if !on.Applied() || on.Accent != "#7aa2f7" {
		t.Errorf("after following again: %+v", on)
	}
}

// The app can also be pinned light, on a machine whose desktop is dark or has
// no theme at all. That is a choice about AgentBox's own window, so the agents'
// desktops stay AgentBox's own colours either way.
func TestTheAppCanBePinnedLight(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	installTheme(t, d, t.TempDir(), "tokyo-night", tokyoNight)

	light := patchTheme(t, d, `{"appearance":"light"}`)
	if light.Appearance != api.AppearanceLight || light.Applied() {
		t.Errorf("after pinning it to light: %+v", light)
	}
	if got := getTheme(t, d); got.Appearance != api.AppearanceLight {
		t.Errorf("the setting didn't stick: %q", got.Appearance)
	}
	if got := d.srv.desktopTheme(); got != agent.BrandDesktopTheme {
		t.Errorf("agents would be painted %+v, want AgentBox's own", got)
	}
}

// An appearance nobody offers is refused, rather than stored and puzzled over
// by every later reader.
func TestAnUnknownAppearanceIsRefused(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/v1/theme", strings.NewReader(`{"appearance":"sepia"}`))
	if err := d.srv.updateTheme(w, r); err == nil {
		t.Fatal("an unknown appearance was accepted")
	}
	if got := getTheme(t, d); got.Appearance != api.AppearanceFollow {
		t.Errorf("it changed the setting anyway: %q", got.Appearance)
	}
}

// The switch this setting grew out of stored "0" for "AgentBox's own look",
// which is what dark means now. An installation that turned it off keeps the
// look it chose.
func TestTheOldFollowFlagReadsAsDark(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	installTheme(t, d, t.TempDir(), "tokyo-night", tokyoNight)
	if err := d.srv.store.SetSetting(context.Background(), state.SettingAppearance, "0"); err != nil {
		t.Fatal(err)
	}
	if got := getTheme(t, d); got.Appearance != api.AppearanceDark || got.Applied() {
		t.Errorf("an installation that had it switched off reads as %+v", got)
	}
}

// The app is told, so its window restyles without polling for it.
func TestAThemeChangeIsPublished(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	home := t.TempDir()
	installTheme(t, d, home, "tokyo-night", tokyoNight)

	events, unsubscribe := d.srv.events.subscribe()
	defer unsubscribe()
	d.srv.themeChanged(context.Background())

	select {
	case ev := <-events:
		if ev.Type != api.EventTheme {
			t.Fatalf("event %q, want %q", ev.Type, api.EventTheme)
		}
		var theme api.Theme
		if err := json.Unmarshal(ev.Data, &theme); err != nil {
			t.Fatal(err)
		}
		if theme.Name != "tokyo-night" || theme.Accent != "#7aa2f7" {
			t.Errorf("the event carried %+v", theme)
		}
	default:
		t.Fatal("no theme event was published")
	}
}

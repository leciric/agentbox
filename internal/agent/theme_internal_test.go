package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeFileTakesOnlyRealColours(t *testing.T) {
	// Everything a colour could be that isn't a colour. browser.sh pastes
	// these into a tint2 config and an openbox themerc, so each one has to
	// come out as AgentBox's own value instead.
	theme := DesktopTheme{
		Name:       "tokyo night; rm -rf /",
		Mode:       "sideways",
		Background: "#1a1b26",
		Surface:    "rgba(36, 40, 59, 0.9)",
		Foreground: "#a9b1d6\nborder_color = #ff0000",
		Muted:      "",
		Accent:     "#7AA2F7",
	}
	got := string(theme.File())
	for _, want := range []string{
		"theme_name=agentbox\n",
		"theme_mode=dark\n",
		"theme_background=#1a1b26\n",
		"theme_surface=" + BrandDesktopTheme.Surface + "\n",
		"theme_foreground=" + BrandDesktopTheme.Foreground + "\n",
		"theme_muted=" + BrandDesktopTheme.Muted + "\n",
		"theme_accent=#7aa2f7\n", // a real colour, lowercased
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the theme file doesn't have %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if key, _, ok := strings.Cut(line, "="); !ok || !strings.HasPrefix(key, "theme_") {
			t.Errorf("stray line in the theme file: %q", line)
		}
	}
}

func TestThemeFileKeepsAWholePalette(t *testing.T) {
	got := string(DesktopTheme{
		Name: "everforest", Mode: "light",
		Background: "#fdf6e3", Surface: "#f4f0d9", Foreground: "#5c6a72",
		Muted: "#939f91", Accent: "#8da101",
	}.File())
	want := "theme_name=everforest\ntheme_mode=light\ntheme_background=#fdf6e3\n" +
		"theme_surface=#f4f0d9\ntheme_foreground=#5c6a72\ntheme_muted=#939f91\ntheme_accent=#8da101\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("the theme file is\n%s\nwant it to end with\n%s", got, want)
	}
}

// runBrowserTheme runs the real browser.sh's `theme` action against a home and
// a palette of its own, with no display: it writes the configs and stops. This
// is the same code path the daemon runs inside an agent when the host's theme
// changes.
func runBrowserTheme(t *testing.T, home string, theme *DesktopTheme) {
	t.Helper()
	share := filepath.Join(t.TempDir(), "share")
	if err := os.MkdirAll(share, 0o755); err != nil {
		t.Fatal(err)
	}
	if theme != nil {
		if err := os.WriteFile(filepath.Join(share, "theme.conf"), theme.File(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(t.TempDir(), "browser.sh")
	if err := os.WriteFile(script, browserScript, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "theme")
	cmd.Env = append(os.Environ(), "HOME="+home, "AGENTBOX_SHARE="+share)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("browser.sh theme: %v\n%s", err, out)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestBrowserScriptPaintsTheDesktopInTheThemesColours(t *testing.T) {
	home := t.TempDir()
	tokyoNight := DesktopTheme{
		Name: "tokyo-night", Mode: "dark",
		Background: "#1a1b26", Surface: "#24283b", Foreground: "#a9b1d6",
		Muted: "#414868", Accent: "#7aa2f7",
	}
	runBrowserTheme(t, home, &tokyoNight)

	tint2 := read(t, filepath.Join(home, ".config", "tint2", "tint2rc"))
	for _, want := range []string{
		"background_color = #24283b 90", // the dock
		"border_color = #a9b1d6 12",     // its edge
		"border_color = #7aa2f7 100",    // the line under the active window
		"clock_font_color = #a9b1d6 72",
	} {
		if !strings.Contains(tint2, want) {
			t.Errorf("the dock's config doesn't have %q:\n%s", want, tint2)
		}
	}
	// The launchers' tiles are drawn in the accent too.
	for _, app := range []string{"browser", "files", "terminal"} {
		if icon := read(t, filepath.Join(home, ".config", "agentbox", "dock", app+".svg")); !strings.Contains(icon, `fill="#7aa2f7"`) {
			t.Errorf("the dock's %s tile isn't in the accent:\n%s", app, icon)
		}
	}
	for _, brand := range []string{"#16161f", "#a78bfa", "#7c3aed", "#e9e7f5"} {
		if strings.Contains(tint2, brand) {
			t.Errorf("the dock's config still has AgentBox's own %s:\n%s", brand, tint2)
		}
	}

	themerc := read(t, filepath.Join(home, ".themes", "AgentBox", "openbox-3", "themerc"))
	for _, want := range []string{
		"border.color: #7aa2f7",
		"window.active.title.bg.color: #24283b",
		"window.active.label.text.color: #a9b1d6",
		"window.inactive.label.text.color: #414868",
		"window.inactive.title.bg.color: #1a1b26",
	} {
		if !strings.Contains(themerc, want) {
			t.Errorf("the openbox theme doesn't have %q:\n%s", want, themerc)
		}
	}
	// And rc.xml has to actually name it, or openbox reads none of that.
	if rc := read(t, filepath.Join(home, ".config", "openbox", "rc.xml")); !strings.Contains(rc, "<name>AgentBox</name>") {
		t.Errorf("openbox isn't pointed at the theme:\n%s", rc)
	}

	// No GTK settings are written by a theme change, and none of them ever
	// says dark: Chromium reads that file and would put every page an agent
	// browses into its dark mode (D67).
	gtk := filepath.Join(home, ".config", "gtk-3.0", "settings.ini")
	if _, err := os.Stat(gtk); !os.IsNotExist(err) {
		t.Errorf("a theme change wrote GTK settings (%v)", err)
	}
	if strings.Contains(string(browserScript), "prefer-dark") {
		t.Error("browser.sh asks GTK for a dark theme, which D67 decided against")
	}
}

// With no palette in the agent — every agent whose binary predates this — the
// desktop is AgentBox's own, exactly as it was.
func TestBrowserScriptFallsBackToAgentBoxsColours(t *testing.T) {
	home := t.TempDir()
	runBrowserTheme(t, home, nil)
	tint2 := read(t, filepath.Join(home, ".config", "tint2", "tint2rc"))
	for _, want := range []string{"background_color = #16161f 90", "border_color = #8b5cf6 100"} {
		if !strings.Contains(tint2, want) {
			t.Errorf("the dock's config doesn't have %q:\n%s", want, tint2)
		}
	}
}

// The rule D67 set: a config the agent has edited is the agent's. A theme
// change goes through the same write_config as everything else, so it can't be
// the one exception that overwrites an agent's own dock.
func TestAThemeChangeKeepsTheAgentsOwnEdits(t *testing.T) {
	home := t.TempDir()
	runBrowserTheme(t, home, nil)

	tint2 := filepath.Join(home, ".config", "tint2", "tint2rc")
	mine := "# mine now\npanel_items = TC\n"
	if err := os.WriteFile(tint2, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	runBrowserTheme(t, home, &DesktopTheme{
		Name: "gruvbox", Mode: "dark",
		Background: "#282828", Surface: "#3c3836", Foreground: "#ebdbb2",
		Muted: "#928374", Accent: "#d79921",
	})
	if got := read(t, tint2); got != mine {
		t.Errorf("the agent's own dock config was replaced:\n%s", got)
	}
	// The window decorations the agent didn't touch still follow the theme.
	if themerc := read(t, filepath.Join(home, ".themes", "AgentBox", "openbox-3", "themerc")); !strings.Contains(themerc, "border.color: #d79921") {
		t.Errorf("the openbox theme didn't follow:\n%s", themerc)
	}
}

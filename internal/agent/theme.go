package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"agentbox/internal/state"
)

// The colours an agent's desktop is painted in. They travel to the agent the
// way the wallpaper does (wallpaper.go): written into it on `browser start`,
// from the host binary, because an agent's machine is its own and has no
// access to the host's ~/.config. browser.sh reads the file and builds the
// dock, the window decorations and the root window's backdrop out of it.
//
// What they are not is a dark-mode signal. D67 decided that the agent's
// Chromium gets no dark GTK theme, because Chromium maps GTK's dark preference
// onto prefers-color-scheme and would then render every page an agent looks at
// in that page's dark mode. Following the host's theme doesn't change that:
// these colours reach tint2 and openbox, and nothing here touches GTK's
// gtk-application-prefer-dark-theme or anything else Chromium reads.

// DesktopTheme is the palette browser.sh paints the desktop with.
type DesktopTheme struct {
	// Name is what to call it, for the agent's own benefit when it looks at
	// the file: "tokyo-night", or "agentbox" for the default below.
	Name string
	// Mode is "dark" or "light". Nothing in the desktop branches on it yet;
	// it is in the file because a light theme's colours are only legible if
	// whatever reads them next knows they are a light theme's.
	Mode string
	// The five colours, each "#rrggbb". Background is the root window;
	// Surface is the dock and the title bars on it; Foreground is their text;
	// Muted is an inactive window's text; Accent is the dock's border and the
	// active window's.
	Background, Surface, Foreground, Muted, Accent string
}

// BrandDesktopTheme is AgentBox's own: the colours the desktop had before it
// could follow anything, and what it falls back to for every field a theme
// doesn't supply a usable value for.
var BrandDesktopTheme = DesktopTheme{
	Name:       "agentbox",
	Mode:       "dark",
	Background: "#0b0b11",
	Surface:    "#16161f",
	Foreground: "#e9e7f5",
	Muted:      "#8b8ba7",
	Accent:     "#8b5cf6",
}

// themePath is where browser.sh looks for the palette, beside the wallpaper.
const themePath = "/usr/local/share/agentbox/theme.conf"

// hexColor is what may be written into the file. browser.sh pastes these
// straight into a tint2 config and an openbox theme, so this is the boundary
// where a colour that came off the host's disk stops being arbitrary text.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// themeName is what a name may be: enough to identify a theme, and nothing
// that would end a line or start a second one.
var themeName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// File renders the theme as the key=value lines browser.sh reads. Every field
// is checked here rather than trusted: a value that isn't a plain hex colour
// falls back to BrandDesktopTheme's, so the worst a strange theme can do is
// leave the desktop looking like AgentBox.
func (t DesktopTheme) File() []byte {
	var b strings.Builder
	b.WriteString("# The agent desktop's colours, written by AgentBox. Don't edit:\n")
	b.WriteString("# it is replaced whenever the desktop starts.\n")
	name := t.Name
	if !themeName.MatchString(name) {
		name = BrandDesktopTheme.Name
	}
	mode := "dark"
	if t.Mode == "light" {
		mode = "light"
	}
	fmt.Fprintf(&b, "theme_name=%s\ntheme_mode=%s\n", name, mode)
	for _, field := range []struct{ key, value, fallback string }{
		{"theme_background", t.Background, BrandDesktopTheme.Background},
		{"theme_surface", t.Surface, BrandDesktopTheme.Surface},
		{"theme_foreground", t.Foreground, BrandDesktopTheme.Foreground},
		{"theme_muted", t.Muted, BrandDesktopTheme.Muted},
		{"theme_accent", t.Accent, BrandDesktopTheme.Accent},
	} {
		value := strings.ToLower(field.value)
		if !hexColor.MatchString(value) {
			value = field.fallback
		}
		fmt.Fprintf(&b, "%s=%s\n", field.key, value)
	}
	return []byte(b.String())
}

// desktopTheme is what to paint an agent's desktop with: whatever the daemon
// last read off the host, or AgentBox's own on a Manager that was never given
// one (the command-line tool, and the tests).
func (m *Manager) desktopTheme() DesktopTheme {
	if m.DesktopTheme == nil {
		return BrandDesktopTheme
	}
	return m.DesktopTheme()
}

// installTheme writes the palette into the agent, unless the one there is
// already this one. Like the wallpaper, a failure isn't fatal: browser.sh
// falls back to AgentBox's own colours when the file isn't there.
func (m *Manager) installTheme(ctx context.Context, a state.Agent) {
	file := m.desktopTheme().File()
	sum := sha256.Sum256(file)
	want := hex.EncodeToString(sum[:])
	out, err := m.Incus.Run(ctx, "exec", a.Instance, "--",
		"sh", "-c", "sha256sum "+themePath+" 2>/dev/null | cut -d' ' -f1")
	if err == nil && strings.TrimSpace(out) == want {
		return
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, themePath, file, 0, 0, 0o644); err != nil {
		m.logf("The desktop's colours weren't installed: %v", err)
	}
}

// ApplyTheme repaints a running agent's desktop in the current colours, for a
// theme that changed while the agent was up. An agent whose desktop has never
// started has no browser.sh in it and nothing to repaint, which the script's
// own `theme` action treats as a success — it writes the configs for the next
// start and leaves the display alone.
func (m *Manager) ApplyTheme(ctx context.Context, a state.Agent) error {
	if err := m.requireRunning(ctx, a); err != nil {
		return err
	}
	m.installTheme(ctx, a)
	if err := m.Incus.WriteFile(ctx, a.Instance, browserScriptPath, browserScript, 0, 0, 0o755); err != nil {
		return err
	}
	return m.runBrowserScript(ctx, a, "theme")
}

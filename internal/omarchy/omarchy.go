// Package omarchy reads the theme Omarchy (github.com/basecamp/omarchy) has
// applied on this machine, so AgentBox can wear the same colours.
//
// Omarchy applies a theme with omarchy-theme-set, which stages the theme into
// a directory and moves it over the current one. Everything it leaves behind
// is plain text a reader can have for free:
//
//	~/.local/state/omarchy/current/theme/colors.toml   the palette
//	~/.local/state/omarchy/current/theme.name          the theme's name
//
// and, on installations from before Omarchy moved its state out of the config
// directory, the same two under ~/.config/omarchy/current/. There is a hook
// (~/.config/omarchy/hooks/theme-set) AgentBox could install a script into,
// but it doesn't: writing into the user's Omarchy configuration to learn a
// colour is a worse bargain than reading the file Omarchy already writes, and
// a hook installed by an app that is later uninstalled outlives it.
//
// Nothing here fails loudly. No Omarchy, a theme that ships no colors.toml, a
// palette that parses to nothing usable: all of them are "there is no theme to
// follow", which leaves AgentBox looking like AgentBox.
package omarchy

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Palette is the small fixed set of colours AgentBox takes from a theme —
// five, because every one of them has to be usable both by CSS in the Electron
// app and by an agent's tint2 and openbox configs, and a palette that large
// has no room for a colour only one of them could use.
//
// A Palette is either complete or not returned at all: Read fills every field
// or reports no theme, so no consumer has to decide what a half-theme means.
type Palette struct {
	// Name is the theme's, as Omarchy records it: "tokyo-night".
	Name string
	// Mode is "dark" or "light", resolved the way omarchy-theme-color
	// resolves it — the mode key, then the legacy theme_type key, then a
	// light.mode file beside the palette, then the background's luminance.
	Mode string
	// Background is the theme's base background (colors.toml `background`).
	Background string
	// Surface is what sits on the background: a panel, a dock
	// (`lighter_background`).
	Surface string
	// Foreground is the text colour (`foreground`).
	Foreground string
	// Muted is the dimmed text and border colour (`muted`).
	Muted string
	// Accent is the theme's one highlight colour (`accent`), which is what
	// most of a theme's character actually is.
	Accent string
}

// Dark reports whether the theme is a dark one.
func (p Palette) Dark() bool { return p.Mode != "light" }

// hexColor is the only value shape AgentBox accepts out of a theme file.
// Omarchy's own parser allows much more — rgba() lists, gradient angles, bare
// words — because its templates paste values into configs for programs that
// understand them. AgentBox's two consumers don't: a tint2 config wants
// "#rrggbb", and a CSS custom property is a place where a value from a file on
// disk becomes part of a stylesheet. Anything that isn't a plain six- or
// three-digit hex is treated as a key the theme didn't set.
var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// Read returns the theme Omarchy has applied under home, or ok=false when
// there is none to read. It never returns an error: every way this can fail —
// no Omarchy, an unreadable file, a palette missing the colours AgentBox
// needs — means the same thing to every caller.
func Read(home string) (Palette, bool) {
	for _, current := range currentDirs(home) {
		p, ok := readCurrent(current)
		if ok {
			return p, true
		}
	}
	return Palette{}, false
}

// currentDirs are the places Omarchy has kept its current theme, newest first.
// The second is where it lived before Omarchy moved its state to
// ~/.local/state (its own migration 1781043107), and is still what an
// installation that hasn't been updated has.
func currentDirs(home string) []string {
	return []string{
		filepath.Join(home, ".local", "state", "omarchy", "current"),
		filepath.Join(home, ".config", "omarchy", "current"),
	}
}

func readCurrent(current string) (Palette, bool) {
	theme := filepath.Join(current, "theme")
	raw, err := os.ReadFile(filepath.Join(theme, "colors.toml"))
	if err != nil {
		return Palette{}, false
	}
	colors := parse(raw)
	lightMode := false
	if _, err := os.Stat(filepath.Join(theme, "light.mode")); err == nil {
		lightMode = true
	}
	p, ok := palette(colors, lightMode)
	if !ok {
		return Palette{}, false
	}
	p.Name = readName(current, theme)
	return p, true
}

// readName is the theme's name: the one Omarchy wrote down, and otherwise the
// directory the current theme points at, which is what it is called on a
// version that kept `theme` as a symlink into the themes directory.
func readName(current, theme string) string {
	if raw, err := os.ReadFile(filepath.Join(current, "theme.name")); err == nil {
		if name := strings.TrimSpace(string(raw)); name != "" {
			return name
		}
	}
	// Only when `theme` really is a symlink: resolving a plain directory
	// would name every theme "theme".
	if info, err := os.Lstat(theme); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(theme); err == nil {
			return filepath.Base(target)
		}
	}
	return ""
}

// parse reads the key = "value" lines of a colors.toml. It is not a TOML
// parser and doesn't need to be: a colors.toml is a flat list of strings, and
// this is the same shape omarchy-theme-color itself reads it in. Table
// headers, comments and anything that isn't a plain hex colour are dropped —
// except `mode` and `theme_type`, the two keys whose value is a word.
func parse(raw []byte) map[string]string {
	colors := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if q := value[:1]; q == `"` || q == `'` {
			// The quoted form, which is also what drops a trailing comment.
			rest := value[1:]
			end := strings.Index(rest, q)
			if end < 0 {
				continue
			}
			value = rest[:end]
		} else if before, _, found := strings.Cut(value, " #"); found {
			// Unquoted, with a comment after it. A `#` with no space before
			// it is the colour's own.
			value = strings.TrimSpace(before)
		}
		if value == "" {
			continue
		}
		if key == "mode" || key == "theme_type" {
			colors[key] = strings.ToLower(value)
			continue
		}
		if hexColor.MatchString(value) {
			colors[key] = strings.ToLower(value)
		}
	}
	return colors
}

// palette resolves the five colours out of whatever the theme defined. The
// fallbacks follow omarchy-theme-color's own cascade, in the order it applies
// them: the short legacy names (bg, fg), then the ANSI colorN names that a
// theme generated before Omarchy's semantic palette is written entirely in,
// then the derivations.
func palette(colors map[string]string, lightMode bool) (Palette, bool) {
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := colors[k]; v != "" {
				return v
			}
		}
		return ""
	}
	p := Palette{
		Background: first("background", "bg", "color0"),
		Foreground: first("foreground", "fg", "color7"),
		Accent:     first("accent", "blue", "color4"),
	}
	if p.Background == "" || p.Foreground == "" || p.Accent == "" {
		// Nothing sensible can be built on a theme missing any of these, and
		// inventing them would make AgentBox look like neither itself nor the
		// desktop around it.
		return Palette{}, false
	}
	p.Surface = first("lighter_background", "lighter_bg", "selection", "color8")
	if p.Surface == "" {
		p.Surface = p.Background
	}
	p.Muted = first("muted", "color8", "dark_foreground", "dark_fg")
	if p.Muted == "" {
		p.Muted = p.Foreground
	}
	p.Mode = resolveMode(colors, lightMode, p.Background)
	return p, true
}

func resolveMode(colors map[string]string, lightMode bool, background string) string {
	for _, key := range []string{"mode", "theme_type"} {
		switch colors[key] {
		case "light":
			return "light"
		case "dark":
			return "dark"
		}
	}
	if lightMode {
		return "light"
	}
	// Omarchy's own auto-detect: the channels of the background added up,
	// against half of 765.
	if r, g, b, ok := rgb(background); ok && r+g+b > 382 {
		return "light"
	}
	return "dark"
}

// rgb splits a hex colour that hexColor has already accepted, expanding the
// three-digit form the way CSS does.
func rgb(hex string) (r, g, b int, ok bool) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(n >> 16), int(n >> 8 & 0xff), int(n & 0xff), true
}

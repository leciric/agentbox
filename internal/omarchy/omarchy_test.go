package omarchy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// install puts one of the testdata themes where Omarchy keeps its current
// theme under home, the way omarchy-theme-set leaves it: a directory of files,
// with the name beside it.
func install(t *testing.T, home, fixture, name string) {
	t.Helper()
	theme := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.RemoveAll(theme); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(theme, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join("testdata", fixture, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(theme, e.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if name != "" {
		if err := os.WriteFile(filepath.Join(filepath.Dir(theme), "theme.name"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadFindsTheCurrentTheme(t *testing.T) {
	home := t.TempDir()
	install(t, home, "tokyo-night", "tokyo-night")

	p, ok := Read(home)
	if !ok {
		t.Fatal("no theme was read")
	}
	want := Palette{
		Name: "tokyo-night", Mode: "dark",
		Background: "#1a1b26", Surface: "#24283b",
		Foreground: "#a9b1d6", Muted: "#414868", Accent: "#7aa2f7",
	}
	if p != want {
		t.Errorf("read %+v, want %+v", p, want)
	}
	if !p.Dark() {
		t.Error("tokyo-night should be dark")
	}
}

// The location Omarchy kept its current theme in before it moved its state out
// of the config directory. An installation that hasn't been updated still has
// it there, and its themes are just as readable.
func TestReadFallsBackToTheOldStateLocation(t *testing.T) {
	home := t.TempDir()
	current := filepath.Join(home, ".config", "omarchy", "current")
	if err := os.MkdirAll(filepath.Join(current, "theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "tokyo-night", "colors.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "theme", "colors.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	p, ok := Read(home)
	if !ok {
		t.Fatal("no theme was read")
	}
	if p.Accent != "#7aa2f7" {
		t.Errorf("accent %q, want #7aa2f7", p.Accent)
	}
}

// A `theme` symlink into a themes directory, which is how the older layout
// pointed at the current theme: its name is the directory it lands in.
func TestReadNamesAThemeBySymlink(t *testing.T) {
	home := t.TempDir()
	themes := filepath.Join(home, ".config", "omarchy", "themes", "everforest")
	if err := os.MkdirAll(themes, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "tokyo-night", "colors.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themes, "colors.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(home, ".config", "omarchy", "current")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(themes, filepath.Join(current, "theme")); err != nil {
		t.Fatal(err)
	}

	p, ok := Read(home)
	if !ok {
		t.Fatal("no theme was read")
	}
	if p.Name != "everforest" {
		t.Errorf("name %q, want everforest", p.Name)
	}
}

func TestReadResolvesLegacyANSINames(t *testing.T) {
	home := t.TempDir()
	install(t, home, "legacy-ansi", "")

	p, ok := Read(home)
	if !ok {
		t.Fatal("no theme was read")
	}
	want := Palette{
		Mode: "dark", Background: "#101014", Surface: "#585858",
		Foreground: "#d0d0d0", Muted: "#585858", Accent: "#5f87d7",
	}
	if p != want {
		t.Errorf("read %+v, want %+v", p, want)
	}
}

func TestReadDetectsLightThemes(t *testing.T) {
	home := t.TempDir()
	install(t, home, "light-by-file", "white")

	p, ok := Read(home)
	if !ok {
		t.Fatal("no theme was read")
	}
	if p.Mode != "light" || p.Dark() {
		t.Errorf("mode %q, want light", p.Mode)
	}

	// The same palette with no light.mode beside it is still light: a white
	// background is what Omarchy's own luminance check looks at.
	if err := os.Remove(filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "light.mode")); err != nil {
		t.Fatal(err)
	}
	p, _ = Read(home)
	if p.Mode != "light" {
		t.Errorf("mode %q by luminance, want light", p.Mode)
	}
}

func TestReadWithoutOmarchyIsNoTheme(t *testing.T) {
	if _, ok := Read(t.TempDir()); ok {
		t.Error("a machine with no Omarchy reported a theme")
	}
	if _, ok := Read("/nonexistent/home"); ok {
		t.Error("a home that doesn't exist reported a theme")
	}
}

func TestReadRefusesAPaletteItCantUse(t *testing.T) {
	home := t.TempDir()
	install(t, home, "no-colours", "broken")
	if _, ok := Read(home); ok {
		t.Error("a theme with no background or accent was accepted")
	}

	// Not TOML at all, and a directory where the file should be: neither is
	// an error, both are "no theme".
	theme := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.WriteFile(filepath.Join(theme, "colors.toml"), []byte("\x00\xff not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(home); ok {
		t.Error("a colors.toml of rubbish was accepted")
	}
	if err := os.Remove(filepath.Join(theme, "colors.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(theme, "colors.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(home); ok {
		t.Error("a directory named colors.toml was accepted")
	}
}

// A value that isn't a plain hex colour never reaches a consumer: these end up
// in a stylesheet and in a tint2 config, so the parser is the place that has
// to be strict about it.
func TestParseKeepsOnlyHexColours(t *testing.T) {
	colors := parse([]byte(`
mode = "dark"
background = "#101014"
foreground = "#d0d0d0"
accent = "rgba(120, 160, 240, 0.8)"
muted = "#585858; } body { display: none"
surface = "#abc"        # short form, with a comment after it
bare = #123456
quoted_comment = "#654321" # a comment
weird key = "#000000"
`))
	want := map[string]string{
		"mode": "dark", "background": "#101014", "foreground": "#d0d0d0",
		"surface": "#abc", "bare": "#123456", "quoted_comment": "#654321",
		"weird key": "#000000",
	}
	if len(colors) != len(want) {
		t.Fatalf("parsed %v, want %v", colors, want)
	}
	for k, v := range want {
		if colors[k] != v {
			t.Errorf("%s = %q, want %q", k, colors[k], v)
		}
	}
}

func TestWatcherReportsAChange(t *testing.T) {
	home := t.TempDir()
	install(t, home, "tokyo-night", "tokyo-night")
	w := NewWatcher(home)
	w.interval = 5 * time.Millisecond

	if p, ok := w.Palette(); !ok || p.Name != "tokyo-night" {
		t.Fatalf("the watcher started on %+v, %v", p, ok)
	}

	changes := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx, func() { changes <- struct{}{} })

	install(t, home, "light-by-file", "white")
	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher didn't notice the theme change")
	}
	if p, _ := w.Palette(); p.Name != "white" || p.Mode != "light" {
		t.Errorf("after the change: %+v", p)
	}

	// Omarchy going away is a change too, and leaves no theme rather than the
	// last one seen.
	if err := os.RemoveAll(filepath.Join(home, ".local")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher didn't notice the theme going away")
	}
	if _, ok := w.Palette(); ok {
		t.Error("the watcher kept a theme that isn't there any more")
	}
}

// The case every machine that isn't running Omarchy is in: the watcher runs,
// finds nothing, and says nothing, for as long as it is left to.
func TestWatcherWithoutOmarchyIsQuiet(t *testing.T) {
	w := NewWatcher(t.TempDir())
	w.interval = time.Millisecond
	if _, ok := w.Palette(); ok {
		t.Fatal("a theme was found in an empty home")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	changed := 0
	w.Run(ctx, func() { changed++ })
	if changed != 0 {
		t.Errorf("the watcher reported %d changes with no Omarchy installed", changed)
	}
}

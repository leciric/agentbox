package customize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/api"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Path: filepath.Join(t.TempDir(), "config", FileName)}
}

func patch(t *testing.T, s *Store, change string) api.Customization {
	t.Helper()
	status, err := s.Patch([]byte(change))
	if err != nil {
		t.Fatalf("Patch(%s) = %v", change, err)
	}
	return status.Customization
}

// A missing file is AgentBox's own look, and so is an empty one.
func TestNoFileIsTheDefault(t *testing.T) {
	s := newStore(t)
	status := s.Status()
	if status.Problem != "" || !reflect.DeepEqual(status.Customization, Default()) || status.Path != s.Path {
		t.Fatalf("Status() = %+v, want the default at %s", status, s.Path)
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := s.Status(); status.Problem != "" || !reflect.DeepEqual(status.Customization, Default()) {
		t.Fatalf("an empty file reads as %+v", status)
	}
}

// A change is a merge patch: what it names is set, the rest stays, a null
// puts AgentBox's own back, and a list is replaced whole. It is written to
// the file, which reads back the same.
func TestPatchMergesAndPersists(t *testing.T) {
	s := newStore(t)
	patch(t, s, `{"theme":{"accent":"#22C55E","dark":{"background":"#0b1020"}},"density":"compact"}`)
	c := patch(t, s, `{"fonts":{"size":16},"sidebar":{"foot":["media","settings"],"hidden":["jobs"]}}`)
	want := api.Customization{
		Version: api.CustomizationVersion,
		Theme:   api.CustomTheme{Accent: "#22c55e", Dark: api.CustomColors{Background: "#0b1020"}},
		Fonts:   api.CustomFonts{Size: 16},
		Density: api.DensityCompact,
		Sidebar: api.CustomSidebar{Foot: []string{"media", "settings"}, Hidden: []string{"jobs"}},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("after two changes: %+v\nwant %+v", c, want)
	}
	if got := s.Status().Customization; !reflect.DeepEqual(got, want) {
		t.Fatalf("the file reads back as %+v", got)
	}
	c = patch(t, s, `{"density":null,"theme":{"accent":null},"sidebar":{"hidden":["running"]}}`)
	if c.Density != "" || c.Theme.Accent != "" || c.Theme.Dark.Background != "#0b1020" {
		t.Errorf("null didn't put just those back: %+v", c)
	}
	if !slices.Equal(c.Sidebar.Hidden, []string{"running"}) || !slices.Equal(c.Sidebar.Foot, []string{"media", "settings"}) {
		t.Errorf("a list wasn't replaced whole, or another went with it: %+v", c.Sidebar)
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  \"theme\": {") {
		t.Errorf("the file isn't indented JSON for a person to read:\n%s", data)
	}
}

// Values are normalized as they are stored: a short colour is spelt out, and
// a value that is AgentBox's own is left unset.
func TestValuesAreNormalized(t *testing.T) {
	s := newStore(t)
	c := patch(t, s, `{"theme":{"accent":"#0F8","radius":"default","light":{"line":" #ABC "}},"density":"Comfortable","fonts":{"size":14,"ui":" Inter "},"sidebar":{"hidden":["jobs","jobs"]}}`)
	if c.Theme.Accent != "#00ff88" || c.Theme.Light.Line != "#aabbcc" {
		t.Errorf("colours = %q, %q", c.Theme.Accent, c.Theme.Light.Line)
	}
	if c.Theme.Radius != "" || c.Density != "" || c.Fonts.Size != 0 {
		t.Errorf("AgentBox's own values were kept as settings: %+v", c)
	}
	if c.Fonts.UI != "Inter" || !slices.Equal(c.Sidebar.Hidden, []string{"jobs"}) {
		t.Errorf("fonts.ui = %q, hidden = %v", c.Fonts.UI, c.Sidebar.Hidden)
	}
}

// What the schema doesn't allow is refused, saying what is wrong and what it
// takes instead, and the file stays as it was.
func TestBadValuesAreRefusedWithWhy(t *testing.T) {
	for _, tc := range []struct{ name, change, says string }{
		{"not JSON", `{"density":`, "isn't JSON"},
		{"not an object", `["compact"]`, "an object of the fields"},
		{"unknown field", `{"colour":"red"}`, `colour isn't a setting: the customization has theme, fonts, density, sidebar, css`},
		{"unknown nested field", `{"theme":{"dark":{"backdrop":"#000000"}}}`, "theme.dark.backdrop isn't a setting: theme.dark has background, sidebar"},
		{"wrong kind", `{"fonts":{"size":"big"}}`, "fonts.size is string: it must be a whole number"},
		{"bad colour", `{"theme":{"accent":"green"}}`, `theme.accent is "green": a colour is #rrggbb`},
		{"bad radius", `{"theme":{"radius":"pill"}}`, `theme.radius is "pill": it is sharp, subtle, default`},
		{"bad density", `{"density":"tight"}`, `density is "tight": it is compact, comfortable`},
		{"font size", `{"fonts":{"size":40}}`, "fonts.size is 40: it is 12 to 18 pixels"},
		{"font with CSS in it", `{"fonts":{"ui":"Inter; color: red"}}`, "fonts.ui is \"Inter; color: red\": it is a font's name"},
		{"unknown sidebar item", `{"sidebar":{"top":["pulls"]}}`, `sidebar.top has "pulls", which isn't a sidebar item: the top has new-task, home, office, filters`},
		{"item in the wrong place", `{"sidebar":{"top":["media"]}}`, `sidebar.top has "media", which is in the sidebar's other place: put it in sidebar.foot`},
		{"item twice", `{"sidebar":{"foot":["media","media"]}}`, `sidebar.foot has "media" twice`},
		{"settings hidden", `{"sidebar":{"hidden":["settings"]}}`, "settings, which can't be hidden"},
		{"unreadable text", `{"theme":{"dark":{"text":"#333333"}}}`, "theme.dark.text #333333 on AgentBox's own background (#0e0e10) reads at 1.5:1, under the 4.5:1 text needs: pick a text further from the background"},
		{"pale ground, own text", `{"theme":{"dark":{"background":"#f0f0f0"}}}`, "AgentBox's own text (#f4f4f5) on theme.dark.background #f0f0f0 reads at 1.0:1, under the 4.5:1 text needs: set theme.dark.text too"},
		{"unreadable sidebar", `{"theme":{"light":{"sidebar":"#222222"}}}`, "AgentBox's own text (#18181b) on theme.light.sidebar #222222"},
		{"muted text", `{"theme":{"light":{"mutedText":"#bbbbbb"}}}`, "theme.light.mutedText #bbbbbb on AgentBox's own background (#ffffff)"},
		{"css from the network", `{"css":"body { background: url(https://example.com/x.png) }"}`, "css loads url(https://example.com/x.png): custom CSS can't load files or anything from the network"},
		{"css import", `{"css":"@import 'x.css';"}`, "css has an @import"},
		{"css unclosed", `{"css":"body { color: red; "}`, "css has 1 { never closed"},
		{"css stray brace", `{"css":"} body {}"}`, "css has a } with no { before it"},
		{"css comment", `{"css":"/* body {}"}`, "never closed"},
		{"css too big", `{"css":"` + strings.Repeat("a", MaxCSS+1) + `"}`, "custom CSS is at most 64 KiB"},
		{"newer file", `{"version":2}`, "newer than this AgentBox reads"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			patch(t, s, `{"density":"spacious"}`)
			_, err := s.Patch([]byte(tc.change))
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("Patch(%.80s) = %v, want an error saying %q", tc.change, err, tc.says)
			}
			if c := s.Status().Customization; c.Density != api.DensitySpacious {
				t.Errorf("a refused change touched the file: %+v", c)
			}
		})
	}
}

// What the schema allows, at its edges, is taken.
func TestGoodValuesAreTaken(t *testing.T) {
	for _, change := range []string{
		`{"theme":{"dark":{"background":"#f0f0f0","text":"#111111","mutedText":"#444444"}}}`,
		`{"theme":{"light":{"background":"#fdf6e3","text":"#073642","mutedText":"#52525b","sidebar":"#eee8d5"}}}`,
		`{"fonts":{"ui":"\"IBM Plex Sans\", 'Noto Sans', sans-serif","mono":"JetBrains Mono","size":12}}`,
		`{"fonts":{"size":18}}`,
		`{"sidebar":{"top":["home","new-task"],"foot":["running","settings"],"hidden":["office","update","filters"]}}`,
		`{"css":"/* } */ .x::after { content: \"}\"; background: url(data:image/png;base64,AAAA) }"}`,
		`{"theme":{"radius":"round"},"density":"compact"}`,
	} {
		if _, err := newStore(t).Patch([]byte(change)); err != nil {
			t.Errorf("Patch(%s) = %v", change, err)
		}
	}
}

// A file broken by hand isn't worn, says why, and isn't patched over —
// which would lose what the user wrote — until it is fixed or reset.
func TestABrokenFileIsReportedAndCanBeReset(t *testing.T) {
	s := newStore(t)
	patch(t, s, `{"density":"compact"}`)
	if err := os.WriteFile(s.Path, []byte(`{"density":"cosy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status := s.Status()
	if !strings.Contains(status.Problem, `density is "cosy"`) || !strings.Contains(status.Problem, "reset it") || status.Customization.Density != "" {
		t.Fatalf("Status() = %+v, want the default and the problem", status)
	}
	if _, err := s.Patch([]byte(`{"density":"spacious"}`)); err == nil || !strings.Contains(err.Error(), "while it is broken") {
		t.Fatalf("a broken file was patched over: %v", err)
	}
	reset, err := s.Reset()
	if err != nil || !reflect.DeepEqual(reset.Customization, Default()) {
		t.Fatalf("Reset() = %+v, %v", reset, err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Errorf("Reset left the file: %v", err)
	}
	if _, err := s.Reset(); err != nil {
		t.Errorf("resetting what is already the default: %v", err)
	}
	if status := s.Status(); status.Problem != "" {
		t.Errorf("after a reset: %+v", status)
	}
}

// The guide a chat reads before it changes anything names every field of the
// schema, every colour role, and every sidebar item.
func TestGuideNamesEveryField(t *testing.T) {
	guide := Guide()
	var walk func(prefix string, t reflect.Type) []string
	walk = func(prefix string, typ reflect.Type) []string {
		var out []string
		for f := range typ.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "version" {
				continue
			}
			if f.Type.Kind() == reflect.Struct {
				out = append(out, walk(prefix+name+".", f.Type)...)
				continue
			}
			out = append(out, prefix+name)
		}
		return out
	}
	for _, field := range walk("", reflect.TypeFor[api.Customization]()) {
		// A colour role is named once for both modes: "theme.dark and
		// theme.light: … background (…)".
		if role, ok := strings.CutPrefix(field, "theme.dark."); ok {
			field = role
		} else if role, ok := strings.CutPrefix(field, "theme.light."); ok {
			field = role
		}
		if !strings.Contains(guide, field) {
			t.Errorf("the guide doesn't say what %s is", field)
		}
	}
	for _, item := range slices.Concat(api.SidebarTopItems, api.SidebarFootItems) {
		if !strings.Contains(guide, item) {
			t.Errorf("the guide doesn't name the sidebar's %s", item)
		}
	}
}

// The file is what Marshal writes: Decode takes back everything it wrote.
func TestTheFileRoundTrips(t *testing.T) {
	c := api.Customization{
		Version: api.CustomizationVersion,
		Theme:   api.CustomTheme{Accent: "#123456", Radius: api.RadiusSharp, Light: api.CustomColors{Success: "#047857"}},
		Fonts:   api.CustomFonts{Mono: "Iosevka", Size: 13},
		Density: api.DensitySpacious,
		Sidebar: api.CustomSidebar{Top: []string{"filters"}, Hidden: []string{"update"}},
		CSS:     ".x { color: red }",
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(raw)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("Decode(Marshal(c)) = %+v, %v", got, err)
	}
}

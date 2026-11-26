// Package customize keeps the user's customization of the desktop app — its
// colours, fonts, density, sidebar and a stylesheet of their own
// (api.Customization) — in one file, and holds every change to it to the
// schema: a value that isn't one of a field's, or a colour that would leave
// text unreadable, is refused with what it should be instead.
//
// The file is the user's: in their configuration folder, written as indented
// JSON, readable and editable by hand. A hand edit that breaks it isn't fatal:
// Status reports the problem and the default look, and the app says so in
// Settings, where it can be reset.
//
// It is a layer over the app, not a change to it: what a chat may change is
// what this schema names, and custom CSS, which can restyle anything, is only
// ever a stylesheet put after the app's own, which Reset takes away.
package customize

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"agentbox/internal/api"
)

// FileName is the file's name in AgentBox's configuration folder.
const FileName = "customization.json"

// MaxCSS bounds the custom stylesheet: room for a theme of its own, not for a
// framework.
const MaxCSS = 64 << 10

// Store is the file, and the one writer of it.
type Store struct {
	Path string
	mu   sync.Mutex
}

// Status is the customization the app should wear. A file that is missing is
// the default, and one that can't be read or isn't valid is the default too,
// with the problem said.
func (s *Store) Status() api.CustomizationStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := api.CustomizationStatus{Path: s.Path, Customization: Default()}
	c, err := s.load()
	if err != nil {
		out.Problem = fmt.Sprintf("%s isn't being used: %v. Fix it, or reset it in Settings → Appearance.", s.Path, err)
		return out
	}
	out.Customization = c
	return out
}

// Patch changes the customization by a JSON merge patch (RFC 7396): what it
// names is set, a null puts a field back to AgentBox's own, and a list
// replaces the list it names. The result is validated whole before anything
// is written, so a refused change leaves the file as it was.
func (s *Store) Patch(patch []byte) (api.CustomizationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var changes any
	if err := json.Unmarshal(patch, &changes); err != nil {
		return api.CustomizationStatus{}, fmt.Errorf("the change isn't JSON: %w", err)
	}
	if _, ok := changes.(map[string]any); !ok {
		return api.CustomizationStatus{}, errors.New("the change is an object of the fields to set, like {\"density\":\"compact\"}")
	}
	current, err := s.load()
	if err != nil {
		return api.CustomizationStatus{}, fmt.Errorf("%s can't be changed while it is broken (%v): fix it, or reset it", s.Path, err)
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return api.CustomizationStatus{}, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return api.CustomizationStatus{}, err
	}
	merged, err := json.Marshal(mergePatch(doc, changes))
	if err != nil {
		return api.CustomizationStatus{}, err
	}
	next, err := Decode(merged)
	if err != nil {
		return api.CustomizationStatus{}, err
	}
	if err := s.write(next); err != nil {
		return api.CustomizationStatus{}, err
	}
	return api.CustomizationStatus{Path: s.Path, Customization: next}, nil
}

// Reset puts AgentBox's own look back, removing the file.
func (s *Store) Reset() (api.CustomizationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return api.CustomizationStatus{}, err
	}
	return api.CustomizationStatus{Path: s.Path, Customization: Default()}, nil
}

func (s *Store) load() (api.Customization, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return api.Customization{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Default(), nil
	}
	return Decode(data)
}

// write replaces the file whole: a reader never finds half of one.
func (s *Store) write(c api.Customization) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".customization-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// Default is AgentBox's own look: nothing customized.
func Default() api.Customization {
	return api.Customization{Version: api.CustomizationVersion}
}

// mergePatch applies an RFC 7396 merge patch to target.
func mergePatch(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	}
	for k, v := range p {
		if v == nil {
			delete(t, k)
			continue
		}
		t[k] = mergePatch(t[k], v)
	}
	return t
}

// Decode reads a customization, holding it to the schema: no field it
// doesn't have, every value one of its field's, and the result normalized
// (colours lower-case #rrggbb, lists without repeats).
func Decode(data []byte) (api.Customization, error) {
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return api.Customization{}, fmt.Errorf("it isn't JSON: %w", err)
	}
	if err := checkFields("", doc, reflect.TypeFor[api.Customization]()); err != nil {
		return api.Customization{}, err
	}
	var c api.Customization
	if err := json.Unmarshal(data, &c); err != nil {
		var typ *json.UnmarshalTypeError
		if errors.As(err, &typ) {
			return api.Customization{}, fmt.Errorf("%s is %s: it must be %s", typ.Field, typ.Value, kindOf(typ.Type))
		}
		return api.Customization{}, err
	}
	if err := Validate(&c); err != nil {
		return api.Customization{}, err
	}
	return c, nil
}

// checkFields refuses a field the schema doesn't have, naming the ones it
// does: json.Unmarshal would drop it silently, and a chat that misspelt one
// would think it had changed something.
func checkFields(path string, v any, t reflect.Type) error {
	m, ok := v.(map[string]any)
	if !ok || t.Kind() != reflect.Struct {
		return nil // a value of the wrong kind is json.Unmarshal's to name
	}
	fields := map[string]reflect.Type{}
	var names []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		fields[name] = f.Type
		if name != "version" {
			names = append(names, name)
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		ft, ok := fields[k]
		if !ok {
			where := "the customization"
			if path != "" {
				where = path
			}
			return fmt.Errorf("%s isn't a setting: %s has %s", join(path, k), where, strings.Join(names, ", "))
		}
		if err := checkFields(join(path, k), m[k], ft); err != nil {
			return err
		}
	}
	return nil
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func kindOf(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "text"
	case reflect.Int:
		return "a whole number"
	case reflect.Slice:
		return "a list of " + kindOf(t.Elem())
	case reflect.Struct:
		return "an object"
	}
	return t.String()
}

var (
	hexColor  = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	fontToken = regexp.MustCompile(`^("[^"\\;{}<>]+"|'[^'\\;{}<>]+'|[A-Za-z0-9][A-Za-z0-9 _-]*)$`)
)

// Validate holds c to the schema and normalizes it in place.
func Validate(c *api.Customization) error {
	switch {
	case c.Version == 0:
		c.Version = api.CustomizationVersion
	case c.Version > api.CustomizationVersion:
		return fmt.Errorf("version is %d, newer than this AgentBox reads (%d): update AgentBox, or reset it", c.Version, api.CustomizationVersion)
	case c.Version < 0:
		return fmt.Errorf("version is %d: it is %d", c.Version, api.CustomizationVersion)
	}
	if err := validateTheme(&c.Theme); err != nil {
		return err
	}
	if err := validateFonts(&c.Fonts); err != nil {
		return err
	}
	c.Density = strings.ToLower(strings.TrimSpace(c.Density))
	if c.Density == api.DensityComfortable {
		c.Density = ""
	}
	if c.Density != "" && c.Density != api.DensityCompact && c.Density != api.DensitySpacious {
		return fmt.Errorf("density is %q: it is %s, %s (AgentBox's own) or %s", c.Density, api.DensityCompact, api.DensityComfortable, api.DensitySpacious)
	}
	if err := validateSidebar(&c.Sidebar); err != nil {
		return err
	}
	return validateCSS(c.CSS)
}

func validateTheme(t *api.CustomTheme) error {
	var err error
	if t.Accent, err = color("theme.accent", t.Accent); err != nil {
		return err
	}
	t.Radius = strings.ToLower(strings.TrimSpace(t.Radius))
	if t.Radius == api.RadiusDefault {
		t.Radius = ""
	}
	switch t.Radius {
	case "", api.RadiusSharp, api.RadiusSubtle, api.RadiusRound:
	default:
		return fmt.Errorf("theme.radius is %q: it is %s, %s, %s (AgentBox's own) or %s", t.Radius, api.RadiusSharp, api.RadiusSubtle, api.RadiusDefault, api.RadiusRound)
	}
	for _, mode := range []struct {
		name string
		c    *api.CustomColors
		own  ownColors
	}{{"dark", &t.Dark, darkOwn}, {"light", &t.Light, lightOwn}} {
		if err := validateColors("theme."+mode.name, mode.c, mode.own); err != nil {
			return err
		}
	}
	return nil
}

// ownColors are the colours of AgentBox's own of a mode that text is held to
// being readable on (desktop tokens.css: --ab-ink, --ab-primary, --ab-muted).
type ownColors struct{ background, text, mutedText string }

var (
	darkOwn  = ownColors{background: "#0e0e10", text: "#f4f4f5", mutedText: "#a1a1aa"}
	lightOwn = ownColors{background: "#ffffff", text: "#18181b", mutedText: "#52525b"}
)

// minContrast is the WCAG AA ratio every text role in the app keeps
// (styles.css), which a customization is held to too.
const minContrast = 4.5

func validateColors(path string, c *api.CustomColors, own ownColors) error {
	v := reflect.ValueOf(c).Elem()
	for i := range v.NumField() {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		norm, err := color(path+"."+name, v.Field(i).String())
		if err != nil {
			return err
		}
		v.Field(i).SetString(norm)
	}
	// Text has to be read on what it sits on: the window, and the places
	// given colours of their own. A role left alone is AgentBox's, so a dark
	// window given a pale background without a text colour is refused too.
	texts := []struct{ name, value, own string }{{"text", c.Text, own.text}, {"mutedText", c.MutedText, own.mutedText}}
	grounds := []struct{ name, value, own string }{{"background", c.Background, own.background}, {"sidebar", c.Sidebar, ""}, {"composer", c.Composer, ""}, {"popover", c.Popover, ""}}
	for _, text := range texts {
		for _, ground := range grounds {
			if ground.value == "" && ground.own == "" || text.value == "" && ground.value == "" {
				continue // both AgentBox's own, or a place that wears the window's
			}
			fg, bg := cmp.Or(text.value, text.own), cmp.Or(ground.value, ground.own)
			ratio := contrast(fg, bg)
			if ratio >= minContrast {
				continue
			}
			describe := func(name, value, own string) string {
				if value == "" {
					return fmt.Sprintf("AgentBox's own %s (%s)", name, own)
				}
				return fmt.Sprintf("%s.%s %s", path, name, value)
			}
			fix := fmt.Sprintf("pick a %s further from the %s", text.name, ground.name)
			if text.value == "" {
				fix = fmt.Sprintf("set %s.%s too", path, text.name)
			}
			return fmt.Errorf("%s on %s reads at %.1f:1, under the %.1f:1 text needs: %s",
				describe(text.name, text.value, text.own), describe(ground.name, ground.value, ground.own),
				math.Floor(ratio*10)/10, minContrast, fix)
		}
	}
	return nil
}

// color normalizes a colour to lower-case #rrggbb, or refuses it.
func color(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !hexColor.MatchString(value) {
		return "", fmt.Errorf("%s is %q: a colour is #rrggbb, like #22c55e", field, value)
	}
	value = strings.ToLower(value)
	if len(value) == 4 {
		value = "#" + strings.Repeat(value[1:2], 2) + strings.Repeat(value[2:3], 2) + strings.Repeat(value[3:4], 2)
	}
	return value, nil
}

// contrast is WCAG's contrast ratio between two #rrggbb colours, 1 to 21.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	n, _ := strconv.ParseUint(hex[1:], 16, 32)
	var sum float64
	for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
		c := float64(n>>(16-8*i)&0xff) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		sum += weight * c
	}
	return sum
}

func validateFonts(f *api.CustomFonts) error {
	for _, font := range []struct {
		name  string
		value *string
	}{{"fonts.ui", &f.UI}, {"fonts.mono", &f.Mono}} {
		*font.value = strings.TrimSpace(*font.value)
		if *font.value == "" {
			continue
		}
		if len(*font.value) > 200 {
			return fmt.Errorf("%s is %d characters: a font list is at most 200", font.name, len(*font.value))
		}
		for family := range strings.SplitSeq(*font.value, ",") {
			if !fontToken.MatchString(strings.TrimSpace(family)) {
				return fmt.Errorf("%s is %q: it is a font's name, or several split by commas, quoting a name with spaces, like \"IBM Plex Sans\", sans-serif", font.name, *font.value)
			}
		}
	}
	if f.Size == api.FontSizeDefault {
		f.Size = 0
	}
	if f.Size != 0 && (f.Size < api.FontSizeMin || f.Size > api.FontSizeMax) {
		return fmt.Errorf("fonts.size is %d: it is %d to %d pixels (%d is AgentBox's own)", f.Size, api.FontSizeMin, api.FontSizeMax, api.FontSizeDefault)
	}
	return nil
}

func validateSidebar(s *api.CustomSidebar) error {
	all := slices.Concat(api.SidebarTopItems, api.SidebarFootItems)
	known := func(field, item string) error {
		if slices.Contains(all, item) {
			return nil
		}
		return fmt.Errorf("%s has %q, which isn't a sidebar item: the top has %s, and the foot %s", field, item,
			strings.Join(api.SidebarTopItems, ", "), strings.Join(api.SidebarFootItems, ", "))
	}
	for _, list := range []struct {
		name, other string
		items       *[]string
		own         []string
	}{{"sidebar.top", "sidebar.foot", &s.Top, api.SidebarTopItems}, {"sidebar.foot", "sidebar.top", &s.Foot, api.SidebarFootItems}} {
		seen := map[string]bool{}
		for i, item := range *list.items {
			item = strings.TrimSpace(item)
			(*list.items)[i] = item
			if err := known(list.name, item); err != nil {
				return err
			}
			if !slices.Contains(list.own, item) {
				return fmt.Errorf("%s has %q, which is in the sidebar's other place: put it in %s", list.name, item, list.other)
			}
			if seen[item] {
				return fmt.Errorf("%s has %q twice", list.name, item)
			}
			seen[item] = true
		}
	}
	var hidden []string
	for _, item := range s.Hidden {
		item = strings.TrimSpace(item)
		if err := known("sidebar.hidden", item); err != nil {
			return err
		}
		if item == api.SidebarSettings {
			return errors.New("sidebar.hidden has settings, which can't be hidden: Settings is where the customization is reset from")
		}
		if !slices.Contains(hidden, item) {
			hidden = append(hidden, item)
		}
	}
	s.Hidden = hidden
	return nil
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssString  = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	cssURL     = regexp.MustCompile(`(?i)url\(\s*(["']?)\s*([^"')\s]*)`)
	cssImport  = regexp.MustCompile(`(?i)@import\b`)
)

// validateCSS holds the custom stylesheet to what can be checked without a
// CSS parser: its size, braces that pair, and nothing loaded from elsewhere —
// the app's window fetches nothing but the daemon, and a stylesheet that
// reached the network would send it the user's every page view.
func validateCSS(css string) error {
	if len(css) > MaxCSS {
		return fmt.Errorf("css is %d KiB: custom CSS is at most %d KiB", len(css)>>10, MaxCSS>>10)
	}
	bare := cssComment.ReplaceAllString(css, " ")
	if strings.Contains(bare, "/*") {
		return errors.New("css has a /* comment that is never closed")
	}
	for _, m := range cssURL.FindAllStringSubmatch(bare, -1) {
		if !strings.HasPrefix(strings.ToLower(m[2]), "data:") {
			return fmt.Errorf("css loads url(%s): custom CSS can't load files or anything from the network; a data: URL works", m[2])
		}
	}
	bare = cssString.ReplaceAllString(bare, `""`)
	if cssImport.MatchString(bare) {
		return errors.New("css has an @import: custom CSS can't load other stylesheets; put what you need in it")
	}
	depth := 0
	for i, r := range bare {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return fmt.Errorf("css has a } with no { before it, at character %d", i+1)
			}
		}
	}
	if depth > 0 {
		return fmt.Errorf("css has %d { never closed", depth)
	}
	return nil
}

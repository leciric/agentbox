package projecticon

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	ico = append([]byte("\x00\x00\x01\x00"), make([]byte, 32)...)
	svg = []byte(`<?xml version="1.0"?><!-- logo --><svg xmlns="http://www.w3.org/2000/svg"></svg>`)
)

func write(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Each case is a repository and the file that should win: earlier rules
// beat later ones, and what isn't an image, or is too big, is passed over.
func TestFindInRepoOrder(t *testing.T) {
	t.Parallel()
	big := append(bytes.Clone(png), make([]byte, MaxBytes)...)
	for _, tc := range []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"root favicon.svg before favicon.ico", map[string][]byte{"favicon.ico": ico, "favicon.svg": svg}, "favicon.svg"},
		{"root favicon before public's", map[string][]byte{"public/favicon.svg": svg, "favicon.ico": ico}, "favicon.ico"},
		{"public before static", map[string][]byte{"static/favicon.svg": svg, "public/favicon.ico": ico}, "public/favicon.ico"},
		{"next's app router", map[string][]byte{"src/app/icon.png": png, "logo.svg": svg}, "src/app/icon.png"},
		{"a favicon before a logo", map[string][]byte{"logo.svg": svg, "web/favicon.ico": ico}, "web/favicon.ico"},
		{"logo.svg before icon.png", map[string][]byte{"icon.png": png, "logo.svg": svg}, "logo.svg"},
		{".github's logo", map[string][]byte{".github/logo.png": png}, ".github/logo.png"},
		{"a named logo in public, svg first", map[string][]byte{"public/brand-logo.png": png, "public/Logo-dark.svg": svg}, "public/Logo-dark.svg"},
		{"a named logo, not a folder's", map[string][]byte{"assets/logos/x.svg": svg, "assets/my_logo.png": png}, "assets/my_logo.png"},
		{"package.json's icon", map[string][]byte{"package.json": []byte(`{"icon":"./res/app.png"}`), "res/app.png": png}, "res/app.png"},
		{"electron-builder's build.icon", map[string][]byte{"package.json": []byte(`{"build":{"icon":"build/icon.ico"}}`), "build/icon.ico": ico}, "build/icon.ico"},
		{"build.icon with no extension is a folder, found by its name", map[string][]byte{"package.json": []byte(`{"build":{"icon":"build"}}`), "build/icon.png": png}, "build/icon.png"},
		{"a manifest's svg before its png", map[string][]byte{
			"public/manifest.json": []byte(`{"icons":[{"src":"/i/192.png","sizes":"192x192"},{"src":"/i/any.svg","sizes":"any"}]}`),
			"public/i/192.png":     png, "public/i/any.svg": svg}, "public/i/any.svg"},
		{"a manifest's largest png up to 512", map[string][]byte{
			"public/site.webmanifest": []byte(`{"icons":[{"src":"a.png","sizes":"192x192"},{"src":"b.png","sizes":"512x512"},{"src":"c.png","sizes":"1024x1024"}]}`),
			"public/a.png":            png, "public/b.png": png, "public/c.png": png}, "public/b.png"},
		{"not an image", map[string][]byte{"favicon.ico": []byte("<html>not found</html>"), "logo.png": png}, "logo.png"},
		{"too big", map[string][]byte{"favicon.ico": big, "logo.png": png}, "logo.png"},
		{"nothing", map[string][]byte{"README.md": []byte("# hi")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			write(t, root, tc.files)
			icon, ok := FindInRepo(root)
			if icon.From != tc.want || ok != (tc.want != "") {
				t.Errorf("FindInRepo() = %q, %v; want %q", icon.From, ok, tc.want)
			}
		})
	}
}

// A symlink pointing out of the repository isn't followed.
func TestFindInRepoStaysInside(t *testing.T) {
	t.Parallel()
	outside := t.TempDir()
	write(t, outside, map[string][]byte{"secret.png": png})
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "favicon.ico")); err != nil {
		t.Fatal(err)
	}
	write(t, root, map[string][]byte{"package.json": []byte(`{"icon":"../` + filepath.Base(outside) + `/secret.png"}`)})
	if icon, ok := FindInRepo(root); ok {
		t.Errorf("FindInRepo() = %q, want nothing", icon.From)
	}
}

// distinct is a png unlike any other made by it, so Candidates doesn't take
// two for the same icon.
func distinct(i byte) []byte { return append(bytes.Clone(png), i) }

// Past the usual places, the walk finds an app's icon however deep its
// package keeps it, ranks what a package names first and an svg beside it
// above that, and passes over dependencies, build output, tests' files and
// Android's adaptive layers. This is the shape of AgentBox's own repository.
func TestCandidatesWalk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, map[string][]byte{
		"desktop/package.json":                      []byte(`{"build":{"mac":{"icon":"build/icon.png"},"linux":{"icon":"build/icon.png"}}}`),
		"desktop/build/icon.png":                    distinct(1),
		"desktop/build/icon.svg":                    svg,
		"mobile/app.json":                           []byte(`{"expo":{"icon":"./assets/icon.png"}}`),
		"mobile/assets/icon.png":                    distinct(2),
		"mobile/assets/favicon.png":                 distinct(3),
		"mobile/assets/splash-icon.png":             distinct(4),
		"mobile/assets/android-icon-foreground.png": distinct(5),
		"desktop/node_modules/x/logo.svg":           []byte(`<svg id="dep"/>`),
		"desktop/out/icon.png":                      distinct(6),
		"internal/x/testdata/logo.png":              distinct(7),
		".cache/logo.png":                           distinct(8),
		"a/b/c/d/e/logo.png":                        distinct(9),
		"docs/README.md":                            []byte("# hi"),
		"mobile/assets/same-icon.png":               distinct(2),
	})
	var got []string
	for _, c := range Candidates(root) {
		got = append(got, c.From)
	}
	want := []string{"desktop/build/icon.svg", "desktop/build/icon.png", "mobile/assets/icon.png", "mobile/assets/favicon.png", "mobile/assets/android-icon-foreground.png", "mobile/assets/splash-icon.png"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("Candidates() =\n%q\nwant\n%q", got, want)
	}
	// The usual places still come first.
	write(t, root, map[string][]byte{"public/favicon.ico": ico})
	if icon, _ := FindInRepo(root); icon.From != "public/favicon.ico" {
		t.Errorf("FindInRepo() = %q, want public/favicon.ico", icon.From)
	}
}

// However many icons a repository has, Candidates returns MaxCandidates.
func TestCandidatesCapped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string][]byte{}
	for i := range 20 {
		files[fmt.Sprintf("img/logo-%02d.png", i)] = distinct(byte(i))
	}
	write(t, root, files)
	if got := Candidates(root); len(got) != MaxCandidates || got[0].From != "img/logo-00.png" {
		t.Errorf("Candidates() = %d, first %q", len(got), got[0].From)
	}
}

func TestSniff(t *testing.T) {
	t.Parallel()
	for data, want := range map[string]string{
		string(png): "image/png", string(ico): "image/x-icon", string(svg): "image/svg+xml",
		"\xef\xbb\xbf <!DOCTYPE svg><svg/>": "image/svg+xml",
		"<svgx>":                            "", "<html><svg></svg></html>": "", "GIF89a....": "image/gif", "plain": "",
	} {
		if got := Sniff([]byte(data)); got != want {
			t.Errorf("Sniff(%q) = %q, want %q", data, got, want)
		}
	}
}

func TestParseImage(t *testing.T) {
	t.Parallel()
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	data, mime, err := ParseImage("data:image/vnd.microsoft.icon;base64," + b64(ico))
	if err != nil || mime != "image/x-icon" || !bytes.Equal(data, ico) {
		t.Errorf("ParseImage(ico) = %d bytes, %q, %v", len(data), mime, err)
	}
	if _, mime, err := ParseImage("data:image/svg+xml;base64," + b64(svg)); err != nil || mime != "image/svg+xml" {
		t.Errorf("ParseImage(svg) = %q, %v", mime, err)
	}
	for name, in := range map[string]string{
		"not a data URL":       "https://example.com/x.png",
		"not base64":           "data:image/png," + string(png),
		"bad base64":           "data:image/png;base64,!!!",
		"a disallowed type":    "data:text/html;base64," + b64([]byte("<html>")),
		"lying about its type": "data:image/png;base64," + b64(svg),
		"too big":              "data:image/png;base64," + b64(append(bytes.Clone(png), make([]byte, MaxBytes)...)),
	} {
		if _, _, err := ParseImage(in); err == nil {
			t.Errorf("ParseImage(%s) succeeded", name)
		}
	}
}

func TestParseEmoji(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"🐶", " 🚀 ", "👨‍👩‍👧‍👦", "🇧🇷", "👍🏽"} {
		if _, err := ParseEmoji(ok); err != nil {
			t.Errorf("ParseEmoji(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "🐶 🐱", "a\tb", strings.Repeat("🐶", 11), "\xff"} {
		if got, err := ParseEmoji(bad); err == nil {
			t.Errorf("ParseEmoji(%q) = %q, want an error", bad, got)
		}
	}
}

package projecticon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
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
		{"build.icon with no extension is a folder", map[string][]byte{"package.json": []byte(`{"build":{"icon":"build"}}`), "build/icon.png": png}, ""},
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

func TestDetectFallsBackToTheAvatar(t *testing.T) {
	t.Parallel()
	var asked []string
	fetch := func(_ context.Context, u string) ([]byte, string, error) {
		asked = append(asked, u)
		switch {
		case strings.Contains(u, "/leciric.png"):
			return png, "image/png", nil
		case strings.Contains(u, "/html.png"):
			return []byte("<html>"), "text/html", nil
		case strings.Contains(u, "/liar.png"):
			return []byte("<html>"), "image/png", nil
		}
		return nil, "", errors.New("404")
	}
	owner := func(o string) func() string { return func() string { return o } }
	root := t.TempDir()
	icon, ok := Detect(context.Background(), root, Options{Owner: owner("leciric"), Fetch: fetch})
	if !ok || icon.From != "github.com/leciric" || icon.MIME != "image/png" {
		t.Errorf("Detect() = %+v, %v", icon, ok)
	}
	if len(asked) != 1 || asked[0] != "https://github.com/leciric.png?size=128" {
		t.Errorf("fetched %q", asked)
	}
	for _, o := range []string{"html", "liar", "missing", "", "a/b"} {
		if icon, ok := Detect(context.Background(), root, Options{Owner: owner(o), Fetch: fetch}); ok {
			t.Errorf("Detect() with owner %q = %+v", o, icon)
		}
	}
	// The repository's own icon wins, without asking GitHub.
	write(t, root, map[string][]byte{"logo.svg": svg})
	asked = nil
	if icon, _ := Detect(context.Background(), root, Options{Owner: owner("leciric"), Fetch: fetch}); icon.From != "logo.svg" || asked != nil {
		t.Errorf("Detect() = %q, fetched %q", icon.From, asked)
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

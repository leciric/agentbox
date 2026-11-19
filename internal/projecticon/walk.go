package projecticon

import (
	"cmp"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

const (
	// walkDepth is how many folders deep walk looks: desktop/build/icon.svg
	// is two.
	walkDepth = 4
	// walkEntries is the most files and folders walk reads before it stops,
	// whatever the repository's size.
	walkEntries = 20000
)

// skipDirs is what walk never goes into: dependencies, build output and
// tests' files, whose icons are someone else's or copies of the project's.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "bower_components": true, "Pods": true, "third_party": true,
	"dist": true, "out": true, "target": true, "coverage": true, "venv": true, "__pycache__": true,
	"testdata": true, "fixtures": true, "__tests__": true, "__mocks__": true,
}

// unlikely is what, in a file's name, makes it a variant rather than the
// icon: Android's adaptive layers, a splash screen, a tray icon.
var unlikely = []string{"background", "foreground", "monochrome", "splash", "mask", "adaptive", "notification", "tray"}

// walk looks through root's tree, at most walkDepth folders deep and
// walkEntries entries, for image files named like an icon or logo and for
// the icons package.json, app.json and web manifests name, and returns them
// best first (rank).
func walk(r *os.Root) []string {
	fsys := r.FS()
	type found struct {
		name  string
		score int
	}
	var files []found
	named := map[string]bool{}
	entries := 0
	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entries++; entries > walkEntries {
			return fs.SkipAll
		}
		base := d.Name()
		if d.IsDir() {
			if name == "." {
				return nil
			}
			hidden := strings.HasPrefix(base, ".") && base != ".github"
			if hidden || skipDirs[base] || strings.Count(name, "/") >= walkDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		switch lower := strings.ToLower(base); {
		case lower == "package.json":
			for _, n := range packageIcons(r, name) {
				named[path.Clean(n)] = true
			}
		case lower == "app.json":
			for _, n := range appJSONIcons(r, name) {
				named[path.Clean(n)] = true
			}
		case lower == "manifest.json" || path.Ext(lower) == ".webmanifest":
			for _, n := range manifestIcons(r, name) {
				named[path.Clean(n)] = true
			}
		case iconFile(lower):
			if score, ok := rank(name, false); ok {
				files = append(files, found{name, score})
			}
		}
		return nil
	})
	// What a package or manifest names is its icon, wherever it is, and an
	// svg beside it with the same name is that icon drawn better.
	for n := range named {
		score, _ := rank(n, true)
		files = append(files, found{n, score})
		if strings.ToLower(path.Ext(n)) != ".svg" {
			svg := strings.TrimSuffix(n, path.Ext(n)) + ".svg"
			s, _ := rank(svg, true)
			files = append(files, found{svg, s + 1})
		}
	}
	slices.SortFunc(files, func(a, b found) int {
		return cmp.Or(b.score-a.score, cmp.Compare(a.name, b.name))
	})
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.name)
	}
	return out
}

// rank scores a file as a project's icon: what a package names first, then
// by its name (favicon, logo, icon), its type (svg, png, ico), and how deep
// it is. ok is false for a name that isn't an icon's.
func rank(name string, named bool) (score int, ok bool) {
	base := strings.ToLower(path.Base(name))
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch {
	case named:
		score = 100
	case stem == "favicon":
		score = 60
	case stem == "logo":
		score = 55
	case stem == "icon" || stem == "app-icon" || stem == "appicon" || stem == "app_icon":
		score = 50
	case stem == "apple-touch-icon":
		score = 45
	case strings.Contains(stem, "logo"):
		score = 40
	case strings.Contains(stem, "favicon"):
		score = 35
	case strings.Contains(stem, "icon"):
		score = 25
	default:
		return 0, false
	}
	score += map[string]int{".svg": 3, ".png": 2, ".ico": 1}[ext]
	score -= 8 * strings.Count(name, "/")
	for _, w := range unlikely {
		if strings.Contains(stem, w) {
			score -= 30
			break
		}
	}
	// A folder of icons is a UI's, not the project's.
	if dir := path.Base(path.Dir(name)); dir == "icons" && !named {
		score -= 20
	}
	return score, true
}

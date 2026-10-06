package cookieimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Reading the user's own installed browser, the way yt-dlp's
// --cookies-from-browser and t3code's browser import do: find the browsers
// in a home directory and their profiles (here), then read a profile's
// cookie store (chromium.go, firefox.go). The daemon does this in the VM,
// where the user's home is mounted at the same path; the one thing it can't
// reach is the key a Chromium browser keeps in the host's keyring, which the
// desktop app's main process fetches on the host and passes in
// (desktop/src/main/browserkeys.ts). Nothing here logs or returns a cookie's
// value.

// MaxBrowserCookies bounds what one profile may bring before expiry and the
// domain filter cut it down: a browser used for years holds a few thousand.
const MaxBrowserCookies = 50000

// The cookie-store engines a profile can have.
const (
	EngineChromium = "chromium"
	EngineFirefox  = "firefox"
)

// Profile is one profile of an installed browser, as a client picks it. ID
// is opaque: FindProfiles gives the same one back for the same home, and
// ReadProfile takes it.
type Profile struct {
	ID          string `json:"id"`
	Browser     string `json:"browser"`
	BrowserName string `json:"browserName"`
	Engine      string `json:"engine"`
	Name        string `json:"name"`
	// Keyring names the browser's entry in the host keyring (the
	// `application` attribute of its Secret Service item: chrome, chromium,
	// brave, msedge, vivaldi), empty for Firefox, whose store needs no key.
	Keyring string `json:"keyring,omitempty"`
	dir     string
	db      string
	// goos is the operating system whose profile layout this was found
	// under (darwin, linux, windows). The daemon runs in a Linux VM but
	// reads the host's home, which may be a Mac's, and Chromium's key
	// derivation uses a different PBKDF2 iteration count on macOS.
	goos string
}

// browser is a browser AgentBox can read, and where it keeps its profiles
// relative to the home directory, per GOOS. A Chromium browser's roots are
// left empty on windows: Chrome, Edge and Brave seal cookies there with
// app-bound encryption (v20), which only the browser itself opens.
type browser struct {
	id, name, engine, keyring string
	roots                     map[string][]string
}

func chromium(id, name, keyring string, mac, linux, windows []string) browser {
	return browser{id: id, name: name, engine: EngineChromium, keyring: keyring, roots: map[string][]string{
		"darwin": mac, "linux": linux, "windows": windows,
	}}
}

// browsers is every browser looked for, most common first.
var browsers = []browser{
	chromium("chrome", "Google Chrome", "chrome",
		[]string{"Library/Application Support/Google/Chrome"},
		[]string{".config/google-chrome"},
		[]string{"AppData/Local/Google/Chrome/User Data"}),
	chromium("chromium", "Chromium", "chromium",
		[]string{"Library/Application Support/Chromium"},
		[]string{".config/chromium", "snap/chromium/common/chromium"},
		nil),
	chromium("brave", "Brave", "brave",
		[]string{"Library/Application Support/BraveSoftware/Brave-Browser"},
		[]string{".config/BraveSoftware/Brave-Browser"},
		nil),
	chromium("edge", "Microsoft Edge", "msedge",
		[]string{"Library/Application Support/Microsoft Edge"},
		[]string{".config/microsoft-edge"},
		nil),
	chromium("vivaldi", "Vivaldi", "vivaldi",
		[]string{"Library/Application Support/Vivaldi"},
		[]string{".config/vivaldi"},
		nil),
	{id: "firefox", name: "Firefox", engine: EngineFirefox, roots: map[string][]string{
		"darwin":  {"Library/Application Support/Firefox"},
		"linux":   {".mozilla/firefox", "snap/firefox/common/.mozilla/firefox"},
		"windows": {"AppData/Roaming/Mozilla/Firefox"},
	}},
}

// FindProfiles lists the profiles of every browser installed under home. The
// daemon reads the host's home mounted into the VM, and can't tell the host's
// OS apart, so it tries every OS's layout: the roots are disjoint, and each
// profile remembers which OS it was found under. A browser with no readable
// profile is left out. It touches only each browser's own files, never a
// cookie value.
func FindProfiles(home string) []Profile {
	var out []Profile
	for _, b := range browsers {
		for _, goos := range []string{"darwin", "linux", "windows"} {
			for _, rel := range b.roots[goos] {
				root := filepath.Join(home, filepath.FromSlash(rel))
				if _, err := os.Stat(root); err != nil {
					continue
				}
				if b.engine == EngineFirefox {
					out = append(out, firefoxProfiles(b, root, goos)...)
				} else {
					out = append(out, chromiumProfiles(b, root, goos)...)
				}
			}
		}
	}
	return out
}

// chromiumProfiles reads a Chromium user-data directory. The profiles are
// the keys of profile.info_cache in Local State, each a sub-directory with a
// Cookies database; a directory with a database but no entry (an older
// browser, or a portable copy) is taken as the one profile "Default".
func chromiumProfiles(b browser, root, goos string) []Profile {
	names := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(root, "Local State")); err == nil {
		var state struct {
			Profile struct {
				InfoCache map[string]struct {
					Name string `json:"name"`
				} `json:"info_cache"`
			} `json:"profile"`
		}
		if json.Unmarshal(data, &state) == nil {
			for dir, info := range state.Profile.InfoCache {
				names[dir] = info.Name
			}
		}
	}
	// The directories to consider: those named in Local State, and any that
	// already hold a cookie database.
	dirs := make([]string, 0, len(names))
	for dir := range names {
		dirs = append(dirs, dir)
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.IsDir() && !slices.Contains(dirs, e.Name()) && chromiumCookieDB(filepath.Join(root, e.Name())) != "" {
				dirs = append(dirs, e.Name())
			}
		}
	}
	slices.Sort(dirs)
	var out []Profile
	for _, dir := range dirs {
		if !safeProfileDir(dir) {
			continue
		}
		pdir := filepath.Join(root, dir)
		db := chromiumCookieDB(pdir)
		if db == "" {
			continue
		}
		name := names[dir]
		if name == "" {
			name = dir
		}
		out = append(out, Profile{
			ID:      b.id + "\x00" + goos + "\x00" + dir,
			Browser: b.id, BrowserName: b.name, Engine: b.engine, Keyring: b.keyring,
			Name: name, dir: pdir, db: db, goos: goos,
		})
	}
	return out
}

// chromiumCookieDB is a profile's cookie database: the newer Network/Cookies,
// else the legacy Cookies, else "".
func chromiumCookieDB(profileDir string) string {
	for _, rel := range []string{filepath.Join("Network", "Cookies"), "Cookies"} {
		p := filepath.Join(profileDir, rel)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// safeProfileDir refuses a profile directory that isn't a plain name, so a
// crafted Local State can't walk out of the user-data directory.
func safeProfileDir(dir string) bool {
	return dir != "" && dir != "." && dir != ".." &&
		!strings.ContainsAny(dir, `/\`) && !strings.Contains(dir, "..")
}

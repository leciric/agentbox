package cookieimport

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// On Windows, Chrome, Edge and Brave are found as app-bound profiles: listed
// so the desktop app can read them on the host, but ReadProfile refuses them
// (the daemon in the VM can't open app-bound cookies).
func TestWindowsAppBoundProfiles(t *testing.T) {
	home := t.TempDir()
	if got := FindProfiles(home); len(got) != 0 {
		t.Fatalf("empty home: %+v", got)
	}
	for _, root := range []string{
		"AppData/Local/Google/Chrome/User Data",
		"AppData/Local/Microsoft/Edge/User Data",
		"AppData/Local/BraveSoftware/Brave-Browser/User Data",
	} {
		dir := filepath.Join(home, filepath.FromSlash(root), "Default", "Network")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Cookies"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	profiles := FindProfiles(home)
	if len(profiles) != 3 {
		t.Fatalf("FindProfiles = %+v", profiles)
	}
	for _, p := range profiles {
		if !p.AppBound() {
			t.Errorf("%s should be app-bound: %+v", p.Browser, p)
		}
		if p.Engine != EngineChromium || p.Dir() != "Default" {
			t.Errorf("%s: engine %q dir %q", p.Browser, p.Engine, p.Dir())
		}
		if _, _, err := ReadProfile(home, p.ID, "", time.Now()); err != ErrAppBound {
			t.Errorf("ReadProfile(%s) = %v, want ErrAppBound", p.Browser, err)
		}
	}
	// brave, chrome, edge: FindProfiles returns browsers in its own order.
	seen := map[string]bool{}
	for _, p := range profiles {
		seen[p.Browser] = true
	}
	for _, id := range []string{"chrome", "edge", "brave"} {
		if !seen[id] {
			t.Errorf("missing %s among %+v", id, profiles)
		}
	}
}

package cookieimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindUnreadableWindowsBrowsers(t *testing.T) {
	home := t.TempDir()
	if got := FindUnreadable(home); len(got) != 0 {
		t.Fatalf("empty home: %+v", got)
	}
	for _, rel := range []string{
		"AppData/Local/Google/Chrome/User Data/Default/Network",
		"AppData/Local/Microsoft/Edge/User Data",
		"AppData/Local/BraveSoftware/Brave-Browser/User Data",
	} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A cookie database Chrome wrote must never be what makes it importable.
	if err := os.WriteFile(filepath.Join(home, "AppData/Local/Google/Chrome/User Data/Default/Network/Cookies"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindUnreadable(home)
	if len(got) != 3 || got[0].Browser != "chrome" || got[1].Browser != "edge" || got[2].Browser != "brave" {
		t.Fatalf("FindUnreadable = %+v", got)
	}
	if profiles := FindProfiles(home); len(profiles) != 0 {
		t.Errorf("Windows Chrome, Edge and Brave must not be listed as readable: %+v", profiles)
	}
}

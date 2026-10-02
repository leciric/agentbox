package naming

import "testing"

func TestProjectSlug(t *testing.T) {
	for display, want := range map[string]string{
		"Organic Web App": "organic-web-app",
		"organicWebApp":   "organic-web-app", "OrganicWebApp": "organic-web-app", "HTTPServer2Go": "http-server2-go", "São Paulo": "sao-paulo", "ORGANIC": "organic",
		"  Café  Crème! ": "cafe-creme",
		"pawly":           "pawly",
		"my--app":         "my-app",
		"2048 Game":       "project-2048-game",
		"a":               "project-a",
		"日本語のアプリ":         "project",
		"Über/Projekt_v2": "uber-projekt-v2",
		"":                "project",
		"a really long project name that goes on": "a-really-long-project-name-tha",
	} {
		got := ProjectSlug(display, nil)
		if got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", display, got, want)
		}
		if err := Validate("project", got, MaxProjectSlug); err != nil {
			t.Errorf("ProjectSlug(%q) = %q: %v", display, got, err)
		}
	}
}

func TestProjectSlugCollisions(t *testing.T) {
	taken := map[string]bool{"organic-web-app": true, "organic-web-app-2": true}
	if got := ProjectSlug("Organic Web App", func(s string) bool { return taken[s] }); got != "organic-web-app-3" {
		t.Errorf("got %q, want organic-web-app-3", got)
	}
	// A suffix still fits within the limit, cutting the base rather than
	// overflowing it.
	long := "a really long project name that goes on"
	first := ProjectSlug(long, nil)
	got := ProjectSlug(long, func(s string) bool { return s == first })
	if got != "a-really-long-project-name-t-2" {
		t.Errorf("got %q", got)
	}
	if err := Validate("project", got, MaxProjectSlug); err != nil {
		t.Error(err)
	}
}

package cookieimport

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func parseFixture(t *testing.T, name string) ([]Cookie, string) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	cookies, format, err := Parse(string(data), now)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return cookies, format
}

// cookies.txt as curl writes it: a domain cookie, curl's #HttpOnly_ prefix, a
// session cookie (expiry 0), an empty value whose trailing tab survived, and
// an expired cookie, dropped.
func TestParseNetscape(t *testing.T) {
	cookies, format := parseFixture(t, "cookies.txt")
	if format != FormatNetscape {
		t.Errorf("format = %q", format)
	}
	want := []Cookie{
		{Domain: ".github.com", Name: "_octo", Value: "GH1.1.123", Path: "/", Expires: 4102444800, Secure: true},
		{Domain: "github.com", Name: "user_session", Value: "sess-abc", Path: "/", Expires: 4102444800, Secure: true, HTTPOnly: true},
		{Domain: "gist.github.com", Name: "_gist_session", Value: "gist-1", Path: "/", Secure: true},
		{Domain: ".notgithub.com", Name: "id", Value: "x", Path: "/", Expires: 4102444800},
		{Domain: "localhost", Name: "dev", Value: "", Path: "/", Expires: 4102444800},
	}
	if !reflect.DeepEqual(cookies, want) {
		t.Errorf("cookies =\n%+v\nwant\n%+v", cookies, want)
	}
}

// A cookie extension's export: hostOnly decides the leading dot, a session
// cookie has no expiry, sameSite's no_restriction is None, and a partitioned
// cookie and an expired one are dropped.
func TestParseExtensionJSON(t *testing.T) {
	cookies, format := parseFixture(t, "extension.json")
	if format != FormatExtension {
		t.Errorf("format = %q", format)
	}
	want := []Cookie{
		{Domain: ".github.com", Name: "_octo", Value: "GH1.1.123", Path: "/", Expires: 4102444800, Secure: true, SameSite: "Lax"},
		{Domain: "github.com", Name: "user_session", Value: "sess-abc", Path: "/", Secure: true, HTTPOnly: true, SameSite: "Strict"},
		{Domain: "www.google.com", Name: "NID", Value: "n", Path: "/", Expires: 4102444800, Secure: true, SameSite: "None"},
	}
	if !reflect.DeepEqual(cookies, want) {
		t.Errorf("cookies =\n%+v\nwant\n%+v", cookies, want)
	}
}

// Playwright's storageState: expires -1 is a session cookie.
func TestParsePlaywright(t *testing.T) {
	cookies, format := parseFixture(t, "playwright.json")
	if format != FormatPlaywright {
		t.Errorf("format = %q", format)
	}
	want := []Cookie{
		{Domain: ".github.com", Name: "_octo", Value: "GH1.1.123", Path: "/", Expires: 4102444800, Secure: true, SameSite: "Lax"},
		{Domain: "github.com", Name: "user_session", Value: "sess-abc", Path: "/", Secure: true, HTTPOnly: true, SameSite: "None"},
	}
	if !reflect.DeepEqual(cookies, want) {
		t.Errorf("cookies =\n%+v\nwant\n%+v", cookies, want)
	}
}

// What can't be an export says why, and never quotes a value.
func TestParseRefuses(t *testing.T) {
	for name, data := range map[string]string{
		"empty":           "  \n",
		"not cookies.txt": "hello world\n",
		"bad expiry":      "github.com\tFALSE\t/\tTRUE\tsoon\tname\tsecret-value\n",
		"broken JSON":     `[{"domain": "a.com", "name": "x", "value": "secret-value"`,
		"JSON of no list": `{"origins": []}`,
		"unnamed cookie":  `[{"domain": "a.com", "value": "secret-value"}]`,
		"no domain":       `[{"name": "x", "value": "secret-value"}]`,
		"only expired":    ".a.com\tTRUE\t/\tFALSE\t1000\tx\tsecret-value\n",
		"relative path":   `[{"domain": "a.com", "name": "x", "value": "secret-value", "path": "app"}]`,
		"list of numbers": `[1, 2]`,
		"too big":         strings.Repeat("#", MaxExportBytes+1),
	} {
		_, _, err := Parse(data, now)
		if err == nil {
			t.Errorf("%s: parsed", name)
			continue
		}
		if strings.Contains(err.Error(), "secret-value") {
			t.Errorf("%s: the error quotes a value: %v", name, err)
		}
	}
}

func TestDomainsGroupsBySite(t *testing.T) {
	cookies, _ := parseFixture(t, "cookies.txt")
	got := Domains(cookies)
	want := []DomainCount{{"github.com", 3}, {"localhost", 1}, {"notgithub.com", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Domains = %+v, want %+v", got, want)
	}
	if s := Site("www.example.co.uk"); s != "example.co.uk" {
		t.Errorf("Site(www.example.co.uk) = %q", s)
	}
}

func TestFilter(t *testing.T) {
	cookies, _ := parseFixture(t, "cookies.txt")
	names := func(cs []Cookie) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Domain+" "+c.Name)
		}
		return out
	}
	for _, tc := range []struct {
		domains []string
		want    []string
	}{
		{[]string{"github.com"}, []string{".github.com _octo", "github.com user_session", "gist.github.com _gist_session"}},
		// A subdomain gets its own cookies and its parent's domain
		// cookies, not the parent's host-only ones.
		{[]string{"https://gist.github.com/me"}, []string{".github.com _octo", "gist.github.com _gist_session"}},
		{[]string{"*.notgithub.com", "localhost:3000"}, []string{".notgithub.com id", "localhost dev"}},
		{[]string{"  ", "ithub.com"}, nil},
	} {
		if got := names(Filter(cookies, tc.domains)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Filter(%q) = %q, want %q", tc.domains, got, tc.want)
		}
	}
}

func TestNormalizeDomain(t *testing.T) {
	for in, want := range map[string]string{
		"GitHub.com":                  "github.com",
		"https://app.example.com/x?y": "app.example.com",
		".example.com.":               "example.com",
		"*.example.com":               "example.com",
		"localhost:8080":              "localhost",
		"":                            "",
	} {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

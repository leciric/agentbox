package api

import "testing"

func TestParseHubLink(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://hub.example.com":                      {"https://hub.example.com", ""},
		" https://hub.example.com/ ":                   {"https://hub.example.com", ""},
		"hub.example.com":                              {"https://hub.example.com", ""},
		"http://localhost:8080/hub/":                   {"http://localhost:8080", ""},
		"https://h.example/hub/setup#abx_l_secret":     {"https://h.example", "abx_l_secret"},
		"https://h.example/hub/approve?code=BCDF-GHJK": {"https://h.example", ""},
		"https://h.example/#not-a-secret":              {"https://h.example", ""},
	} {
		hub, secret, err := ParseHubLink(in)
		if err != nil || hub != want[0] || secret != want[1] {
			t.Errorf("ParseHubLink(%q) = %q, %q, %v; want %q, %q", in, hub, secret, err, want[0], want[1])
		}
	}
	for _, in := range []string{"", "ftp://hub.example.com", "https://"} {
		if _, _, err := ParseHubLink(in); err == nil {
			t.Errorf("ParseHubLink(%q) took it", in)
		}
	}
}

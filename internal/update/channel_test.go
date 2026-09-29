package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNightly(t *testing.T) {
	for v, want := range map[string]bool{
		"0.11.0-nightly.20260929.12":  true,
		"v0.11.0-nightly.20260929.12": true,
		"0.11.0":                      false,
		"0.11.0-nightly":              false,
		"0.11.0-nightly.2026929.1":    false,
		"0.11.0-rc.1":                 false,
		"dev":                         false,
	} {
		if got := IsNightly(v); got != want {
			t.Errorf("IsNightly(%q) = %v, want %v", v, got, want)
		}
	}
	if DefaultChannel("0.11.0-nightly.20260929.12") != ChannelNightly || DefaultChannel("0.10.0") != ChannelStable {
		t.Error("DefaultChannel doesn't follow the build")
	}
}

func TestNewerOrdersNightlies(t *testing.T) {
	for _, tc := range []struct{ latest, current string }{
		{"0.11.0-nightly.20260930.1", "0.11.0-nightly.20260929.12"},
		{"0.11.0-nightly.20260929.13", "0.11.0-nightly.20260929.12"},
		{"0.11.0", "0.11.0-nightly.20260929.12"},
		{"0.11.0-nightly.20260929.12", "0.10.0"},
	} {
		if !Newer(tc.latest, tc.current) {
			t.Errorf("Newer(%q, %q) = false", tc.latest, tc.current)
		}
	}
}

func TestOffer(t *testing.T) {
	stable := Latest{Version: "0.10.0", URL: "stable"}
	nightly := Latest{Version: "0.11.0-nightly.20260929.12", URL: "nightly"}
	for _, tc := range []struct {
		name, channel, current string
		stable, nightly        Latest
		want                   string // "" for nothing
	}{
		{"stable, up to date", ChannelStable, "0.10.0", stable, Latest{}, ""},
		{"stable, behind", ChannelStable, "0.9.0", stable, Latest{}, "stable"},
		{"stable never offers a nightly", ChannelStable, "0.10.0", stable, nightly, ""},
		{"back from nightly to a lower stable", ChannelStable, "0.11.0-nightly.20260929.12", stable, Latest{}, "stable"},
		{"back from nightly, stable unknown", ChannelStable, "0.11.0-nightly.20260929.12", Latest{}, Latest{}, ""},
		{"nightly from stable", ChannelNightly, "0.10.0", stable, nightly, "nightly"},
		{"nightly, up to date", ChannelNightly, "0.11.0-nightly.20260929.12", stable, nightly, ""},
		{"nightly, a newer nightly", ChannelNightly, "0.11.0-nightly.20260928.3", stable, nightly, "nightly"},
		{"nightly, the release it led to", ChannelNightly, "0.11.0-nightly.20260929.12", Latest{Version: "0.11.0", URL: "stable"}, nightly, "stable"},
		{"nightly, GitHub unreachable", ChannelNightly, "0.9.0", stable, Latest{}, "stable"},
		{"nightly, landing unreachable", ChannelNightly, "0.10.0", Latest{}, nightly, "nightly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Offer(tc.channel, tc.current, tc.stable, tc.nightly)
			if !ok {
				got.URL = ""
			}
			if got.URL != tc.want || ok != (tc.want != "") {
				t.Errorf("Offer() = %+v, %v, want %q", got, ok, tc.want)
			}
		})
	}
}

func TestLatestReleasePicksTheNewestReleaseOrNightly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.12.0-nightly.20261001.9","html_url":"draft","draft":true},
			{"tag_name":"image-99","html_url":"image"},
			{"tag_name":"v0.11.0-nightly.20260929.12","html_url":"n12"},
			{"tag_name":"v0.11.0-nightly.20260930.2","html_url":"n2"},
			{"tag_name":"v0.10.0","html_url":"stable"},
			{"tag_name":"v0.11.0-rc.1","html_url":"rc"}
		]`))
	}))
	defer srv.Close()
	got, err := LatestRelease(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.11.0-nightly.20260930.2" || got.URL != "n2" {
		t.Errorf("LatestRelease() = %+v", got)
	}
}

package opencode

import "testing"

func TestReleaseCommands(t *testing.T) {
	cases := []struct {
		release    Release
		command    string
		autonomous string
	}{
		{V1, "opencode", "opencode --auto"},
		{V2, "opencode2", "opencode2 --auto"},
	}
	for _, c := range cases {
		if got := c.release.Command(); got != c.command {
			t.Errorf("Release(%d).Command() = %q, want %q", c.release, got, c.command)
		}
		if got := c.release.Autonomous(); got != c.autonomous {
			t.Errorf("Release(%d).Autonomous() = %q, want %q", c.release, got, c.autonomous)
		}
	}
}

func TestReleaseOfStoredSetting(t *testing.T) {
	for raw, want := range map[string]Release{
		"":    V1,
		"1":   V1,
		"2":   V2,
		"two": V1,
		"0":   V1,
	} {
		if got := ReleaseOf(raw); got != want {
			t.Errorf("ReleaseOf(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestReleaseStringRoundTrips(t *testing.T) {
	for _, r := range []Release{V1, V2} {
		if got := ReleaseOf(r.String()); got != r {
			t.Errorf("ReleaseOf(%q) = %d, want %d", r.String(), got, r)
		}
	}
}

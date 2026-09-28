package image

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestOlderVersion(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		older bool
	}{
		{"2026.09.21.1", "2026.09.27.1", true},
		{"2026.09.27.1", "2026.09.27.1", false},
		{"2026.09.27.1", "2026.09.21.1", false},
		// Numbers, not strings: the tenth build of a day comes after the ninth.
		{"2026.09.27.9", "2026.09.27.10", true},
		{"2026.09.27.10", "2026.09.27.9", false},
		{"2026.10.01.1", "2026.09.30.3", false},
		// Unrecorded is older than anything, and not older than itself.
		{"", "2026.09.27.1", true},
		{"2026.09.27.1", "", false},
		{"", "", false},
		{"2026.09.27", "2026.09.27.1", true},
	} {
		if got := OlderVersion(c.a, c.b); got != c.older {
			t.Errorf("OlderVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.older)
		}
	}
}

// A version bump without its line in Changes would leave every base saved
// before it saying it is behind without saying on what.
func TestChangesEndAtVersion(t *testing.T) {
	if last := Changes[len(Changes)-1]; last.Version != Version {
		t.Errorf("Changes ends at %s, but Version is %s: say what %s changed", last.Version, Version, Version)
	}
	for i := 1; i < len(Changes); i++ {
		if !OlderVersion(Changes[i-1].Version, Changes[i].Version) {
			t.Errorf("Changes isn't in order: %s before %s", Changes[i-1].Version, Changes[i].Version)
		}
	}
	for _, c := range Changes {
		if strings.TrimSpace(c.What) == "" {
			t.Errorf("Changes says nothing about %s", c.Version)
		}
	}
}

func TestBehindImage(t *testing.T) {
	current := ToolsFor(Components{})
	specs := specsOf(current)
	image := Installed{Version: Version, ToolsVersion: ToolsVersion(current), Tools: specs}
	older := slices.Clone(specs)
	for i, s := range older {
		if toolName(s) == "claude" {
			older[i] = "claude@0.0.1"
		}
	}
	older = append(older, "npm:retired@1.0")

	t.Run("up to date", func(t *testing.T) {
		if b := BehindImage(image, image); b.Any() {
			t.Errorf("a base from the current image is behind: %+v", b)
		}
	})

	t.Run("an older image", func(t *testing.T) {
		b := BehindImage(Installed{Version: "2026.09.26.1", Tools: specs}, image)
		if !b.Any() || !b.Image || b.From != "2026.09.26.1" {
			t.Fatalf("behind = %+v", b)
		}
		// Only what came after it: Incus was already in 2026.09.26.1.
		if len(b.Changes) != 1 || b.Changes[0].Version != "2026.09.27.1" || !strings.Contains(b.Changes[0].What, "Mesa") {
			t.Errorf("changes = %+v", b.Changes)
		}
		if len(b.Install)+len(b.Remove) > 0 {
			t.Errorf("tools that didn't move: %+v", b)
		}
	})

	t.Run("an image that isn't recorded", func(t *testing.T) {
		b := BehindImage(Installed{}, image)
		if !b.Image || b.From != "" || len(b.Changes) != 0 {
			t.Errorf("behind = %+v", b)
		}
		if !b.ToolsUnknown || !slices.Equal(specsOf(b.Install), specs) {
			t.Errorf("unrecorded tools should all be installed: %+v", b.Install)
		}
	})

	t.Run("tools moved on", func(t *testing.T) {
		b := BehindImage(Installed{Version: Version, Tools: older}, image)
		if b.Image {
			t.Error("the image didn't move")
		}
		if !slices.Equal(specsOf(b.Install), []string{Pin("claude")}) || !slices.Equal(specsOf(b.Remove), []string{"npm:retired@1.0"}) {
			t.Errorf("install %v, remove %v", specsOf(b.Install), specsOf(b.Remove))
		}
		want := []ToolChange{
			{Name: "claude", From: "0.0.1", To: versionOf(Pin("claude"))},
			{Name: "npm:retired", From: "1.0"},
		}
		if got := b.ToolChanges(); !slices.Equal(got, want) {
			t.Errorf("ToolChanges() = %+v, want %+v", got, want)
		}
	})

	t.Run("a component the image has now", func(t *testing.T) {
		withIncus := image
		withIncus.Components = Components{Incus: true, Codex: true}
		b := BehindImage(Installed{Version: Version, Tools: specs}, withIncus)
		if !slices.Equal(b.Components, []string{"Codex", "Incus"}) {
			t.Errorf("components = %v", b.Components)
		}
		if !slices.Contains(specsOf(b.Install), Pin("codex")) {
			t.Errorf("Codex's tools aren't installed: %v", specsOf(b.Install))
		}
		if !b.components.Incus || !b.components.Codex {
			t.Errorf("system.sh wouldn't be asked for them: %+v", b.components)
		}
	})

	t.Run("a component the image dropped", func(t *testing.T) {
		codex := specsOf(ToolsFor(Components{Codex: true}))
		b := BehindImage(Installed{Version: Version, Components: Components{Codex: true}, Tools: codex}, image)
		// The base keeps Codex: the project may use it, and nothing is broken
		// by having it.
		if b.Any() {
			t.Errorf("behind = %+v", b)
		}
		if !slices.Contains(specsOf(b.tools), Pin("codex")) {
			t.Error("a catch-up would stop recording Codex")
		}
	})
}

// What a catch-up records is what a base saved afterwards reads back, and
// that must no longer be behind.
func TestCatchUpRecordsWhatItBroughtTheMachineTo(t *testing.T) {
	image := Installed{Version: Version, Components: Components{Incus: true}, Tools: specsOf(ToolsFor(Components{}))}
	b := BehindImage(Installed{Version: "2026.09.21.1", Tools: []string{"claude@0.0.1"}}, image)
	config := map[string]string{}
	for _, pair := range b.record() {
		k, v, _ := strings.Cut(pair, "=")
		config[k] = v
	}
	after := InstalledFrom(config)
	if after.Version != Version || !after.Components.Incus {
		t.Errorf("recorded %+v", after)
	}
	if again := BehindImage(after, image); again.Any() {
		t.Errorf("still behind after catching up: %+v", again)
	}
}

// system.sh runs again over machines agents have used, so it must be safe to:
// an append that isn't guarded adds its line once per refresh.
func TestSystemScriptIsSafeToRunAgain(t *testing.T) {
	script := string(systemScript)
	for n, line := range strings.Split(script, "\n") {
		if strings.Contains(line, ">>") && !strings.Contains(line, "grep -q") {
			t.Errorf("system.sh line %d appends without checking first: %s", n+1, line)
		}
		for _, once := range []string{"useradd", "userdel", "groupadd -g", "settings.json"} {
			if strings.Contains(line, once) && !strings.HasPrefix(strings.TrimSpace(line), "#") && !strings.Contains(line, "||") {
				t.Errorf("system.sh line %d does what belongs in provision.sh: %s", n+1, line)
			}
		}
	}
	// Incus is left alone where it's there: it may be running.
	if !strings.Contains(script, "dpkg-query -W -f='${Status}' incus") {
		t.Error("system.sh would install Incus over a running one")
	}
	if !strings.Contains(string(provision), `/root/system.sh "$USER_NAME"`) {
		t.Error("provision.sh doesn't run system.sh")
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		for name, s := range map[string][]byte{"system.sh": systemScript, "provision.sh": provision} {
			cmd := exec.Command(bash, "-n")
			cmd.Stdin = strings.NewReader(string(s))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s: %v\n%s", name, err, out)
			}
		}
	}
}

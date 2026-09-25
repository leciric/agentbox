package image_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/image"
	"agentbox/internal/incus"
)

// This machine has no Incus (D43), so the incus binary is a shell script that
// records what it was asked to do and answers the few queries the build makes.
// What the commands themselves do to a real instance is what the base-image
// workflow checks on CI.
type fake struct {
	incus.Client
	log string
}

func fakeIncus(t *testing.T, extra string) *fake {
	t.Helper()
	dir := t.TempDir()
	f := &fake{log: filepath.Join(dir, "commands")}
	// One line per invocation, arguments separated by "|": raw.idmap has a
	// newline in it, which would otherwise split a command over two lines. A
	// test's own cases come first, so they can answer instead of the defaults.
	script := fmt.Sprintf(`#!/bin/sh
{ for a in "$@"; do printf '%%s|' "$a"; done | tr '\n' ' '; printf '\n'; } >>%q
case "$1" in
%s
  list) echo '[{"name":"agentbox-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;
  query) echo '["/1.0/instances/agentbox-base/snapshots/generic","/1.0/instances/agentbox-base/snapshots/ready"]' ;;
esac
exit 0
`, f.log, extra)
	path := filepath.Join(dir, "incus")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.Client = incus.Client{Bin: path}
	return f
}

// commands is every incus invocation so far, in order.
func (f *fake) commands(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if err != nil {
		t.Fatalf("the fake incus ran nothing: %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// at returns the position of the first command containing want, or -1.
func at(commands []string, want string) int {
	for i, c := range commands {
		if strings.Contains(c, want) {
			return i
		}
	}
	return -1
}

func (f *fake) ran(t *testing.T, want string) int {
	t.Helper()
	commands := f.commands(t)
	i := at(commands, want)
	if i < 0 {
		t.Errorf("incus was never run with %q; it ran:\n%s", want, strings.Join(commands, "\n"))
	}
	return i
}

func (f *fake) neverRan(t *testing.T, want string) {
	t.Helper()
	if i := at(f.commands(t), want); i >= 0 {
		t.Errorf("incus shouldn't have been run with %q, but was: %s", want, f.commands(t)[i])
	}
}

var host = image.User{Name: "dev", UID: 1000, GID: 1000}

func TestBuildMakesTheImageHere(t *testing.T) {
	inc := fakeIncus(t, "")
	var log bytes.Buffer

	if err := image.Build(t.Context(), inc.Client, host, image.Options{}, &log); err != nil {
		t.Fatalf("Build() = %v\n%s", err, log.String())
	}

	profile := inc.ran(t, "profile|show|agentbox")
	created := inc.ran(t, "init|"+image.Source+"|agentbox-base-next")
	// The image is made around the placeholder user and only then made this
	// machine's.
	provisioned := inc.ran(t, "/root/provision.sh|agent|1000|1000")
	generic := inc.ran(t, "snapshot|create|agentbox-base-next|generic")
	personalised := inc.ran(t, "/root/personalise.sh|agent|dev|1000|1000")
	ready := inc.ran(t, "snapshot|create|agentbox-base-next|ready")
	if !(profile < created && created < provisioned && provisioned < generic && generic < personalised && personalised < ready) {
		t.Errorf("the build ran out of order:\n%s", strings.Join(inc.commands(t), "\n"))
	}
	inc.neverRan(t, "import|")
	// A new machine has no previous image to swap out.
	inc.ran(t, "rename|agentbox-base-next|agentbox-base")
	inc.neverRan(t, "rename|agentbox-base|agentbox-base-old")
}

// The base image's own ID map is what keeps creating an agent fast: a copy
// that has to be shifted walks every file in the image.
func TestBuildMapsTheHostUserBeforeTheImageEverStarts(t *testing.T) {
	inc := fakeIncus(t, "")
	if err := image.Build(t.Context(), inc.Client, image.User{Name: "sam", UID: 1001, GID: 100}, image.Options{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	commands := inc.commands(t)
	idmap, started := at(commands, "raw.idmap=uid 1001 1001 gid 100 100"), at(commands, "start|agentbox-base-next")
	if idmap < 0 || started < 0 || idmap > started {
		t.Errorf("raw.idmap should be set before the image is first started:\n%s", strings.Join(commands, "\n"))
	}
	inc.ran(t, "/root/personalise.sh|agent|sam|1001|100")
}

// AGENTBOX_DEBIAN_MIRROR reaches provision.sh the way the components do, and a
// build without it sends nothing, so the script keeps deb.debian.org.
func TestBuildLocallyPassesTheDebianMirror(t *testing.T) {
	t.Setenv("AGENTBOX_DEBIAN_MIRROR", "http://debian.example.org/debian/")
	inc := fakeIncus(t, "")
	if err := image.Build(t.Context(), inc.Client, host, image.Options{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	inc.ran(t, "--env|AGENTBOX_DEBIAN_MIRROR=http://debian.example.org/debian|--|/root/provision.sh|")

	t.Setenv("AGENTBOX_DEBIAN_MIRROR", "")
	inc = fakeIncus(t, "")
	if err := image.Build(t.Context(), inc.Client, host, image.Options{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	inc.ran(t, "/root/provision.sh|")
	inc.neverRan(t, "AGENTBOX_DEBIAN_MIRROR")
}

func TestBuildReplacesThePreviousImage(t *testing.T) {
	inc := fakeIncus(t, `  list) echo '[{"name":"agentbox-base"},{"name":"agentbox-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;`)
	if err := image.Build(t.Context(), inc.Client, host, image.Options{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	commands := inc.commands(t)
	renamed := inc.ran(t, "rename|agentbox-base|agentbox-base-old")
	swapped := inc.ran(t, "rename|agentbox-base-next|agentbox-base")
	// The previous image is only deleted once the new one has its name, so a
	// build that fails leaves the machine with a working image either way.
	if renamed > swapped || at(commands[swapped:], "delete|--force|agentbox-base-old") < 0 {
		t.Errorf("the previous image should be renamed away, swapped and only then deleted:\n%s", strings.Join(commands, "\n"))
	}
}

// A build records the tools it put in, so a later AgentBox can move them on
// in place.
func TestBuildRecordsTheTools(t *testing.T) {
	inc := fakeIncus(t, "")
	if err := image.Build(t.Context(), inc.Client, host, image.Options{Components: image.Components{Codex: true}}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	tools := image.ToolsFor(image.Components{Codex: true})
	inc.ran(t, "user.agentbox.tools-version="+image.ToolsVersion(tools)+"|")
	inc.ran(t, "user.agentbox.tools="+image.Pin("go")+" ")
	inc.ran(t, " "+image.Pin("codex")+" ")
}

// updatePlan is the plan for a base image whose Claude Code is older than the
// one tools.txt pins.
func updatePlan(t *testing.T) image.Plan {
	t.Helper()
	var specs []string
	for _, tool := range image.ToolsFor(image.Components{}) {
		spec := tool.Spec
		if tool.Name() == "claude" {
			spec = "claude@0.0.1"
		}
		specs = append(specs, spec)
	}
	plan := image.PlanFor(true, image.Installed{Version: image.Version, ToolsVersion: "0123456789ab", Tools: specs}, image.Components{})
	if plan.Action != image.NeedsTools {
		t.Fatalf("PlanFor = %+v", plan)
	}
	return plan
}

const baseAndNext = `  list) echo '[{"name":"agentbox-base"},{"name":"agentbox-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;`

func TestUpdateToolsInPlace(t *testing.T) {
	inc := fakeIncus(t, baseAndNext)
	var log bytes.Buffer
	if err := image.UpdateTools(t.Context(), inc.Client, host, updatePlan(t), &log); err != nil {
		t.Fatalf("UpdateTools() = %v\n%s", err, log.String())
	}
	copied := inc.ran(t, "copy|agentbox-base/ready|agentbox-base-next|")
	installed := inc.ran(t, "/root/tools.sh|install|dev|/root/tools.list|")
	verified := inc.ran(t, "/root/tools.sh|verify|dev|/root/tools.current|")
	recorded := inc.ran(t, "user.agentbox.tools-version="+image.ToolsVersion(image.ToolsFor(image.Components{}))+"|")
	ready := inc.ran(t, "snapshot|create|agentbox-base-next|ready")
	swapped := inc.ran(t, "rename|agentbox-base-next|agentbox-base")
	if !(copied < installed && installed < verified && verified < recorded && recorded < ready && ready < swapped) {
		t.Errorf("the update ran out of order:\n%s", strings.Join(inc.commands(t), "\n"))
	}
	if !strings.Contains(log.String(), "Installing "+image.Pin("claude")+"\n") {
		t.Errorf("the update should install Claude Code alone:\n%s", log.String())
	}
	// Nothing was removed from tools.txt, and an update is not a build.
	inc.neverRan(t, "|remove|")
	inc.neverRan(t, "init|")
	inc.neverRan(t, "provision.sh")
}

// A tool that doesn't work after the update leaves the base as it was.
func TestUpdateToolsKeepsTheBaseWhenACheckFails(t *testing.T) {
	inc := fakeIncus(t, baseAndNext+"\n  exec) case \"$*\" in *verify*) exit 1 ;; esac ;;")
	err := image.UpdateTools(t.Context(), inc.Client, host, updatePlan(t), &bytes.Buffer{})
	if err == nil {
		t.Fatal("UpdateTools() succeeded with a failing check")
	}
	verified := inc.ran(t, "/root/tools.sh|verify|")
	if at(inc.commands(t)[verified:], "delete|--force|agentbox-base-next") < 0 {
		t.Errorf("the copy should be deleted after the failed check:\n%s", strings.Join(inc.commands(t), "\n"))
	}
	inc.neverRan(t, "rename|")
	inc.neverRan(t, "snapshot|create|")
}

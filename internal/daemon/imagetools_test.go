package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/image"
)

// olderTools is what an image records when its Claude Code is older than the
// one tools.txt pins: the tools an in-place update moves on.
func olderTools() (version, specs string) {
	var out []string
	for _, t := range image.ToolsFor(image.Components{}) {
		if t.Name() == "claude" {
			out = append(out, "claude@0.0.1")
			continue
		}
		out = append(out, t.Spec)
	}
	return "0123456789ab", strings.Join(out, " ")
}

// withNext answers `incus list` with the base and a running copy of it, so
// the copy an update makes comes up with an address.
const withNext = `  list) echo '[{"name":"agentbox-base","status":"Stopped"},{"name":"agentbox-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.5"}]}}}}]' ;;
`

// updatedConfig is the base image once its tools are the current ones.
func updatedConfig() string {
	tools := image.ToolsFor(image.Components{})
	return imageConfig(image.Version, image.Components{}, image.ToolsVersion(tools), toolSpecs(tools))
}

// waitForImage polls Setup's base image check until ok says it's the one.
func waitForImage(t *testing.T, d testDaemon, ok func(api.SetupCheck) bool) api.SetupCheck {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		check := setupCheck(t, d, "image")
		if ok(check) {
			return check
		}
		if time.Now().After(deadline) {
			t.Fatalf("the base image check stayed %+v", check)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func incusLog(t *testing.T, root string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "incus.log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func ranLine(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// A base image whose tools are behind is updated in place by the daemon on
// its own, with Setup saying so rather than asking for a rebuild, and the
// base in use until the updated copy replaces it.
func TestDaemonUpdatesTheBaseImageToolsInPlace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	toolsVersion, tools := olderTools()
	// Every exec in the copy waits for the test's go-ahead, so it can look
	// at Setup while the update runs.
	extra := withNext + `  exec) while [ ! -f "$INCUS_LOG.go" ]; do sleep 0.02; done ;;
  config) [ "$2" = set ] && touch "$INCUS_LOG.updated" ;;
`
	d := startTestDaemon(t, root, toolsIncus(image.Version, image.Components{}, toolsVersion, tools, extra, updatedConfig()))

	running := waitForImage(t, d, func(c api.SetupCheck) bool { return c.Job != "" })
	if running.Status != api.SetupUpdating || !strings.Contains(running.Detail, "Updating agent tools") || !strings.Contains(running.Detail, image.Pin("claude")) {
		t.Errorf("while the tools update: %+v", running)
	}
	// Only one job makes the next image at a time: a build you start waits
	// its turn (buildImage claims the image the same way).
	if d.srv.claimImage() {
		t.Error("the image could be claimed for a build during the update")
	}
	// The status is still a usable image to the setup wizard, unless another
	// required check holds it back on this machine.
	status, err := d.client.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	othersOK := true
	for _, c := range status.Checks {
		if c.Required && c.ID != "image" && c.Status != api.SetupOK {
			othersOK = false
		}
	}
	if othersOK && !status.Ready {
		t.Error("Setup isn't ready while the tools update, though the image is usable")
	}

	if err := os.WriteFile(filepath.Join(root, "incus.log.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	done := waitForImage(t, d, func(c api.SetupCheck) bool { return c.Status == api.SetupOK })
	if !strings.Contains(done.Detail, image.ToolsVersion(image.ToolsFor(image.Components{}))) {
		t.Errorf("after the update: %+v", done)
	}
	log := incusLog(t, root)
	for _, want := range []string{"copy agentbox-base/ready agentbox-base-next", "snapshot create agentbox-base-next ready", "rename agentbox-base-next agentbox-base"} {
		if !ranLine(log, want) {
			t.Errorf("the update never ran incus %s:\n%s", want, strings.Join(log, "\n"))
		}
	}
	if ranLine(log, "init ") {
		t.Error("the update built the image from scratch")
	}
	d.srv.jobs.wait() // Setup can say so a moment before the job does
	job, _, err := d.srv.jobs.lookup(context.Background(), running.Job)
	if err != nil || job.Kind != "image-tools" || job.Status != api.JobSucceeded {
		t.Errorf("the update's job = %+v, %v", job, err)
	}
}

// When updating in place fails, the base is left as it was, and Setup says
// why and offers the rebuild without starting one: that is yours to start.
func TestDaemonLeavesTheBaseWhenTheToolsUpdateFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	toolsVersion, tools := olderTools()
	extra := withNext + `  exec) echo "Error: mise failed" >&2; exit 1 ;;
`
	d := startTestDaemon(t, root, toolsIncus(image.Version, image.Components{}, toolsVersion, tools, extra, ""))

	failed := waitForImage(t, d, func(c api.SetupCheck) bool { return c.Status == api.SetupWarn })
	if !strings.Contains(failed.Detail, "updating its agent tools failed") || !strings.Contains(failed.Detail, "rebuild it") ||
		failed.Fix != "agentbox image build" || failed.Job != "" {
		t.Errorf("after the update failed: %+v", failed)
	}
	d.srv.jobs.wait()
	jobs, err := d.srv.jobs.list(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, j := range jobs {
		kinds[j.Kind] = j.Status
	}
	if kinds["image-tools"] != api.JobFailed || kinds["image-build"] != "" {
		t.Errorf("jobs = %+v, want a failed image-tools and no build", kinds)
	}
	log := incusLog(t, root)
	if ranLine(log, "init ") {
		t.Errorf("the image was built again without asking:\n%s", strings.Join(log, "\n"))
	}
	for _, l := range log {
		if l == "delete --force agentbox-base" || strings.HasPrefix(l, "rename agentbox-base ") {
			t.Errorf("the base image went: incus %s", l)
		}
	}
	// The image still works, so it holds nothing up.
	status, err := d.client.Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	othersOK := true
	for _, c := range status.Checks {
		if c.Required && c.ID != "image" && c.Status != api.SetupOK {
			othersOK = false
		}
	}
	if othersOK && !status.Ready {
		t.Error("Setup isn't ready after a failed update, though the image works")
	}
	// It isn't tried again and again: that waits for the next start, and the
	// rebuild it offers can have the image.
	before := len(incusLog(t, root))
	setupCheck(t, d, "image")
	if after := incusLog(t, root); ranLine(after[before:], "copy ") {
		t.Error("Setup started another update after it failed")
	}
	if !d.srv.claimImage() {
		t.Error("a rebuild can't have the image after the update failed")
	}
}

// Cancelling the update stops it, leaves the base as it was, and doesn't
// start it again on Setup's next look.
func TestDaemonToolsUpdateCancelled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	toolsVersion, tools := olderTools()
	extra := withNext + `  exec) exec sleep 30 ;;
`
	d := startTestDaemon(t, root, toolsIncus(image.Version, image.Components{}, toolsVersion, tools, extra, ""))

	running := waitForImage(t, d, func(c api.SetupCheck) bool { return c.Job != "" })
	j, ok := d.srv.jobs.get(running.Job)
	if !ok {
		t.Fatalf("no job %s", running.Job)
	}
	// Setup shows the job before it has made its copy: cancel once it is
	// working in the copy, so there is a copy for the cancel to delete.
	for deadline := time.Now().Add(10 * time.Second); !ranLine(incusLog(t, root), "exec agentbox-base-next"); {
		if time.Now().After(deadline) {
			t.Fatalf("the update never ran in its copy:\n%s", strings.Join(incusLog(t, root), "\n"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	j.cancel()
	cancelled := waitForImage(t, d, func(c api.SetupCheck) bool { return c.Status == api.SetupWarn })
	if !strings.Contains(cancelled.Detail, "cancelled") || cancelled.Fix != "agentbox image build" {
		t.Errorf("after the cancel: %+v", cancelled)
	}
	d.srv.jobs.wait()
	log := incusLog(t, root)
	if !ranLine(log, "delete --force agentbox-base-next") || ranLine(log, "rename ") {
		t.Errorf("the cancelled update should delete its copy and swap nothing:\n%s", strings.Join(log, "\n"))
	}
	before := len(log)
	setupCheck(t, d, "image")
	if after := incusLog(t, root); ranLine(after[before:], "copy ") {
		t.Error("Setup started the update again after it was cancelled")
	}
}

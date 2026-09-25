//go:build integration

package image_test

import (
	"bytes"
	"context"
	"os/user"
	"strconv"
	"strings"
	"testing"

	"agentbox/internal/image"
	"agentbox/internal/incus"
)

// These tests need Incus, scripts/host-setup.sh and a built base image, and
// replace that image with an updated copy of it:
//
//	agentbox image build && go test -tags integration -run Tools ./internal/image

func incusUser(t *testing.T) (incus.Client, image.User) {
	t.Helper()
	inc := incus.Client{}
	if ok, err := image.Ready(context.Background(), inc); err != nil || !ok {
		t.Skipf("no base image to update (%v): run agentbox image build first", err)
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return inc, image.User{Name: u.Username, UID: uid, GID: gid}
}

// staleClaude is what the image records if its Claude Code were older than
// the pin, so the plan installs the pinned one again: mise has it, so this
// is quick, and the whole update still runs against the real base.
func staleClaude(installed image.Installed) image.Installed {
	installed.ToolsVersion = "0123456789ab"
	installed.Tools = nil
	for _, tool := range image.ToolsFor(installed.Components) {
		spec := tool.Spec
		if tool.Name() == "claude" {
			spec = "claude@0.0.1"
		}
		installed.Tools = append(installed.Tools, spec)
	}
	return installed
}

func TestUpdateToolsOnIncus(t *testing.T) {
	ctx := context.Background()
	inc, u := incusUser(t)
	installed, err := image.InstalledBuild(ctx, inc)
	if err != nil {
		t.Fatal(err)
	}
	plan := image.PlanFor(true, staleClaude(installed), installed.Components)
	if plan.Action != image.NeedsTools {
		t.Fatalf("plan = %+v, want a tools update", plan)
	}
	var log bytes.Buffer
	if err := image.UpdateTools(ctx, inc, u, plan, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	after, err := image.InstalledBuild(ctx, inc)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != installed.Version || after.Components != installed.Components || after.ToolsVersion != image.ToolsVersion(image.ToolsFor(installed.Components)) {
		t.Errorf("after the update the image records %+v", after)
	}
	if got := image.PlanFor(true, after, installed.Components); got.Action != image.UpToDate {
		t.Errorf("after the update the image still needs %v", got.Action)
	}
	if !strings.Contains(log.String(), "Checking every tool") {
		t.Errorf("the update never checked the tools:\n%s", log.String())
	}
	if list, _ := inc.Run(ctx, "list", "--format", "csv", "--columns", "n"); strings.Contains(list, image.Base+"-next") || strings.Contains(list, image.Base+"-old") {
		t.Errorf("the update left instances behind:\n%s", list)
	}
}

// A tool that fails its check leaves the base as it was.
func TestUpdateToolsKeepsTheBaseOnIncus(t *testing.T) {
	ctx := context.Background()
	inc, u := incusUser(t)
	before, err := inc.Config(ctx, image.Base)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := image.InstalledBuild(ctx, inc)
	if err != nil {
		t.Fatal(err)
	}
	plan := image.PlanFor(true, staleClaude(installed), installed.Components)
	plan.Tools = append(plan.Tools, image.Tool{Spec: "broken@1.0", Check: "false"})
	if err := image.UpdateTools(ctx, inc, u, plan, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "verify") {
		t.Fatalf("UpdateTools with a failing check = %v", err)
	}
	after, err := inc.Config(ctx, image.Base)
	if err != nil {
		t.Fatal(err)
	}
	if after["volatile.uuid"] != before["volatile.uuid"] {
		t.Error("the base image was replaced, though the update failed")
	}
	if ok, err := image.Ready(ctx, inc); err != nil || !ok {
		t.Errorf("no ready base image after the failed update: %v", err)
	}
	if list, _ := inc.Run(ctx, "list", "--format", "csv", "--columns", "n"); strings.Contains(list, image.Base+"-next") {
		t.Errorf("the failed update left its copy:\n%s", list)
	}
}

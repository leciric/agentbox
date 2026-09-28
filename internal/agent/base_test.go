package agent_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/agent"
	"agentbox/internal/image"
	"agentbox/internal/state"
)

// A save replaces a whole machine and nothing about it merges, so the one
// thing that makes it undoable is the base it replaced still existing. Saving
// keeps it under another name instead of deleting it, which is a rename rather
// than a copy: no bytes move, and on a copy-on-write pool the two bases share
// their extents.
func TestSaveBaseKeepsTheBaseItReplaces(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-base","status":"Stopped"},{"name":"ab-hello-stack-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.9"}]}}}}]' ;;
  query) echo '{"config":{},"expanded_config":{},"devices":{}}' ;;
esac
exit 0`)
	f := setup(t, inc)
	a := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02"}

	if _, err := f.m.SaveBase(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	const base, previous = "ab-hello-stack-base", "ab-hello-stack-base-previous"
	// The base that was there is renamed out of the way, not deleted...
	ran(t, calls(), "rename "+base+" "+previous)
	ran(t, calls(), "rename "+base+"-next "+base)
	if slices.Contains(calls(), "delete --force "+base) {
		t.Error("the save deleted the base it replaced instead of keeping it")
	}
	// ...and what the save before last kept goes, so a project holds one step
	// back rather than one per save it has ever made.
	ran(t, calls(), "delete --force "+previous)
}

// A project with no base yet has nothing to keep, and must not end up with a
// "previous" that never existed.
func TestSaveBaseWithNoBaseKeepsNothing(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.9"}]}}}}]' ;;
  query) echo '{"config":{},"expanded_config":{},"devices":{}}' ;;
esac
exit 0`)
	f := setup(t, inc)
	a := state.Agent{Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01"}

	if _, err := f.m.SaveBase(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	noCall(t, calls(), "rename ab-hello-stack-base ")
	oneCall(t, calls(), "rename ab-hello-stack-base-next ab-hello-stack-base")
}

// PreviousBase reads the kept base the same way ProjectBase reads the current
// one, and RevertBase swaps them: the base that was in use goes, and the one
// it replaced comes back under the name new agents are copied from.
func TestRevertBase(t *testing.T) {
	inc, calls := loggingIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-base","status":"Stopped"},{"name":"ab-hello-stack-base-previous","status":"Stopped"}]' ;;
  query)
    case "$2" in
      */snapshots) echo "[\"$2/ready\"]" ;;
      */ab-hello-stack-base-previous) echo '{"config":{"user.agentbox.saved-from":"hello-stack/agent-01","user.agentbox.saved-at":"2025-09-12T10:00:00Z"}}' ;;
      *) echo '{"config":{"user.agentbox.saved-from":"hello-stack/agent-02","user.agentbox.saved-at":"2026-09-12T10:00:00Z"}}' ;;
    esac ;;
esac
exit 0`)
	f := setup(t, inc)
	ctx := context.Background()

	was, ok, err := f.m.PreviousBase(ctx, "hello-stack")
	if err != nil || !ok || was.SavedFrom != "hello-stack/agent-01" {
		t.Fatalf("PreviousBase() = %+v, %v, %v", was, ok, err)
	}
	back, err := f.m.RevertBase(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if back.SavedFrom != "hello-stack/agent-01" || back.SnapshotRef() != "ab-hello-stack-base/ready" {
		t.Errorf("RevertBase() = %+v, want the older base under the base's own name", back)
	}
	ran(t, calls(), "delete --force ab-hello-stack-base")
	ran(t, calls(), "rename ab-hello-stack-base-previous ab-hello-stack-base")
}

// Nothing to go back to is a plain refusal, not a half-done revert.
func TestRevertBaseWithoutOne(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  query) echo '[]' ;;
esac
exit 0`))
	if _, err := f.m.RevertBase(context.Background(), "hello-stack"); err == nil ||
		!strings.Contains(err.Error(), "no previous base") {
		t.Errorf("RevertBase() without one = %v, want a refusal naming what is missing", err)
	}
}

// Removing the base gives back everything the project holds, the kept base
// included: a user who goes back to the plain image shouldn't be left paying
// for a machine the app no longer shows.
func TestRemoveBaseRemovesWhatASaveKept(t *testing.T) {
	inc, calls := loggingIncus(t, "exit 0")
	f := setup(t, inc)
	if err := f.m.RemoveBase(context.Background(), "hello-stack"); err != nil {
		t.Fatal(err)
	}
	ran(t, calls(), "delete --force ab-hello-stack-base-previous")
	ran(t, calls(), "delete --force ab-hello-stack-base")
}

// And the kept base can go on its own, which is how the disk comes back
// without losing the base the project is actually on.
func TestRemovePreviousBase(t *testing.T) {
	inc, calls := loggingIncus(t, "exit 0")
	f := setup(t, inc)
	if err := f.m.RemovePreviousBase(context.Background(), "hello-stack"); err != nil {
		t.Fatal(err)
	}
	ran(t, calls(), "delete --force ab-hello-stack-base-previous")
	if slices.Contains(calls(), "delete --force ab-hello-stack-base") {
		t.Error("RemovePreviousBase() deleted the base the project is on")
	}
}

// "base-previous" is a machine name AgentBox owns, so an agent may not take it.
func TestPreviousBaseNameIsReserved(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	if _, err := f.m.Create(context.Background(), "hello-stack", agent.CreateOptions{AI: "none", Name: "base-previous"}); err == nil ||
		!strings.Contains(err.Error(), "reserved") {
		t.Errorf("Create(Name: base-previous) = %v, want a reserved-name error", err)
	}
}

// ran fails unless incus was given exactly this command. oneCall matches a
// prefix, and every base command here is a prefix of the one beside it.
func ran(t *testing.T, calls []string, want string) {
	t.Helper()
	if !slices.Contains(calls, want) {
		t.Errorf("incus was never given %q; it ran:\n  %s", want, strings.Join(calls, "\n  "))
	}
}

// imageConfig is what an instance copied from an image of this version, with
// these tools, carries in its configuration.
func imageConfig(version string, incus bool, tools []string) string {
	with := "0"
	if incus {
		with = "1"
	}
	return fmt.Sprintf(`{"config":{"user.agentbox.image-version":%q,"user.agentbox.with-incus":%q,"user.agentbox.tools":%q,"user.agentbox.saved-from":"organic/agent-03","user.agentbox.saved-at":"2026-09-21T10:00:00Z"}}`,
		version, with, strings.Join(tools, " "))
}

func pinnedTools(claude string) []string {
	var specs []string
	for _, tool := range image.ToolsFor(image.Components{}) {
		spec := tool.Spec
		if claude != "" && tool.Name() == "claude" {
			spec = "claude@" + claude
		}
		specs = append(specs, spec)
	}
	return specs
}

// A base saved before the image moved on — organic's, from 2026.09.21.1,
// before Mesa went in — reads as behind it, on what changed since.
func TestProjectBaseKnowsTheImageItDescendsFrom(t *testing.T) {
	inc, _ := loggingIncus(t, fmt.Sprintf(`case "$1" in
  query)
    case "$2" in
      */snapshots) echo "[\"$2/ready\"]" ;;
      */agentbox-base) echo '%s' ;;
      *) echo '%s' ;;
    esac ;;
esac
exit 0`, imageConfig(image.Version, false, pinnedTools("")), imageConfig("2026.09.21.1", false, pinnedTools("0.0.1"))))
	f := setup(t, inc)

	base, ok, err := f.m.ProjectBase(context.Background(), "organic")
	if err != nil || !ok {
		t.Fatalf("ProjectBase() = %v, %v", ok, err)
	}
	if base.Built.Version != "2026.09.21.1" || len(base.Built.Tools) == 0 {
		t.Errorf("Built = %+v", base.Built)
	}
	behind, err := f.m.BaseBehind(context.Background(), base)
	if err != nil || !behind.Any() {
		t.Fatalf("BaseBehind() = %+v, %v", behind, err)
	}
	said := agent.DescribeBehind(behind)
	for _, want := range []string{"image 2026.09.21.1 → " + image.Version, "Mesa", "claude 0.0.1 → "} {
		if !strings.Contains(said, want) {
			t.Errorf("DescribeBehind() = %q, missing %q", said, want)
		}
	}
}

// The refresh agent's machine is caught up in place, then recorded as
// descending from the current image, which is what a base saved from it
// carries.
func TestCatchUpTheRefreshAgent(t *testing.T) {
	inc, calls := loggingIncus(t, fmt.Sprintf(`case "$1" in
  query)
    case "$2" in
      */agentbox-base) echo '%s' ;;
      *) echo '%s' ;;
    esac ;;
esac
exit 0`, imageConfig(image.Version, true, pinnedTools("")), imageConfig("2026.09.21.1", false, pinnedTools("0.0.1"))))
	f := setup(t, inc)
	a := state.Agent{Project: "organic", Name: "agent-07", Instance: "ab-organic-agent-07"}

	what, err := f.m.CatchUp(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(what, "Incus, which the image has now") || !strings.Contains(what, "claude 0.0.1 → ") {
		t.Errorf("CatchUp() said %q", what)
	}
	oneCall(t, calls(), "exec ab-organic-agent-07 -T --env AGENTBOX_WITH_ANDROID=0")
	oneCall(t, calls(), "exec ab-organic-agent-07 -T -- /root/tools.sh install dev /root/tools.list")
	oneCall(t, calls(), "config set ab-organic-agent-07 user.agentbox.image-version="+image.Version+" ")
}

// An agent made from a base that is up to date is left as it is.
func TestCatchUpLeavesAnUpToDateAgentAlone(t *testing.T) {
	current := imageConfig(image.Version, false, pinnedTools(""))
	inc, calls := loggingIncus(t, fmt.Sprintf(`case "$1" in
  query) echo '%s' ;;
esac
exit 0`, current))
	f := setup(t, inc)
	a := state.Agent{Project: "organic", Name: "agent-07", Instance: "ab-organic-agent-07"}

	what, err := f.m.CatchUp(context.Background(), a)
	if err != nil || what != "" {
		t.Fatalf("CatchUp() = %q, %v", what, err)
	}
	noCall(t, calls(), "exec ")
	noCall(t, calls(), "config set ")
}

// A save keeps what the agent's machine descends from: it is the machine's,
// not the agent's, unlike its limits.
func TestSaveBaseKeepsWhatTheImageRecorded(t *testing.T) {
	inc, calls := loggingIncus(t, fmt.Sprintf(`case "$1" in
  list) echo '[{"name":"ab-organic-base-next","status":"Running","state":{"network":{"eth0":{"addresses":[{"family":"inet","address":"10.0.0.9"}]}}}}]' ;;
  query) echo '%s' ;;
esac
exit 0`, imageConfig(image.Version, false, pinnedTools(""))))
	f := setup(t, inc)
	a := state.Agent{Project: "organic", Name: "agent-07", Instance: "ab-organic-agent-07"}

	base, err := f.m.SaveBase(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if base.Built.Version != image.Version {
		t.Errorf("Built = %+v", base.Built)
	}
	noCall(t, calls(), "config unset ab-organic-base-next user.agentbox.")
}

package daemon

import (
	"context"
	"fmt"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/testutil"
)

// baseIncus stands in for a project that has a base and, beside it, the base
// that save replaced. A revert is two renames and a delete, so what the script
// models is which of the two machines exists after them.
func baseIncus(root string) string {
	return fmt.Sprintf(`state=%q
case "$1" in
  list)
    out='[{"name":"ab-hello-stack-base","status":"Stopped"}'
    [ -f "$state" ] || out="$out,{\"name\":\"ab-hello-stack-base-previous\",\"status\":\"Stopped\"}"
    echo "$out]" ;;
  query)
    case "$2" in
      */ab-hello-stack-base-previous/snapshots)
        if [ -f "$state" ]; then echo "Error: Instance not found" >&2; exit 1; fi
        echo "[\"$2/ready\"]" ;;
      */ab-hello-stack-base/snapshots) echo "[\"$2/ready\"]" ;;
      */ab-hello-stack-base-previous) echo '{"config":{"user.agentbox.saved-from":"hello-stack/agent-01","user.agentbox.saved-at":"2025-09-12T10:00:00Z"},"devices":{}}' ;;
      */ab-hello-stack-base)
        if [ -f "$state" ]; then
          echo '{"config":{"user.agentbox.saved-from":"hello-stack/agent-01","user.agentbox.saved-at":"2025-09-12T10:00:00Z"},"devices":{}}'
        else
          echo '{"config":{"user.agentbox.saved-from":"hello-stack/agent-02","user.agentbox.saved-at":"2026-09-12T10:00:00Z"},"devices":{}}'
        fi ;;
      */agentbox-base/snapshots) echo '["/1.0/instances/agentbox-base/snapshots/ready"]' ;;
      */snapshots) echo '[]' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
  rename) [ "$2" = ab-hello-stack-base-previous ] && touch "$state" ;;
  delete) [ "$3" = ab-hello-stack-base-previous ] && touch "$state" ;;
esac
exit 0
`, root+"/gone")
}

// The app's one question about a base is "can I undo the last save?", so the
// answer has to come back with the base itself rather than from a second route.
func TestBaseAPIReportsAndRevertsTheSaveBefore(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, baseIncus(root))
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	base, ok, err := d.client.Base(ctx, "hello-stack")
	if err != nil || !ok {
		t.Fatalf("Base() = %+v, %v, %v", base, ok, err)
	}
	if base.SavedFrom != "hello-stack/agent-02" {
		t.Errorf("Base().SavedFrom = %q, want the last save", base.SavedFrom)
	}
	if base.Previous == nil || base.Previous.SavedFrom != "hello-stack/agent-01" {
		t.Fatalf("Base().Previous = %+v, want the base that save replaced", base.Previous)
	}

	back, err := d.client.RevertBase(ctx, "hello-stack")
	if err != nil {
		t.Fatal(err)
	}
	if back.SavedFrom != "hello-stack/agent-01" || back.Snapshot != "ab-hello-stack-base/ready" {
		t.Errorf("RevertBase() = %+v, want the older base under the name new agents copy", back)
	}
	// And a project that has reverted has nothing left to revert to: one step
	// back is the whole offer, and the app must not show a button for a second.
	after, ok, err := d.client.Base(ctx, "hello-stack")
	if err != nil || !ok {
		t.Fatalf("Base() after reverting = %+v, %v, %v", after, ok, err)
	}
	if after.Previous != nil {
		t.Errorf("Base().Previous after reverting = %+v, want none", after.Previous)
	}
	if _, err := d.client.RevertBase(ctx, "hello-stack"); !api.IsNotFound(err) {
		t.Errorf("reverting twice: got %v, want 404", err)
	}
}

// Dropping what a save kept is the other half: it gives the disk back without
// touching the base the project is actually on.
func TestRemovePreviousBaseAPI(t *testing.T) {
	root := t.TempDir()
	d := startTestDaemon(t, root, baseIncus(root))
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: testutil.FixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	if err := d.client.RemovePreviousBase(ctx, "hello-stack"); err != nil {
		t.Fatal(err)
	}
	base, ok, err := d.client.Base(ctx, "hello-stack")
	if err != nil || !ok {
		t.Fatalf("Base() = %+v, %v, %v", base, ok, err)
	}
	if base.Previous != nil {
		t.Errorf("Base().Previous = %+v, want none once it has been dropped", base.Previous)
	}
}

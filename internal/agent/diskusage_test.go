package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/state"
)

// TestDiskUsage checks that every category is measured the way it should be:
// a machine and a saved base from Incus' volume state (which works whether or
// not the instance is running, unlike Instances' own State), and the rest —
// worktrees, media, state.db and the daemon log — from the host filesystem.
// A project's previous base doesn't exist here, the way most projects never
// have one, and must not appear rather than error the whole call.
func TestDiskUsage(t *testing.T) {
	inc := fakeIncus(t, `case "$1" in
  query)
    case "$2" in
      */volumes/container/ab-hello-stack-agent-01/state) echo '{"usage":{"used":1000}}' ;;
      */volumes/container/agentbox-base/state) echo '{"usage":{"used":5000}}' ;;
      */volumes/container/ab-hello-stack-base/state) echo '{"usage":{"used":3000}}' ;;
      */volumes/container/ab-hello-stack-base-previous/state) echo "Error: not found" >&2; exit 1 ;;
    esac ;;
esac
exit 0`)
	f := setup(t, inc)
	ctx := context.Background()

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), make([]byte, 250), 0o644); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01",
		Worktree: worktree, Status: state.AgentReady, CreatedAt: time.Now(),
	}
	if err := f.st.AddAgent(ctx, a); err != nil {
		t.Fatal(err)
	}

	mediaDir := filepath.Join(f.m.Paths.Data, "media", "hello-stack", "agent-01")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "shot.png"), make([]byte, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.m.Paths.StateDB(), make([]byte, 111), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.m.Paths.DaemonLog(), make([]byte, 22), 0o644); err != nil {
		t.Fatal(err)
	}

	usage, err := f.m.DiskUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const want = 1000 + 5000 + 3000 + 250 + 400 + 111 + 22
	if usage.Total != want {
		t.Errorf("Total = %d, want %d", usage.Total, want)
	}

	labels := make([]string, len(usage.Categories))
	for i, c := range usage.Categories {
		labels[i] = c.Label
	}
	wantOrder := []string{"Base images and saved bases", "Agent machines", "Media", "Worktrees", "state.db and logs"}
	if len(labels) != len(wantOrder) {
		t.Fatalf("Categories = %v, want %v", labels, wantOrder)
	}
	for i, want := range wantOrder {
		if labels[i] != want {
			t.Errorf("Categories[%d] = %q, want %q (categories must sort largest first)", i, labels[i], want)
		}
	}

	bases := usage.Categories[0]
	if bases.Bytes != 8000 || len(bases.Items) != 2 {
		t.Fatalf("bases = %+v", bases)
	}
	if bases.Items[0].Label != "Base image" || bases.Items[0].Bytes != 5000 {
		t.Errorf("bases.Items[0] = %+v, want the base image first (largest)", bases.Items[0])
	}
	if bases.Items[1].Label != "hello-stack base" || bases.Items[1].Bytes != 3000 {
		t.Errorf("bases.Items[1] = %+v", bases.Items[1])
	}
	for _, it := range bases.Items {
		if it.Label == "hello-stack base (previous)" {
			t.Errorf("a project with no previous base reported one: %+v", it)
		}
	}

	machines := usage.Categories[1]
	if machines.Bytes != 1000 || len(machines.Items) != 1 || machines.Items[0].Label != "hello-stack/agent-01" {
		t.Errorf("machines = %+v", machines)
	}

	media := usage.Categories[2]
	if media.Bytes != 400 || len(media.Items) != 1 || media.Items[0].Label != "hello-stack" {
		t.Errorf("media = %+v", media)
	}

	worktrees := usage.Categories[3]
	if worktrees.Bytes != 250 || len(worktrees.Items) != 1 || worktrees.Items[0].Label != "hello-stack/agent-01" {
		t.Errorf("worktrees = %+v", worktrees)
	}

	stateAndLogs := usage.Categories[4]
	if stateAndLogs.Bytes != 133 || len(stateAndLogs.Items) != 2 {
		t.Fatalf("state and logs = %+v", stateAndLogs)
	}
	if stateAndLogs.Items[0].Label != "state.db" || stateAndLogs.Items[0].Bytes != 111 {
		t.Errorf("state and logs Items[0] = %+v", stateAndLogs.Items[0])
	}
	if stateAndLogs.Items[1].Label != "daemon.log" || stateAndLogs.Items[1].Bytes != 22 {
		t.Errorf("state and logs Items[1] = %+v", stateAndLogs.Items[1])
	}
}

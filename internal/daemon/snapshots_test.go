package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/gateway"
	"agentbox/internal/k8s"
	"agentbox/internal/state"
)

// snapshotServer is the reconciler's test server with one project and one
// cluster agent on it, stopped, so nothing has to wait for a pod.
func snapshotServer(t *testing.T, desired string) (*Server, state.Agent) {
	t.Helper()
	srv := reconcilerServer(t)
	reconcilerProject(t, srv.store)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	if err := srv.store.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "agent-01", Instance: "ab-pawly-agent-01", AI: "claude",
		Branch: "agentbox/agent-01", BaseRef: "main", BaseCommit: "abc", Status: "ready", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.PlaceAgent(ctx, "pawly", "agent-01", "k3d", k8s.WorkspaceDir, desired); err != nil {
		t.Fatal(err)
	}
	a, err := srv.store.Agent(ctx, "pawly", "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	return srv, a
}

// writeTestSnapshot writes the pair a snapshot is: an archive and its metadata.
func writeTestSnapshot(t *testing.T, srv *Server, project, agent, name string, meta snapshotMeta) snapshotMeta {
	t.Helper()
	dir := filepath.Join(srv.cfg.Paths.Snapshots(), project, agent)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if meta.Name == "" {
		meta.Name = name
	}
	if err := os.WriteFile(filepath.Join(dir, name+".tar.gz"), []byte("a tarball"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshotMeta(dir, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

func TestSnapshotsListsTheArchivesWithTheirWorktree(t *testing.T) {
	srv, a := snapshotServer(t, state.DesiredStopped)
	ctx := context.Background()
	ref := gateway.Ref{Project: a.Project, Agent: a.Name}
	writeTestSnapshot(t, srv, a.Project, a.Name, "before", snapshotMeta{
		Head: "abc123", CreatedAt: time.Now().Add(-time.Hour), Image: "agentbox-agent:dev",
	})
	writeTestSnapshot(t, srv, a.Project, a.Name, "after", snapshotMeta{Head: "def456", CreatedAt: time.Now()})

	// A metadata file whose archive is gone is not a snapshot to offer.
	dir := filepath.Join(srv.cfg.Paths.Snapshots(), a.Project, a.Name)
	if err := os.WriteFile(filepath.Join(dir, "half.json"), []byte(`{"name":"half"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := (podLifecycle{s: srv}).Snapshots(ctx, ref)
	if err != nil {
		t.Fatalf("Snapshots() = %v", err)
	}
	if len(list) != 2 || list[0].Name != "before" || list[1].Name != "after" {
		t.Fatalf("Snapshots() = %+v, want before then after", list)
	}
	if list[0].Head != "abc123" || list[1].Head != "def456" {
		t.Errorf("heads = %q, %q, want the metadata's", list[0].Head, list[1].Head)
	}
}

func TestSnapshotGitRoundTripsAndNeedsAnArchive(t *testing.T) {
	srv, a := snapshotServer(t, state.DesiredStopped)
	ctx := context.Background()
	ref := gateway.Ref{Project: a.Project, Agent: a.Name}
	writeTestSnapshot(t, srv, a.Project, a.Name, "before", snapshotMeta{Image: "img"})
	p := podLifecycle{s: srv}

	if err := p.SnapshotGit(ctx, ref, "before", "refs/agentbox/snapshots/agent-01/before", "commit-1", "head-1"); err != nil {
		t.Fatalf("SnapshotGit() = %v", err)
	}
	commit, head, err := p.SnapshotGitInfo(ctx, ref, "before")
	if err != nil || commit != "commit-1" || head != "head-1" {
		t.Fatalf("SnapshotGitInfo() = %q, %q, %v", commit, head, err)
	}
	if err := p.SnapshotGit(ctx, ref, "missing", "ref", "c", "h"); err == nil {
		t.Error("SnapshotGit() on a snapshot with no archive = nil, want an error")
	}
	if _, _, err := p.SnapshotGitInfo(ctx, ref, "missing"); err == nil {
		t.Error("SnapshotGitInfo() on a missing snapshot = nil, want an error")
	}
	// A snapshot taken before the record existed has no worktree to read.
	writeTestSnapshot(t, srv, a.Project, a.Name, "old", snapshotMeta{})
	if _, _, err := p.SnapshotGitInfo(ctx, ref, "old"); err == nil {
		t.Error("SnapshotGitInfo() on an unrecorded snapshot = nil, want an error")
	}
}

func TestDeleteSnapshotRemovesThePairAndToleratesTheMissing(t *testing.T) {
	srv, a := snapshotServer(t, state.DesiredStopped)
	ctx := context.Background()
	ref := gateway.Ref{Project: a.Project, Agent: a.Name}
	writeTestSnapshot(t, srv, a.Project, a.Name, "before", snapshotMeta{})
	p := podLifecycle{s: srv}

	has, err := p.HasSnapshot(ctx, ref, "before")
	if err != nil || !has {
		t.Fatalf("HasSnapshot(before) = %v, %v", has, err)
	}
	if err := p.DeleteSnapshot(ctx, ref, "before"); err != nil {
		t.Fatalf("DeleteSnapshot() = %v", err)
	}
	if has, err = p.HasSnapshot(ctx, ref, "before"); err != nil || has {
		t.Errorf("HasSnapshot(before) after deleting = %v, %v", has, err)
	}
	if err := p.DeleteSnapshot(ctx, ref, "before"); err != nil {
		t.Errorf("DeleteSnapshot() of a snapshot that is gone = %v, want nil", err)
	}
}

func TestSnapshotNamesCannotLeaveTheDirectory(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", `a\b`, "a/b"} {
		if err := checkSnapshotName(name); err == nil {
			t.Errorf("checkSnapshotName(%q) = nil, want an error", name)
		}
	}
	if err := checkSnapshotName("snap-20261027-120000"); err != nil {
		t.Errorf("checkSnapshotName() of a real name = %v", err)
	}
}

func TestNextClaimNameKeepsRoomForAPodName(t *testing.T) {
	long := strings.Repeat("a", 250)
	got := nextClaimName(long)
	if len(got) > 200 {
		t.Errorf("nextClaimName() made %d characters, want at most 200: %q", len(got), got)
	}
	if got == long || !strings.HasPrefix(got, long[:180]) {
		t.Errorf("nextClaimName() = %q, want the old name and a restore suffix", got)
	}
	if nextClaimName("ab-pawly-agent-01-workspace") == "ab-pawly-agent-01-workspace" {
		t.Error("nextClaimName() of a short name = the old name, want a restore suffix")
	}
}

// TestPrepareClaimRefusesASnapshotFromAnotherCluster is the one rule a fork on a
// cluster has: the archive can only be read back into a claim where the pod
// runs.
func TestPrepareClaimRefusesASnapshotFromAnotherCluster(t *testing.T) {
	srv, a := snapshotServer(t, state.DesiredStopped)
	ctx := context.Background()
	// The new agent runs where the daemon has a client; the source it names
	// runs somewhere else, which is what it refuses.
	clusterWithFakeClient(t, srv, reconcilerCluster(), podClient())
	other := state.Cluster{Name: "other", Namespace: "agentbox", Image: "img", CreatedAt: time.Now()}
	clusterWithFakeClient(t, srv, other, podClient())
	if err := srv.store.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "agent-02", Instance: "ab-pawly-agent-02", Branch: "b", BaseRef: "main", BaseCommit: "abc", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.PlaceAgent(ctx, "pawly", "agent-02", "other", k8s.WorkspaceDir, state.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	ref := gateway.Ref{Project: a.Project, Agent: a.Name}
	err := (podLifecycle{s: srv}).PrepareClaim(ctx, ref, "", "pawly/agent-02@before")
	if err == nil || !strings.Contains(err.Error(), "stays on the cluster") {
		t.Fatalf("PrepareClaim() = %v, want it to refuse the other cluster", err)
	}
}

func TestSnapshotTargetRefusesAHostAgent(t *testing.T) {
	srv := reconcilerServer(t)
	reconcilerProject(t, srv.store)
	ctx := context.Background()
	if err := srv.store.AddAgent(ctx, state.Agent{
		Project: "pawly", Name: "host-01", Instance: "pawly-host-01", Branch: "b", BaseRef: "main", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err := (podLifecycle{s: srv}).target(ctx, gateway.Ref{Project: "pawly", Agent: "host-01"})
	if err == nil || !strings.Contains(err.Error(), "Incus snapshots") {
		t.Fatalf("target() = %v, want it to say a host agent has Incus snapshots", err)
	}
}

func TestWaitPodGoneReturnsWhenTheClusterHasNoPod(t *testing.T) {
	srv, a := snapshotServer(t, state.DesiredStopped)
	ctx := context.Background()
	client := podClient()
	clusterWithFakeClient(t, srv, reconcilerCluster(), client)
	if err := waitPodGone(ctx, k8s.FromClient("k3d", "agentbox", client), a); err != nil {
		t.Errorf("waitPodGone() with no pod = %v, want nil", err)
	}
}

func TestSnapshotMetaWithoutAnArchiveIsNotASnapshot(t *testing.T) {
	srv, a := snapshotServer(t, state.DesiredStopped)
	dir := filepath.Join(srv.cfg.Paths.Snapshots(), a.Project, a.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.json"), []byte(`{"name":"gone"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSnapshotMeta(dir, "gone"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("readSnapshotMeta() = %v, want the archive's not-exist", err)
	}
}

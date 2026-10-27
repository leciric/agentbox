package workspace_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"agentbox/internal/gitrepo"
	"agentbox/internal/workspace"
)

// TestAbsentFailsEveryOperation checks the contract of an agent whose tree is
// inside its pod: every method says so with the same error, Path is "", and
// Status doesn't pretend the tree was removed from this host.
func TestAbsentFailsEveryOperation(t *testing.T) {
	ctx := context.Background()
	w := workspace.Absent{}
	repo := gitrepo.Repo{Root: t.TempDir()}

	if got := w.Path(); got != "" {
		t.Errorf("Path() = %q, want an empty path for a tree that isn't on this host", got)
	}

	calls := []struct {
		name string
		call func() error
	}{
		{"Prepare", func() error {
			return w.Prepare(ctx, repo, workspace.PrepareOptions{Branch: "agentbox/agent-01", Base: "HEAD"})
		}},
		{"Diff", func() error {
			_, err := w.Diff(ctx, workspace.DiffOptions{BaseRef: "main"})
			return err
		}},
		{"Files", func() error {
			_, _, err := w.Files(ctx, 100)
			return err
		}},
		{"ReadFile", func() error {
			_, err := w.ReadFile(ctx, "README.md")
			return err
		}},
		{"Checkpoint", func() error {
			_, err := w.Checkpoint(ctx, "refs/agentbox/checkpoints/x", "checkpoint")
			return err
		}},
		{"Snapshot", func() error {
			_, err := w.Snapshot(ctx, "refs/agentbox/snapshots/x", "snapshot")
			return err
		}},
		{"Restore", func() error { return w.Restore(ctx, "deadbeef") }},
		{"Status", func() error {
			st, err := w.Status(ctx)
			if st.Exists {
				t.Errorf("Status() says the tree exists on this host: %+v", st)
			}
			return err
		}},
		{"Destroy", func() error { return w.Destroy(ctx, repo) }},
	}
	for _, c := range calls {
		err := c.call()
		if err == nil {
			t.Errorf("%s() = nil, want the pod workspace's error", c.name)
			continue
		}
		if !errors.Is(err, workspace.ErrAbsent) {
			t.Errorf("%s() = %v, want workspace.ErrAbsent", c.name, err)
		}
	}
	// The error names the pod, so a reader knows where the tree is and what
	// will be able to read it.
	if msg := workspace.ErrAbsent.Error(); !strings.Contains(msg, "pod") || !strings.Contains(msg, "gateway") {
		t.Errorf("ErrAbsent = %q, want it to say the tree is in the pod and that the gateway path isn't built", msg)
	}
}

// TestAbsentLeavesTheHostAlone checks the filesystem half of the promise: a
// method called on a host path that exists still writes nothing, because
// Absent has no path of its own to write to.
func TestAbsentLeavesTheHostAlone(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	w := workspace.Absent{}
	repo := gitrepo.Repo{Root: dir}

	// A path that really is there, so a method that reached for a directory
	// would land in it rather than fail on a missing one.
	kept := dir + "/should-not-be-touched"
	if err := os.WriteFile(kept, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = w.Prepare(ctx, repo, workspace.PrepareOptions{Branch: "b", Base: "HEAD"})
	_ = w.Destroy(ctx, repo)
	_ = w.Restore(ctx, "HEAD")
	_, _ = w.Checkpoint(ctx, "refs/x", "x")
	_, _ = w.Snapshot(ctx, "refs/x", "x")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "should-not-be-touched" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("the directory holds %v, want only the file that was already there", names)
	}
}

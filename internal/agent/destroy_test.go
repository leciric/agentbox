package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// destroyFixture adds an agent-01 record with a real worktree, ready for
// Destroy to be pointed at in whatever state a test wants.
func destroyFixture(t *testing.T, f fixture) state.Agent {
	t.Helper()
	worktree := f.m.Paths.Worktree("hello-stack", "agent-01")
	if err := f.repo.AddWorktree(worktree, "agentbox/agent-01", "HEAD"); err != nil {
		t.Fatal(err)
	}
	a := state.Agent{
		Project:   "hello-stack",
		Name:      "agent-01",
		Instance:  "ab-hello-stack-agent-01",
		Branch:    "agentbox/agent-01",
		Worktree:  worktree,
		CreatedAt: time.Now(),
		Status:    state.AgentReady,
	}
	if err := f.st.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// addMediaFixture gives a an item with a real file on disk, the way Screenshot
// or AddNote would have left it.
func addMediaFixture(t *testing.T, f fixture, a state.Agent) state.Media {
	t.Helper()
	itemDir := filepath.Join(f.m.MediaDir(a.Project, a.Name), "item-1")
	if err := os.MkdirAll(itemDir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(itemDir, "note.txt")
	if err := os.WriteFile(file, []byte("proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	item := state.Media{
		ID: "item-1", Project: a.Project, Agent: a.Name, Kind: "file", Name: "note",
		File: "item-1/note.txt", Source: "agent", Meta: "{}", CreatedAt: time.Now(),
	}
	if err := f.st.AddMedia(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

func assertGone(t *testing.T, f fixture, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Destroy() = %v, want no error", err)
	}
	if agents, _ := f.st.Agents(context.Background(), ""); len(agents) != 0 {
		t.Errorf("agent record survived Destroy(): %+v", agents)
	}
}

// The machine was destroyed outside AgentBox (or by an earlier, otherwise
// successful destroy), but the worktree is untouched.
func TestDestroyMachineGoneWorktreePresent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
  delete) echo "should not be called: the instance is already gone" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	assertGone(t, f, err)
}

// The worktree directory was removed by hand, but the machine is still there.
func TestDestroyWorktreeGoneMachinePresent(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  delete) exit 0 ;;
esac`))
	a := destroyFixture(t, f)
	if err := os.RemoveAll(a.Worktree); err != nil {
		t.Fatal(err)
	}

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	assertGone(t, f, err)
}

// Both the machine and the worktree are gone: the machine outright, and the
// worktree only as far as git remembers it (its directory is still there,
// like when git forgot it because a previous destroy pruned it from inside
// the very machine that got deleted).
func TestDestroyBothGone(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
  delete) echo "should not be called: the instance is already gone" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)
	if err := os.RemoveAll(f.repo.GitDir + "/worktrees/agent-01"); err != nil {
		t.Fatal(err)
	}

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	assertGone(t, f, err)
}

// A retry, or a concurrent destroy, can delete the instance between the
// status check and the delete call. Destroy must not treat that race as a
// reason to leave the record behind.
func TestDestroyMachineVanishesDuringDelete(t *testing.T) {
	marker := t.TempDir() + "/deleted"
	f := setup(t, fakeIncus(t, `case "$1" in
  list)
    if [ -e `+marker+` ]; then echo '[]'; else echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]'; fi ;;
  delete) touch `+marker+`; echo "Error: not found" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	assertGone(t, f, err)
}

// A delete failure that isn't a disappearing act is a real failure: the
// record and worktree must survive so the user can see it and retry.
func TestDestroyGenuineFailureKeepsRecord(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  delete) echo "Error: instance is busy" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	if err == nil {
		t.Fatal("Destroy() = nil, want an error for a genuine failure")
	}
	if agents, _ := f.st.Agents(context.Background(), ""); len(agents) != 1 {
		t.Errorf("agent record should survive a genuine failure, got %+v", agents)
	}
	if _, err := os.Stat(a.Worktree); err != nil {
		t.Errorf("worktree should survive a genuine failure: %v", err)
	}
}

// The default: destroying an agent keeps its media, findable afterwards, with
// its retention clock started rather than left at the item's own created_at.
func TestDestroyKeepsMediaByDefault(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
  delete) echo "should not be called: the instance is already gone" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)
	item := addMediaFixture(t, f, a)

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	assertGone(t, f, err)

	got, err := f.st.MediaItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("MediaItem() after a keeping destroy = %v, want the item to survive", err)
	}
	if got.OrphanedAt.IsZero() {
		t.Error("kept media should have its retention clock started, not left at zero")
	}
	if _, err := os.Stat(f.m.MediaPath(item)); err != nil {
		t.Errorf("kept media's file should survive: %v", err)
	}
}

// DeleteMedia removes the row and the file together, same as before this
// option existed.
func TestDestroyDeletesMediaWhenAsked(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
  delete) echo "should not be called: the instance is already gone" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)
	item := addMediaFixture(t, f, a)

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true, DeleteMedia: true})
	assertGone(t, f, err)

	if _, err := f.st.MediaItem(context.Background(), item.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MediaItem() after a deleting destroy = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(f.m.MediaPath(item)); !os.IsNotExist(err) {
		t.Errorf("deleted media's file should be gone, stat = %v", err)
	}
}

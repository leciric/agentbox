package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/state"
	"agentbox/internal/testutil"
)

// destroyFixture adds an agent-01 record with a real worktree, ready for
// Destroy to be pointed at in whatever state a test wants.
func destroyFixture(t *testing.T, f fixture) state.Agent {
	t.Helper()
	worktree := f.m.Paths.Worktree("hello-stack", "agent-01")
	if err := f.repo.AddWorktree(worktree, "agentbox/agent-01", "HEAD", "test"); err != nil {
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
	// What is kept of it is the archived agent, with its chat.
	if past, _ := f.st.ArchivedAgents(context.Background(), state.ArchivedQuery{}); len(past) != 1 {
		t.Errorf("Destroy() kept %d archived agents, want 1", len(past))
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

// A destroy that wasn't asked to delete the branch still deletes it when
// nothing on it is only here, and keeps it when it has a commit that isn't
// merged or pushed — until that commit reaches a remote at the same place.
func TestDestroyDeletesTheBranchOnlyWhenNothingIsLost(t *testing.T) {
	script := `case "$1" in
  list) echo '[]' ;;
esac`
	for _, tc := range []struct {
		name         string
		commit, push bool
		wantBranch   bool
	}{
		{"no commits", false, false, false},
		{"an unpushed commit", true, false, true},
		{"a pushed commit", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, fakeIncus(t, script))
			a := destroyFixture(t, f)
			if tc.commit {
				if err := os.WriteFile(filepath.Join(a.Worktree, "work.txt"), []byte("work"), 0o644); err != nil {
					t.Fatal(err)
				}
				testutil.Git(t, a.Worktree, "add", "work.txt")
				testutil.Git(t, a.Worktree, "commit", "--quiet", "-m", "work")
			}
			if tc.push {
				testutil.Git(t, f.repo.Root, "update-ref", "refs/remotes/origin/"+a.Branch, a.Branch)
			}
			err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
			assertGone(t, f, err)
			if got := f.repo.BranchExists(a.Branch); got != tc.wantBranch {
				t.Errorf("branch exists after destroy = %v, want %v", got, tc.wantBranch)
			}
		})
	}
}

// Media retention set to immediately deletes an agent's media with it, as if
// DeleteMedia had been asked for.
func TestDestroyDeletesMediaWhenRetentionIsImmediately(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
esac`))
	if err := f.st.SetSetting(context.Background(), state.SettingMediaRetention, api.MediaRetentionImmediately); err != nil {
		t.Fatal(err)
	}
	a := destroyFixture(t, f)
	item := addMediaFixture(t, f, a)
	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	assertGone(t, f, err)
	if _, err := f.st.MediaItem(context.Background(), item.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MediaItem() after destroy with immediate retention = %v, want ErrNotFound", err)
	}
}

// A favorite survives its agent whatever the destroy was told about media:
// with retention immediately or DeleteMedia asked for, its row and file stay,
// and its retention clock starts for when it stops being a favorite. The rest
// of the agent's media goes.
func TestDestroyKeepsFavoriteMedia(t *testing.T) {
	for _, tc := range []struct {
		name      string
		immediate bool
		opts      agent.DestroyOptions
	}{
		{"retention immediately", true, agent.DestroyOptions{Force: true}},
		{"DeleteMedia", false, agent.DestroyOptions{Force: true, DeleteMedia: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
esac`))
			if tc.immediate {
				if err := f.st.SetSetting(ctx, state.SettingMediaRetention, api.MediaRetentionImmediately); err != nil {
					t.Fatal(err)
				}
			}
			a := destroyFixture(t, f)
			fav := addMediaFixture(t, f, a)
			if _, err := f.st.SetMediaFavorite(ctx, fav.ID, true, time.Now()); err != nil {
				t.Fatal(err)
			}
			plainDir := filepath.Join(f.m.MediaDir(a.Project, a.Name), "item-2")
			if err := os.MkdirAll(plainDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := f.st.AddMedia(ctx, state.Media{ID: "item-2", Project: a.Project, Agent: a.Name, Kind: "file", Name: "plain", File: "item-2", Source: "agent", Meta: "{}", CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}

			assertGone(t, f, f.m.Destroy(ctx, a, tc.opts))

			got, err := f.st.MediaItem(ctx, fav.ID)
			if err != nil || !got.Favorite || got.OrphanedAt.IsZero() {
				t.Errorf("favorite after destroy = %+v, %v; want kept, orphaned", got, err)
			}
			if _, err := os.Stat(f.m.MediaPath(fav)); err != nil {
				t.Errorf("favorite's file after destroy = %v, want kept", err)
			}
			if _, err := f.st.MediaItem(ctx, "item-2"); !errors.Is(err, state.ErrNotFound) {
				t.Errorf("plain item after destroy = %v, want ErrNotFound", err)
			}
			if _, err := os.Stat(plainDir); !os.IsNotExist(err) {
				t.Errorf("plain item's directory after destroy = %v, want gone", err)
			}
		})
	}
}

// listed is how List shows agent-01, "" when it leaves it out.
func listed(t *testing.T, f fixture) string {
	t.Helper()
	statuses, err := f.m.List(context.Background(), "hello-stack")
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	for _, st := range statuses {
		if st.Name == "agent-01" {
			return st.State
		}
	}
	return ""
}

// Nothing ever lists an agent being destroyed as "missing": while its
// machine is deleted it is left out, and the moment the machine is gone it is
// a past agent, with its worktree, slow to delete, still to go.
func TestDestroyNeverListsTheAgentAsMissing(t *testing.T) {
	dir := t.TempDir()
	deleting, release, deleted := dir+"/deleting", dir+"/release", dir+"/deleted"
	f := setup(t, fakeIncus(t, `case "$1" in
  list) if [ -e `+deleted+` ]; then echo '[]'; else echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]'; fi ;;
  delete) touch `+deleting+`; while [ ! -e `+release+` ]; do sleep 0.02; done; touch `+deleted+` ;;
esac`))
	a := destroyFixture(t, f)
	if got := listed(t, f); got != "stopped" {
		t.Fatalf("before the destroy, List() shows agent-01 as %q, want stopped", got)
	}

	var archivedWith struct {
		state    string
		past     int
		worktree error
	}
	done := make(chan error, 1)
	go func() {
		done <- f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true, Archived: func() {
			archivedWith.state = listed(t, f)
			past, _ := f.st.ArchivedAgents(context.Background(), state.ArchivedQuery{})
			archivedWith.past = len(past)
			_, archivedWith.worktree = os.Stat(a.Worktree)
		}})
	}()
	for {
		if _, err := os.Stat(deleting); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := listed(t, f); got != "" {
		t.Errorf("while its machine is deleted, List() shows agent-01 as %q, want it left out", got)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	assertGone(t, f, <-done)
	if archivedWith.state != "" || archivedWith.past != 1 {
		t.Errorf("once its machine was gone, List() showed %q and %d past agents, want it left out and 1", archivedWith.state, archivedWith.past)
	}
	if archivedWith.worktree != nil {
		t.Errorf("the agent was archived after its worktree was removed (%v), want before", archivedWith.worktree)
	}
}

// A worktree that can't be removed — files container root left in it, say —
// no longer leaves behind a row whose machine is gone: the agent is a past
// agent, and only the directory is left over.
func TestDestroyArchivesWhenTheWorktreeWontGo(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[]' ;;
esac`))
	a := destroyFixture(t, f)
	locked := filepath.Join(a.Worktree, "root-owned")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root removes whatever the permissions say")
	}

	err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true})
	assertGone(t, f, err)
	if got := listed(t, f); got != "" {
		t.Errorf("List() shows agent-01 as %q, want it gone", got)
	}
}

// A destroy that fails before the machine is gone puts the agent back as it
// was, listed with its machine, rather than hidden as being destroyed.
func TestDestroyFailureListsTheAgentAgain(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Stopped"}]' ;;
  delete) echo "Error: instance is busy" >&2; exit 1 ;;
esac`))
	a := destroyFixture(t, f)
	if err := f.m.Destroy(context.Background(), a, agent.DestroyOptions{Force: true}); err == nil {
		t.Fatal("Destroy() = nil, want the delete's error")
	}
	if got := listed(t, f); got != "stopped" {
		t.Errorf("after a failed destroy, List() shows agent-01 as %q, want stopped", got)
	}
}

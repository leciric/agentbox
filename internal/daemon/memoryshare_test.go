package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentbox/internal/api"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSharingMemoriesCommitsThemOnABranch(t *testing.T) {
	t.Parallel()
	d := startTestDaemon(t, t.TempDir(), oneAgentIncus)
	ctx := context.Background()
	root := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: root}); err != nil {
		t.Fatal(err)
	}
	user := d.client.ProjectMemory("hello-stack")
	head := gitOut(t, root, "rev-parse", "HEAD")
	status := gitOut(t, root, "status", "--porcelain")

	if _, err := user.Share(ctx, api.ShareMemoriesRequest{}); err == nil {
		t.Fatal("shared with nothing named")
	}
	old, err := user.AddMemory(ctx, api.AddMemoryRequest{Kind: "decision", Title: "Old way", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := user.AddMemory(ctx, api.AddMemoryRequest{Kind: "decision", Title: "Deploy on Fridays: never", Content: "It broke twice.", SupersedesID: old.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := user.AddMemory(ctx, api.AddMemoryRequest{Kind: "discovery", Title: "The cache lies"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := user.Share(ctx, api.ShareMemoriesRequest{IDs: []string{cur.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "agentbox/memories" || got.Unchanged || !slices.Equal(got.Files, []string{".agentbox/memories/" + cur.ID + ".md"}) ||
		!strings.HasPrefix(got.Push, "git push -u ") {
		t.Fatalf("share = %+v", got)
	}
	// The checkout is as it was: same commit, same files, same index.
	if now := gitOut(t, root, "rev-parse", "HEAD"); now != head {
		t.Fatalf("HEAD moved from %s to %s", head, now)
	}
	if now := gitOut(t, root, "status", "--porcelain"); now != status {
		t.Fatalf("the checkout changed:\n%s", now)
	}
	if _, err := os.Stat(filepath.Join(root, ".agentbox")); err == nil {
		t.Fatal("files were written into the checkout")
	}
	file := gitOut(t, root, "show", got.Branch+":.agentbox/memories/"+cur.ID+".md")
	if !strings.Contains(file, `title: "Deploy on Fridays: never"`) || !strings.Contains(file, `supersedes: ["`+old.ID+`"]`) {
		t.Fatalf("the file:\n%s", file)
	}
	if subject := gitOut(t, root, "log", "-1", "--format=%s", got.Branch); subject != "docs: share 1 AgentBox memory" {
		t.Fatalf("subject %q", subject)
	}
	gitOut(t, root, "cat-file", "-e", got.Branch+":.agentbox/memories/README.md")

	// Sharing everything goes on from the branch, and again changes nothing.
	all, err := user.Share(ctx, api.ShareMemoriesRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Files) != 2 || gitOut(t, root, "rev-parse", all.Commit+"^") != got.Commit {
		t.Fatalf("share all = %+v", all)
	}
	gitOut(t, root, "cat-file", "-e", got.Branch+":.agentbox/memories/"+other.ID+".md")
	again, err := user.Share(ctx, api.ShareMemoriesRequest{All: true})
	if err != nil || !again.Unchanged || again.Commit != all.Commit {
		t.Fatalf("sharing again = %+v %v", again, err)
	}

	// Once the files are in the checkout, a co-worker's memory among them
	// is read as one from the repository; ours stay ours.
	gitOut(t, root, "checkout", "-q", got.Branch, "--", ".agentbox")
	theirs := "---\nid: mem_coworker\nkind: issue\ntitle: Flaky e2e on CI\n---\n\nRetry once.\n"
	if err := os.WriteFile(filepath.Join(root, ".agentbox/memories/mem_coworker.md"), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := user.Memories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var fromRepo, ours int
	for _, m := range list {
		switch {
		case m.ID == "mem_coworker" && m.Source == api.MemorySourceRepository && m.SourcePath == ".agentbox/memories/mem_coworker.md":
			fromRepo++
		case m.Source == "":
			ours++
		}
	}
	if fromRepo != 1 || ours != 2 {
		t.Fatalf("memories = %+v", list)
	}
	if _, err := user.Share(ctx, api.ShareMemoriesRequest{IDs: []string{"mem_coworker"}}); err == nil {
		t.Fatal("shared a memory that came from the repository")
	}
}

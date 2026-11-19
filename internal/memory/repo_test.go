package memory_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentbox/internal/memory"
)

// repoProject is a project whose checkout is a temporary folder, as the
// projects table has it, so syncRepo finds its files.
func repoProject(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := db.Exec(`INSERT INTO projects (name, root, created_at) VALUES (?, ?, 0)`, name, root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, memory.RepoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeRepoFile(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, memory.RepoDir, name)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	// A later write in the same test must not look like the same file to
	// the fingerprint, which a coarse clock could make it.
	later := time.Now().Add(time.Duration(len(data)) * time.Second)
	_ = os.Chtimes(path, later, later)
}

func TestRepoMemoryRoundTrips(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	m := memory.Memory{ID: "mem_abc", Kind: memory.KindDecision, Title: `Use "sqlite": not postgres`,
		Content: "Because it is one file.\n\n- backups are a copy", Importance: 4, CreatedAt: at, UpdatedAt: at.Add(time.Hour)}
	data := memory.EncodeRepoMemory(m, []string{"mem_old", "mem_older"})
	if !strings.HasPrefix(string(data), "---\nid: mem_abc\nkind: decision\n") {
		t.Fatalf("file starts:\n%s", data)
	}
	got, chain, err := memory.DecodeRepoMemory(data, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Kind != m.Kind || got.Title != m.Title || got.Content != m.Content ||
		got.Importance != 4 || !got.CreatedAt.Equal(at) || !got.UpdatedAt.Equal(m.UpdatedAt) {
		t.Fatalf("read back %+v", got)
	}
	if !slices.Equal(chain, []string{"mem_old", "mem_older"}) {
		t.Fatalf("chain %v", chain)
	}
}

func TestRepoMemoryReadsHandWrittenFiles(t *testing.T) {
	got, chain, err := memory.DecodeRepoMemory([]byte("---\r\ntitle: 'It''s fine'\r\nupdated: 2026-01-02\r\nsupersedes: [a1, 'b2']\r\n---\r\nBody\r\n"), "from_name")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "from_name" || got.Title != "It's fine" || got.Content != "Body" || got.UpdatedAt.Year() != 2026 {
		t.Fatalf("read %+v", got)
	}
	if !slices.Equal(chain, []string{"a1", "b2"}) {
		t.Fatalf("chain %v", chain)
	}
	for _, bad := range []string{"no frontmatter", "---\nid: x\n", "---\nid: ../etc\ntitle: t\n---\n", "---\nimportance: lots\n---\n"} {
		if _, _, err := memory.DecodeRepoMemory([]byte(bad), "ok"); err == nil {
			t.Errorf("read %q", bad)
		}
	}
}

func TestReadsImportSharedMemories(t *testing.T) {
	s, db := openDB(t)
	ctx := context.Background()
	root := repoProject(t, db, "shop")
	writeRepoFile(t, root, "mem_shared.md", "---\nid: mem_shared\nkind: discovery\ntitle: The cache lies\nimportance: 4\n---\n\nRestart it after a deploy.\n")
	writeRepoFile(t, root, "README.md", "# not a memory\n")
	writeRepoFile(t, root, "broken.md", "no frontmatter at all\n")

	list, err := s.Memories(ctx, "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "mem_shared" || list[0].Source != memory.SourceRepository ||
		list[0].SourcePath != ".agentbox/memories/mem_shared.md" || list[0].Importance != 4 {
		t.Fatalf("memories %+v", list)
	}
	// Search and an agent's context find it like any other.
	found, err := s.Search(ctx, "shop", "cache", 5)
	if err != nil || len(found.Memories) != 1 {
		t.Fatalf("search %+v %v", found.Memories, err)
	}
	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "shop", Query: "deploy the cache"})
	if err != nil || !strings.Contains(built.Text, "The cache lies") {
		t.Fatalf("context %q %v", built.Text, err)
	}
	report, err := s.ImportRepo(ctx, "shop", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 1 || !strings.Contains(report.Problems[0], "broken.md") || len(report.Added)+len(report.Updated) != 0 {
		t.Fatalf("second import %+v", report)
	}

	// An edit to the file rewrites the row; removing it removes the row.
	writeRepoFile(t, root, "mem_shared.md", "---\nid: mem_shared\nkind: discovery\ntitle: The cache lies, sometimes\n---\n\nRestart it after a deploy.\n")
	if list, _ = s.Memories(ctx, "shop", nil); len(list) != 1 || list[0].Title != "The cache lies, sometimes" {
		t.Fatalf("after an edit %+v", list)
	}
	if err := s.DeleteMemory(ctx, "shop", "mem_shared"); err == nil || !strings.Contains(err.Error(), "mem_shared.md") {
		t.Fatalf("deleting a shared memory: %v", err)
	}
	if err := os.Remove(filepath.Join(root, memory.RepoDir, "mem_shared.md")); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.Memories(ctx, "shop", nil); len(list) != 0 {
		t.Fatalf("after removing the file %+v", list)
	}
}

func TestAgentBoxsOwnCopyWins(t *testing.T) {
	s, db := openDB(t)
	ctx := context.Background()
	root := repoProject(t, db, "shop")
	own, err := s.AddMemory(ctx, memory.Memory{Project: "shop", Title: "Ours, corrected", Content: "As we know it now."})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.AddMemory(ctx, memory.Memory{Project: "shop", Title: "Ours, newer", SupersedesID: own.ID})
	if err != nil {
		t.Fatal(err)
	}
	// The same id; a file superseding our memory; and a file our memory
	// supersedes: each is a memory the project already has.
	writeRepoFile(t, root, own.ID+".md", "---\nid: "+own.ID+"\ntitle: Theirs, stale\n---\nOld.\n")
	writeRepoFile(t, root, "mem_theirs.md", "---\nid: mem_theirs\ntitle: Theirs, on top of ours\nsupersedes: [\""+newer.ID+"\"]\n---\n")
	writeRepoFile(t, root, "mem_sibling.md", "---\nid: mem_sibling\ntitle: Theirs, beside ours\nsupersedes: [\""+own.ID+"\"]\n---\n")
	writeRepoFile(t, root, "mem_new.md", "---\nid: mem_new\ntitle: Only theirs\n---\n")
	report, err := s.ImportRepo(ctx, "shop", root)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(report.Skipped)
	want := []string{own.ID, "mem_sibling", "mem_theirs"}
	slices.Sort(want)
	if !slices.Equal(report.Skipped, want) || !slices.Equal(report.Added, []string{"mem_new"}) {
		t.Fatalf("import %+v", report)
	}
	got, err := s.Memory(ctx, "shop", own.ID)
	if err != nil || got.Title != "Ours, corrected" || got.Source != "" {
		t.Fatalf("our memory %+v %v", got, err)
	}
}

func TestSharedChainsSupersedeEachOther(t *testing.T) {
	s, db := openDB(t)
	ctx := context.Background()
	root := repoProject(t, db, "shop")
	writeRepoFile(t, root, "mem_v1.md", "---\nid: mem_v1\ntitle: Version one\n---\n")
	writeRepoFile(t, root, "mem_v2.md", "---\nid: mem_v2\ntitle: Version two\nsupersedes: [mem_v1]\n---\n")
	list, err := s.Memories(ctx, "shop", nil)
	if err != nil || len(list) != 1 || list[0].ID != "mem_v2" || list[0].SupersedesID != "mem_v1" {
		t.Fatalf("memories %+v %v", list, err)
	}
}

func TestRepoExportWritesTheProjectsOwn(t *testing.T) {
	s, db := openDB(t)
	ctx := context.Background()
	root := repoProject(t, db, "shop")
	writeRepoFile(t, root, "mem_theirs.md", "---\nid: mem_theirs\ntitle: Theirs\n---\n")
	old, err := s.AddMemory(ctx, memory.Memory{Project: "shop", Title: "Old way"})
	if err != nil {
		t.Fatal(err)
	}
	cur, err := s.AddMemory(ctx, memory.Memory{Project: "shop", Kind: memory.KindDecision, Title: "New way", Content: "Do it so.", SupersedesID: old.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMemory(ctx, memory.Memory{Project: memory.Global, Title: "Mine everywhere"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Memories(ctx, "shop", nil); err != nil { // imports mem_theirs
		t.Fatal(err)
	}
	files, remove, err := s.RepoExport(ctx, "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	files0Data := files[0].Data
	if len(files) != 1 || files[0].Path != ".agentbox/memories/"+cur.ID+".md" ||
		!strings.Contains(string(files[0].Data), "supersedes: [\""+old.ID+"\"]") {
		t.Fatalf("files %+v", files)
	}
	if !slices.Equal(remove, []string{".agentbox/memories/" + old.ID + ".md"}) {
		t.Fatalf("remove %v", remove)
	}
	// Named, a shared memory is skipped and an unknown one is an error.
	if files, _, err = s.RepoExport(ctx, "shop", []string{"mem_theirs"}); err != nil || len(files) != 0 {
		t.Fatalf("exporting a shared one: %+v %v", files, err)
	}
	if _, _, err = s.RepoExport(ctx, "shop", []string{"mem_nope"}); err == nil {
		t.Fatal("exported a memory that doesn't exist")
	}
	// What it wrote reads back as the same memory.
	back, chain, err := memory.DecodeRepoMemory(files0Data, "x")
	if err != nil || back.ID != cur.ID || back.Title != "New way" || back.Kind != memory.KindDecision || !slices.Equal(chain, []string{old.ID}) {
		t.Fatalf("read back %+v %v %v", back, chain, err)
	}
}

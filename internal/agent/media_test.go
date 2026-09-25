package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/state"
)

// logsFixture is an agent-01 with no real worktree content, for the media
// methods that only need a running instance and a project.
func logsFixture(t *testing.T, f fixture, ai string) state.Agent {
	t.Helper()
	a := state.Agent{
		Project: "hello-stack", Name: "agent-01", Instance: "ab-hello-stack-agent-01",
		AI: ai, Worktree: t.TempDir(), CreatedAt: time.Now(), Status: state.AgentReady,
	}
	if err := f.st.AddAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestAddMediaCopiesAFileOutOfTheAgent checks the whole path: the agent's own
// report of what's at the path decides the item's kind and size, and the
// file pulled out of the agent ends up at MediaPath with that content.
func TestAddMediaCopiesAFileOutOfTheAgent(t *testing.T) {
	src := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(src, []byte("results"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SRC", src)
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) echo file; echo 7 ;;
  file) [ "$2" = "pull" ] && cp "$SRC" "$4" ;;
esac`))
	a := logsFixture(t, f, "none")

	item, err := f.m.AddMedia(context.Background(), a, agent.AddMediaOptions{Path: "report.txt", Name: "Build report"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != agent.MediaLog || item.Name != "Build report" || item.Size != 7 {
		t.Errorf("AddMedia() item = %+v", item)
	}
	content, err := os.ReadFile(f.m.MediaPath(item))
	if err != nil || string(content) != "results" {
		t.Errorf("AddMedia() file = %q, %v", content, err)
	}
}

func TestAddMediaRefusesWhenTheAgentIsntRunning(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in list) echo '[]' ;; esac`))
	a := logsFixture(t, f, "none")
	if _, err := f.m.AddMedia(context.Background(), a, agent.AddMediaOptions{Path: "x"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("AddMedia() on a missing instance = %v", err)
	}
}

func TestAddMediaRefusesAnUnknownOrANoteKind(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;; esac`))
	a := logsFixture(t, f, "none")
	for _, kind := range []string{"note", "sculpture"} {
		if _, err := f.m.AddMedia(context.Background(), a, agent.AddMediaOptions{Path: "x", Kind: kind}); err == nil || !strings.Contains(err.Error(), "unknown kind") {
			t.Errorf("AddMedia(Kind: %q) = %v, want it refused", kind, err)
		}
	}
}

func TestAddMediaRefusesABareDirectoryWithNoIndex(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) echo dir; echo 4096 ;;
esac`))
	a := logsFixture(t, f, "none")
	if _, err := f.m.AddMedia(context.Background(), a, agent.AddMediaOptions{Path: "coverage"}); err == nil || !strings.Contains(err.Error(), "zip it") {
		t.Errorf("AddMedia() of a bare directory = %v", err)
	}
}

func TestAddMediaRefusesAFileOverTheSizeLimit(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) echo file; echo 2000000000 ;;
esac`))
	a := logsFixture(t, f, "none")
	if _, err := f.m.AddMedia(context.Background(), a, agent.AddMediaOptions{Path: "huge.bin"}); err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Errorf("AddMedia() of an oversized file = %v", err)
	}
}

// TestAddNoteKeepsAShortSummaryTitledAfterItsFirstLine checks the title
// AddNote makes when none is given, and the two ways a note is refused.
func TestAddNoteKeepsAShortSummaryTitledAfterItsFirstLine(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := logsFixture(t, f, "none")
	ctx := context.Background()

	item, err := f.m.AddNote(ctx, a, "All green\nEverything passed.", "", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "All green" || item.Text != "All green\nEverything passed." || item.Kind != agent.MediaNote {
		t.Errorf("AddNote() = %+v", item)
	}
	if f.m.MediaPath(item) != "" {
		t.Errorf("a note shouldn't have a file path: %q", f.m.MediaPath(item))
	}

	named, err := f.m.AddNote(ctx, a, "line one\nline two", "My title", "user")
	if err != nil || named.Name != "My title" {
		t.Errorf("AddNote() with a name = %+v, %v", named, err)
	}

	long := strings.Repeat("x", 70) + "\nrest"
	truncated, err := f.m.AddNote(ctx, a, long, "", "agent")
	if err != nil || !strings.HasSuffix(truncated.Name, "…") || len([]rune(truncated.Name)) != 61 {
		t.Errorf("AddNote() with a long first line = %+v, %v, want it truncated to 60 runes and an ellipsis", truncated, err)
	}

	if _, err := f.m.AddNote(ctx, a, "   ", "", "agent"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("AddNote() with only whitespace should be refused")
	}
	if _, err := f.m.AddNote(ctx, a, strings.Repeat("a", 20001), "", "agent"); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Errorf("AddNote() with an overlong note should be refused")
	}
}

// TestAddLogsChoosesASourceOrRefuses checks the "nothing chosen" refusal,
// the terminal path's default window and label, and the Android branch's
// own validation, which never reaches the agent at all.
func TestAddLogsChoosesASourceOrRefuses(t *testing.T) {
	f := setup(t, fakeIncus(t, `case "$1" in
  list) echo '[{"name":"ab-hello-stack-agent-01","status":"Running"}]' ;;
  exec) echo "line one"; echo "line two" ;;
esac`))
	a := logsFixture(t, f, "none")
	ctx := context.Background()

	if _, err := f.m.AddLogs(ctx, a, agent.LogsOptions{}); err == nil || !strings.Contains(err.Error(), "choose a Compose service") {
		t.Errorf("AddLogs() with nothing chosen = %v", err)
	}

	item, err := f.m.AddLogs(ctx, a, agent.LogsOptions{Terminal: true})
	if err != nil {
		t.Fatal(err)
	}
	if item.Kind != agent.MediaLog || item.Name != "shell terminal" {
		t.Errorf("AddLogs(Terminal) = %+v", item)
	}
	content, err := os.ReadFile(f.m.MediaPath(item))
	if err != nil || string(content) != "line one\nline two\n" {
		t.Errorf("AddLogs() content = %q, %v", content, err)
	}

	if _, err := f.m.AddLogs(ctx, a, agent.LogsOptions{Android: true, Since: "not-a-duration"}); err == nil || !strings.Contains(err.Error(), "invalid time") {
		t.Errorf("AddLogs(Android) with a bad Since = %v", err)
	}
}

func TestDeleteMediaRemovesTheRowAndItsFile(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := destroyFixture(t, f)
	item := addMediaFixture(t, f, a)

	if err := f.m.DeleteMedia(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.MediaItem(context.Background(), item.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("DeleteMedia() left the row behind: %v", err)
	}
	if _, err := os.Stat(f.m.MediaPath(item)); !os.IsNotExist(err) {
		t.Errorf("DeleteMedia() left the file behind: %v", err)
	}
}

// TestExportMediaWritesAReadmeAndCopiesEachItem checks that a note ends up in
// the README as text, that a file item is copied out beside it, and that an
// agent with nothing to export is refused.
func TestExportMediaWritesAReadmeAndCopiesEachItem(t *testing.T) {
	f := setup(t, fakeIncus(t, "exit 0"))
	a := destroyFixture(t, f)
	addMediaFixture(t, f, a)
	if _, err := f.m.AddNote(context.Background(), a, "All good", "", "agent"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	out, n, err := f.m.ExportMedia(context.Background(), a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("ExportMedia() exported %d items, want 2", n)
	}
	readme, err := os.ReadFile(filepath.Join(out, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "All good") {
		t.Errorf("README doesn't mention the note:\n%s", readme)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 { // README plus the copied file item
		t.Errorf("ExportMedia() left %d entries in %s, want at least 2", len(entries), out)
	}

	empty := state.Agent{Project: "hello-stack", Name: "agent-02", Instance: "ab-hello-stack-agent-02", Worktree: t.TempDir(), Status: state.AgentReady, CreatedAt: time.Now()}
	if err := f.st.AddAgent(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.ExportMedia(context.Background(), empty, dir); err == nil || !strings.Contains(err.Error(), "no media to export") {
		t.Errorf("ExportMedia() of an agent with no media = %v, want it refused", err)
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// Notes on the diff are drafts the daemon keeps, until Request changes sends
// them with what the user wrote: the agent gets each as its file and lines,
// the code and the note, and from then on they are pending until the code
// they quote changes or the user resolves them.
func TestReviewNotesAreSentWithRequestChanges(t *testing.T) {
	t.Parallel()
	w := newWakeTest(t)
	ctx := context.Background()
	a, err := w.srv.store.Agent(ctx, "hello-stack", "agent-02")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(a.Worktree, "save.go")
	if err := os.WriteFile(file, []byte("package app\n\nfunc save() {\n\tdelete(all)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.srv.record(ctx, finishedEvent(a, api.AgentChanges{}, nil, "Added saving.", time.Now()))

	for _, bad := range []api.AddReviewNoteRequest{
		{Path: "save.go", Side: "new", Start: 4, End: 4, Body: "  "},
		{Path: "../etc/passwd", Side: "new", Start: 1, End: 1, Body: "x"},
		{Path: "save.go", Side: "left", Start: 1, End: 1, Body: "x"},
		{Path: "save.go", Side: "new", Start: 5, End: 4, Body: "x"},
	} {
		if _, err := w.client.AddReviewNote(ctx, a.Ref(), bad); err == nil {
			t.Errorf("added %+v", bad)
		}
	}
	onCode, err := w.client.AddReviewNote(ctx, a.Ref(), api.AddReviewNoteRequest{
		Path: "save.go", Side: "new", Start: 3, End: 4, Code: "+func save() {\n+\tdelete(all)", Body: "Ask before deleting everything.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if onCode.Status != api.ReviewNoteDraft || onCode.SentAt != nil {
		t.Errorf("a new note is %+v, want a draft", onCode)
	}
	onRemoved, err := w.client.AddReviewNote(ctx, a.Ref(), api.AddReviewNoteRequest{
		Path: "save.go", Side: "old", Start: 7, End: 7, Code: "-\tbackup()", Body: "Keep the backup",
	})
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := w.client.AddReviewNote(ctx, a.Ref(), api.AddReviewNoteRequest{Path: "save.go", Side: "new", Start: 1, End: 1, Code: "+package app", Body: "Never mind"})
	if err != nil {
		t.Fatal(err)
	}
	body := "Keep the backup, it's what undo reads."
	if n, err := w.client.EditReviewNote(ctx, a.Ref(), onRemoved.ID, api.EditReviewNoteRequest{Body: &body}); err != nil || n.Body != body {
		t.Errorf("edited to %+v (%v)", n, err)
	}
	if err := w.client.DeleteReviewNote(ctx, a.Ref(), dropped.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.client.DeleteReviewNote(ctx, a.Ref(), dropped.ID); err == nil {
		t.Error("deleted a note twice")
	}
	if notes, err := w.client.ReviewNotes(ctx, a.Ref()); err != nil || len(notes) != 2 {
		t.Fatalf("notes = %+v (%v), want the two left", notes, err)
	}

	if _, err := w.client.RequestChanges(ctx, a.Ref(), api.RequestChangesRequest{Notes: []int64{dropped.ID}}); err == nil {
		t.Error("sent a note that was deleted")
	}
	// Notes alone are enough to send.
	if _, err := w.client.RequestChanges(ctx, a.Ref(), api.RequestChangesRequest{Text: "Also add a test.", Notes: []int64{onCode.ID, onRemoved.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Also add a test.",
		"1. save.go:3-4\n\n```diff\n+func save() {\n+\tdelete(all)\n```\n\nAsk before deleting everything.",
		"2. save.go:7 (removed lines, numbered as in the old file)",
		"it's what undo reads.",
	} {
		// The tool gets the prompt as JSON.
		quoted, _ := json.Marshal(want)
		waitFor(t, "the notes to reach the AI tool: "+want, func() bool { return w.tool.got(string(quoted[1 : len(quoted)-1])) })
	}
	r, _, err := w.srv.store.AgentReviewOf(ctx, "hello-stack", "agent-02")
	if err != nil || !strings.Contains(r.Note, "save.go:3-4 — Ask before deleting everything.") {
		t.Errorf("what was asked = %q (%v)", r.Note, err)
	}

	notes, err := w.client.ReviewNotes(ctx, a.Ref())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Status != api.ReviewNotePending || n.SentAt == nil {
			t.Errorf("a sent note is %+v, want pending", n)
		}
	}
	if _, err := w.client.EditReviewNote(ctx, a.Ref(), onCode.ID, api.EditReviewNoteRequest{Body: &body}); err == nil {
		t.Error("rewrote a note the agent already has")
	}
	if _, err := w.client.RequestChanges(ctx, a.Ref(), api.RequestChangesRequest{Notes: []int64{onCode.ID}}); err == nil {
		t.Error("sent a note twice")
	}
	yes, no := true, false
	if n, err := w.client.EditReviewNote(ctx, a.Ref(), onRemoved.ID, api.EditReviewNoteRequest{Resolved: &yes}); err != nil || n.Status != api.ReviewNoteResolved || n.ResolvedAt == nil {
		t.Errorf("resolved, the note is %+v (%v)", n, err)
	}
	if n, err := w.client.EditReviewNote(ctx, a.Ref(), onRemoved.ID, api.EditReviewNoteRequest{Resolved: &no}); err != nil || n.Status != api.ReviewNotePending {
		t.Errorf("taken back, the note is %+v (%v)", n, err)
	}

	// The agent rewrites the lines: both notes are resolved, the first since
	// its code is gone, the second since the file changed.
	if err := os.WriteFile(file, []byte("package app\n\nfunc save() {\n\tif confirm() {\n\t\tdelete(all)\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes, _ = w.client.ReviewNotes(ctx, a.Ref())
	for _, n := range notes {
		if n.Status != api.ReviewNoteResolved {
			t.Errorf("after the change, %s:%d is %s", n.Path, n.Start, n.Status)
		}
	}
}

// A note is resolved by the user too, and back to pending when they take it
// back; one on code moved elsewhere in its file is still pending.
func TestReviewNoteStatus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sent := time.Now()
	for _, c := range []struct {
		name string
		note state.ReviewNote
		want string
	}{
		{"draft", state.ReviewNote{Path: "a.txt", Side: "new", Code: "+zzz"}, api.ReviewNoteDraft},
		{"resolved by the user", state.ReviewNote{Path: "a.txt", Side: "new", Code: "+one", SentAt: sent, ResolvedAt: sent}, api.ReviewNoteResolved},
		{"still there", state.ReviewNote{Path: "a.txt", Side: "new", Code: "+two\n three", SentAt: sent}, api.ReviewNotePending},
		{"only part of a line", state.ReviewNote{Path: "a.txt", Side: "new", Code: "+tw", SentAt: sent}, api.ReviewNoteResolved},
		{"removed lines skipped", state.ReviewNote{Path: "a.txt", Side: "new", Code: "+one\n-gone\n+two", SentAt: sent}, api.ReviewNotePending},
		{"file gone", state.ReviewNote{Path: "b.txt", Side: "new", Code: "+one", SentAt: sent}, api.ReviewNoteResolved},
		{"old side unchanged", state.ReviewNote{Path: "a.txt", Side: "old", Code: "-x", SentAt: sent, SentHash: fileHash(dir, "a.txt")}, api.ReviewNotePending},
		{"old side changed", state.ReviewNote{Path: "a.txt", Side: "old", Code: "-x", SentAt: sent, SentHash: "before"}, api.ReviewNoteResolved},
		{"outside the worktree", state.ReviewNote{Path: "../a.txt", Side: "new", Code: "+one", SentAt: sent}, api.ReviewNoteResolved},
	} {
		if got := noteStatus(c.note, dir); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

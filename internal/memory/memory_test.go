package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// template is every test's starting point: state.Open runs the whole
// migrations list (package memory's tables among them, D72), which this
// package's tests otherwise pay for hundreds of times over. Built once per
// test binary and copied into place, so open() only ever runs the PRAGMA
// user_version check that tells state.Open there is nothing left to migrate.
var template = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "agentbox-memory-template")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "template.db")
	st, err := state.Open(path)
	if err != nil {
		return "", err
	}
	defer st.Close()
	// Folds the WAL back into the main file, so copying that file alone
	// (open, below) carries everything the template has.
	if _, err := st.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return "", err
	}
	return path, nil
})

// open gives each test its own state database, migrated the way the daemon
// migrates the real one, and a memory store on the handle it opened.
func open(t *testing.T) *memory.Store {
	t.Helper()
	tmpl, err := template()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return memory.New(st.DB())
}

func TestEvents(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	start := time.Unix(1700000000, 0)
	for i, e := range []memory.Event{
		{Project: "pawly", Agent: "agent-01", Type: "test_failed", At: start,
			Payload: json.RawMessage(`{"suite":"api","failed":3}`)},
		{Project: "pawly", Agent: "agent-02", Type: "pr_opened", At: start.Add(time.Minute)},
		{Project: "other", Agent: "agent-01", Type: "test_failed", At: start.Add(2 * time.Minute)},
	} {
		got, err := s.AppendEvent(ctx, e)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if got.ID == "" {
			t.Errorf("event %d got no id", i)
		}
	}

	events, err := s.Events(ctx, "pawly", memory.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want the project's 2", len(events))
	}
	if events[0].Type != "pr_opened" {
		t.Errorf("first event is %q, want the newest", events[0].Type)
	}
	if got := string(events[1].Payload); got != `{"suite":"api","failed":3}` {
		t.Errorf("payload = %s", got)
	}
	if !events[1].At.Equal(start) {
		t.Errorf("at = %v, want %v", events[1].At, start)
	}

	one, err := s.Events(ctx, "pawly", memory.EventFilter{Agent: "agent-01"})
	if err != nil || len(one) != 1 {
		t.Fatalf("by agent: %d events, %v", len(one), err)
	}
	byType, err := s.Events(ctx, "pawly", memory.EventFilter{Types: []string{"pr_opened"}})
	if err != nil || len(byType) != 1 {
		t.Fatalf("by type: %d events, %v", len(byType), err)
	}
	since, err := s.Events(ctx, "pawly", memory.EventFilter{Since: start.Add(30 * time.Second)})
	if err != nil || len(since) != 1 {
		t.Fatalf("since: %d events, %v", len(since), err)
	}

	// An event with no time happened now, and one with no payload still reads
	// back as a JSON document rather than as nothing.
	now, err := s.AppendEvent(ctx, memory.Event{Project: "pawly", Type: "agent_created"})
	if err != nil {
		t.Fatal(err)
	}
	if now.At.IsZero() {
		t.Error("an event with no time got none")
	}
	if string(now.Payload) != "{}" {
		t.Errorf("empty payload = %s, want {}", now.Payload)
	}

	if _, err := s.AppendEvent(ctx, memory.Event{Project: "pawly"}); err == nil {
		t.Error("an event with no type was accepted")
	}
	if _, err := s.AppendEvent(ctx, memory.Event{Type: "x"}); err == nil {
		t.Error("an event with no project was accepted")
	}
	if _, err := s.AppendEvent(ctx, memory.Event{Project: "pawly", Type: "x", Payload: json.RawMessage("not json")}); err == nil {
		t.Error("an event with an invalid payload was accepted")
	}
}

func TestMemoriesAndSuperseding(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	old, err := s.AddMemory(ctx, memory.Memory{
		Project: "pawly", Kind: memory.KindProject, Importance: 5,
		Title: "The API runs on port 7777", Content: "internal/daemon/server.go binds :7777.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if old.CreatedAt.IsZero() || !old.UpdatedAt.Equal(old.CreatedAt) {
		t.Errorf("times = %v / %v", old.CreatedAt, old.UpdatedAt)
	}
	if _, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Kind: memory.KindIssue,
		Title: "Android emulator won't boot under load"}); err != nil {
		t.Fatal(err)
	}

	// A memory with no kind is a project fact, and one with no importance is
	// ordinary.
	plain, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "pnpm, not npm"})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Kind != memory.KindProject || plain.Importance != memory.DefaultImportance {
		t.Errorf("defaults: kind %q, importance %d", plain.Kind, plain.Importance)
	}

	live, err := s.Memories(ctx, "pawly", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 3 {
		t.Fatalf("got %d memories, want 3", len(live))
	}
	if live[0].ID != old.ID {
		t.Errorf("first memory is %q, want the most important", live[0].Title)
	}

	issues, err := s.Memories(ctx, "pawly", []string{memory.KindIssue})
	if err != nil || len(issues) != 1 {
		t.Fatalf("by kind: %d memories, %v", len(issues), err)
	}

	// The correction replaces the old fact as it is written.
	fixed, err := s.AddMemory(ctx, memory.Memory{
		Project: "pawly", Kind: memory.KindProject, Importance: 5,
		Title: "The API runs on port 8080", Content: "It moved off 7777 in the port change.",
		SupersedesID: old.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err = s.Memories(ctx, "pawly", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range live {
		if m.ID == old.ID {
			t.Error("a superseded memory came back from Memories")
		}
	}
	if len(live) != 3 {
		t.Fatalf("got %d memories, want 3 (the superseded one replaced)", len(live))
	}

	// It is still readable by id, and says it was superseded, so how the
	// project's understanding changed can be followed.
	was, err := s.Memory(ctx, "pawly", old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !was.Superseded {
		t.Error("the superseded memory doesn't say so")
	}
	if got, err := s.Memory(ctx, "pawly", fixed.ID); err != nil || got.SupersedesID != old.ID {
		t.Errorf("the replacement supersedes %q, want %q (%v)", got.SupersedesID, old.ID, err)
	}

	// Superseding after the fact works the same way.
	later, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Kind: memory.KindDiscovery, Title: "pnpm workspaces need a root install"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SupersedeMemory(ctx, "pawly", plain.ID, later.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Memory(ctx, "pawly", plain.ID); err != nil || !got.Superseded {
		t.Errorf("SupersedeMemory left %q live (%v)", plain.Title, err)
	}

	// What is refused.
	if err := s.SupersedeMemory(ctx, "pawly", later.ID, later.ID); err == nil {
		t.Error("a memory superseded itself")
	}
	if err := s.SupersedeMemory(ctx, "pawly", "mem_nope", later.ID); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("superseding a memory that isn't there: %v, want ErrNotFound", err)
	}
	// A memory of another project isn't reachable from this one.
	if _, err := s.Memory(ctx, "other", old.ID); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("another project read a memory: %v", err)
	}
	if _, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "x", Kind: "rumour"}); err == nil {
		t.Error("an unknown kind was accepted")
	}
	if _, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "  "}); err == nil {
		t.Error("a memory with no title was accepted")
	}
	if _, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: "x", Importance: 9}); err == nil {
		t.Error("importance 9 was accepted")
	}
	if _, err := s.AddMemory(ctx, memory.Memory{Project: "pawly", Title: strings.Repeat("x", memory.MaxTitleLen+1)}); err == nil {
		t.Error("an over-long title was accepted")
	}
}

func TestWorkingMemoryIsAMergePatch(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	// A project that has never written any has none, which is not an error.
	empty, err := s.WorkingMemory(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Goal != "" || !empty.UpdatedAt.IsZero() {
		t.Errorf("a new project's working memory = %+v", empty)
	}

	goal, task := "Ship the reminders page", "Wire the API"
	w, err := s.SetWorkingMemory(ctx, "pawly", memory.WorkingMemoryPatch{
		Goal: &goal, CurrentTask: &task,
		ActiveAgents: &[]string{"agent-01", "agent-02"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.Goal != goal || len(w.ActiveAgents) != 2 || w.UpdatedAt.IsZero() {
		t.Fatalf("working memory = %+v", w)
	}

	// A patch that names one field leaves the others exactly as they were.
	next := "Write the tests"
	w, err = s.SetWorkingMemory(ctx, "pawly", memory.WorkingMemoryPatch{CurrentTask: &next})
	if err != nil {
		t.Fatal(err)
	}
	if w.CurrentTask != next {
		t.Errorf("current task = %q, want %q", w.CurrentTask, next)
	}
	if w.Goal != goal || len(w.ActiveAgents) != 2 {
		t.Errorf("a one-field patch changed the rest: %+v", w)
	}

	// An empty value clears its field; there is no separate clear.
	blank := ""
	w, err = s.SetWorkingMemory(ctx, "pawly", memory.WorkingMemoryPatch{Goal: &blank, ActiveAgents: &[]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if w.Goal != "" || len(w.ActiveAgents) != 0 {
		t.Errorf("clearing left %+v", w)
	}
	if w.CurrentTask != next {
		t.Errorf("clearing the goal cleared the task too: %+v", w)
	}

	// An empty patch is a read.
	same, err := s.SetWorkingMemory(ctx, "pawly", memory.WorkingMemoryPatch{})
	if err != nil {
		t.Fatal(err)
	}
	if !same.UpdatedAt.Equal(w.UpdatedAt) {
		t.Error("an empty patch wrote anyway")
	}

	// It is one document per project.
	if other, err := s.WorkingMemory(ctx, "other"); err != nil || other.CurrentTask != "" {
		t.Errorf("another project's working memory = %+v (%v)", other, err)
	}

	stored, err := s.WorkingMemory(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if stored.CurrentTask != next {
		t.Errorf("read back = %+v", stored)
	}
}

func TestArtifactsAreReferences(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	a, err := s.AddArtifact(ctx, memory.Artifact{
		Project: "pawly", Agent: "agent-01", Type: "file",
		Path: "desktop/src/renderer/Reminders.tsx", Metadata: json.RawMessage(`{"lines":180}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.CreatedAt.IsZero() {
		t.Fatalf("artifact = %+v", a)
	}
	if _, err := s.AddArtifact(ctx, memory.Artifact{Project: "pawly", Type: "pull_request", Path: "https://github.com/leciric/agentbox/pull/12"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddArtifact(ctx, memory.Artifact{Project: "other", Type: "file", Path: "x"}); err != nil {
		t.Fatal(err)
	}

	list, err := s.Artifacts(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d artifacts, want the project's 2", len(list))
	}
	got, err := s.Artifact(ctx, "pawly", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Metadata) != `{"lines":180}` || got.Agent != "agent-01" {
		t.Errorf("artifact = %+v", got)
	}

	if _, err := s.AddArtifact(ctx, memory.Artifact{Project: "pawly", Path: "x"}); err == nil {
		t.Error("an artifact with no type was accepted")
	}
	if _, err := s.AddArtifact(ctx, memory.Artifact{Project: "pawly", Type: "file"}); err == nil {
		t.Error("an artifact with no path was accepted")
	}
	if _, err := s.Artifact(ctx, "pawly", "art_nope"); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("missing artifact: %v, want ErrNotFound", err)
	}
}

func TestReports(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	r, err := s.AddReport(ctx, memory.Report{
		Project: "pawly", Agent: "agent-01", Status: memory.StatusPartial,
		Task:            "Add the reminders page",
		Summary:         "The page renders and saves. Pagination is not done.",
		Discoveries:     []string{"The API returns 500 for an empty cart", ""},
		Decisions:       []string{"Load everything for now; there are never more than 50"},
		RemainingIssues: []string{"Pagination"},
		Artifacts:       []string{"art_abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == "" || r.CreatedAt.IsZero() {
		t.Fatalf("report = %+v", r)
	}
	if len(r.Discoveries) != 1 {
		t.Errorf("discoveries = %v, want the blank one dropped", r.Discoveries)
	}

	if _, err := s.AddReport(ctx, memory.Report{Project: "pawly", Agent: "agent-02", Summary: "Done."}); err != nil {
		t.Fatal(err)
	}
	all, err := s.Reports(ctx, "pawly", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d reports, want 2", len(all))
	}
	if all[0].Status != memory.StatusDone {
		t.Errorf("a report with no status = %q, want done", all[0].Status)
	}
	one, err := s.Reports(ctx, "pawly", "agent-01")
	if err != nil || len(one) != 1 {
		t.Fatalf("by agent: %d reports, %v", len(one), err)
	}
	if one[0].RemainingIssues[0] != "Pagination" || one[0].Decisions == nil {
		t.Errorf("read back = %+v", one[0])
	}

	if _, err := s.AddReport(ctx, memory.Report{Project: "pawly", Agent: "a", Summary: "x", Status: "sort of"}); err == nil {
		t.Error("an unknown status was accepted")
	}
	if _, err := s.AddReport(ctx, memory.Report{Project: "pawly", Agent: "a"}); err == nil {
		t.Error("a report with no summary was accepted")
	}
	if _, err := s.AddReport(ctx, memory.Report{Project: "pawly", Summary: "x"}); err == nil {
		t.Error("a report with no agent was accepted")
	}
}

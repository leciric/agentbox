package memory_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentbox/internal/memory"
)

// add writes a memory and fails the test if it couldn't, so the tests below
// read as the situation they are about rather than as error handling.
func add(t *testing.T, s *memory.Store, m memory.Memory) memory.Memory {
	t.Helper()
	if m.Project == "" {
		m.Project = "pawly"
	}
	out, err := s.AddMemory(context.Background(), m)
	if err != nil {
		t.Fatalf("AddMemory(%q): %v", m.Title, err)
	}
	return out
}

// Two memories of one kind whose titles say close to the same thing are
// flagged, not merged: a title is somebody's one-line summary, and two
// summaries that rhyme are not the same fact. The pair that *is* merged is
// the one with nothing to lose — same title, and the newer says every word of
// the older.
func TestMechanicalPassFlagsNearDuplicatesAndMergesExactOnes(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	old := time.Unix(1700000000, 0)

	// A near-duplicate pair: same kind, titles that share most of their words.
	first := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "Agent briefs are capped at 3000 words",
		Content: "internal/brief", CreatedAt: old})
	second := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "The agent brief is capped at 3000 words",
		Content: "see internal/brief", CreatedAt: old.Add(time.Hour)})
	// Same words, different kind: a fact and a problem are not duplicates of
	// each other however alike they read.
	add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "The agent brief is capped at 3000 words",
		Content: "and the recap overflows it", CreatedAt: old.Add(2 * time.Hour)})
	// Related, and not a duplicate: the second says more, which is exactly
	// what Jaccard is chosen to punish.
	add(t, s, memory.Memory{Kind: memory.KindProject, Title: "The API listens on 7777", CreatedAt: old})
	add(t, s, memory.Memory{Kind: memory.KindProject,
		Title: "The API listens on 7777 in development and on 8080 in CI", CreatedAt: old.Add(time.Hour)})
	// The one safe merge: the same title, and the newer content contains the
	// older word for word.
	stale := add(t, s, memory.Memory{Kind: memory.KindDiscovery, Title: "FTS5 is in the driver",
		Content: "modernc.org/sqlite is built with it.", CreatedAt: old})
	fuller := add(t, s, memory.Memory{Kind: memory.KindDiscovery, Title: "FTS5 is in the driver",
		Content: "modernc.org/sqlite is built with it. There is no LIKE fallback.", CreatedAt: old.Add(time.Hour)})

	pass, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{DecayAfter: -1})
	if err != nil {
		t.Fatal(err)
	}
	if pass.Kind != memory.PassMechanical {
		t.Errorf("pass kind = %q, want %q", pass.Kind, memory.PassMechanical)
	}
	if pass.MemoriesSuperseded != 1 {
		t.Errorf("%d memories superseded, want 1", pass.MemoriesSuperseded)
	}
	if pass.DuplicatesFound != 1 {
		t.Errorf("%d duplicate candidates, want 1 (the two brief memories)", pass.DuplicatesFound)
	}

	// The merged one is gone from listings, still readable by id, and points
	// at what replaced it.
	gone, err := s.Memory(ctx, "pawly", stale.ID)
	if err != nil || !gone.Superseded {
		t.Fatalf("the merged memory = %+v, %v; want it superseded", gone, err)
	}
	if kept, err := s.Memory(ctx, "pawly", fuller.ID); err != nil || kept.SupersedesID != stale.ID {
		t.Errorf("the memory that replaced it supersedes %q, want %q (%v)", kept.SupersedesID, stale.ID, err)
	}

	// The flagged pair is both still live: nothing is deleted on a score.
	duplicates, err := s.Duplicates(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if len(duplicates) != 1 {
		t.Fatalf("Duplicates() = %+v, want one pair", titlesOfPairs(duplicates))
	}
	if duplicates[0].Memory.ID != second.ID || duplicates[0].Of.ID != first.ID {
		t.Errorf("the pair is %q of %q, want the newer brief memory of the older",
			duplicates[0].Memory.Title, duplicates[0].Of.Title)
	}
	if duplicates[0].Similarity < memory.DefaultDuplicateThreshold {
		t.Errorf("similarity = %v, want at least the threshold %v", duplicates[0].Similarity, memory.DefaultDuplicateThreshold)
	}
	live, err := s.Memories(ctx, "pawly", []string{memory.KindProject})
	if err != nil || len(live) != 4 {
		t.Errorf("live project memories = %v, %v; want all four still there", titles(live), err)
	}

	// Running again is not cumulative: the candidate list is rewritten, and
	// the merge has already happened.
	again, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{DecayAfter: -1})
	if err != nil {
		t.Fatal(err)
	}
	if again.MemoriesSuperseded != 0 || again.DuplicatesFound != 1 {
		t.Errorf("a second pass merged %d and flagged %d, want 0 and 1", again.MemoriesSuperseded, again.DuplicatesFound)
	}
}

func titlesOfPairs(pairs []memory.Duplicate) [][2]string {
	out := make([][2]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, [2]string{p.Memory.Title, p.Of.Title})
	}
	return out
}

// Importance ages for the kinds that are about a moment, never for the ones
// that are about the project, and never below the floor. A memory something
// has read recently doesn't age at all, and a pass that runs twice in a row
// doesn't charge twice.
func TestImportanceDecay(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Unix(1700000000, 0)
	ancient := now.Add(-90 * 24 * time.Hour)

	episodic := add(t, s, memory.Memory{Kind: memory.KindEpisodic, Title: "The demo was recorded", Importance: 4, CreatedAt: ancient})
	issue := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "The count query is unindexed", Importance: 2, CreatedAt: ancient})
	floored := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "The favicon is wrong", Importance: 1, CreatedAt: ancient})
	fact := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "The API listens on 7777", Importance: 4, CreatedAt: ancient})
	decision := add(t, s, memory.Memory{Kind: memory.KindDecision, Title: "Paginate at 50", Importance: 4, CreatedAt: ancient})
	recent := add(t, s, memory.Memory{Kind: memory.KindEpisodic, Title: "The seed script was rewritten", Importance: 4, CreatedAt: now.Add(-time.Hour)})
	read := add(t, s, memory.Memory{Kind: memory.KindEpisodic, Title: "The migration ran", Importance: 4, CreatedAt: ancient})
	if err := s.MarkReferenced(ctx, "pawly", read.ID); err != nil {
		t.Fatal(err)
	}

	pass, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if pass.MemoriesDecayed != 2 {
		t.Errorf("%d memories decayed, want 2", pass.MemoriesDecayed)
	}
	for _, want := range []struct {
		what string
		id   string
		want int
	}{
		{"an old episodic memory", episodic.ID, 3},
		{"an old issue", issue.ID, 1},
		{"an issue already at the floor", floored.ID, 1},
		{"a project fact", fact.ID, 4},
		{"a decision", decision.ID, 4},
		{"something that happened this hour", recent.ID, 4},
		{"something the recap read", read.ID, 4},
	} {
		got, err := s.Memory(ctx, "pawly", want.id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Importance != want.want {
			t.Errorf("%s: importance = %d, want %d", want.what, got.Importance, want.want)
		}
	}

	// The same pass an hour later takes nothing more: the decay is stamped.
	soon, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{Now: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if soon.MemoriesDecayed != 0 {
		t.Errorf("a pass an hour later decayed %d, want 0", soon.MemoriesDecayed)
	}
	// A month later it takes another point, down to the floor.
	later, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{Now: now.Add(60 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if later.MemoriesDecayed != 2 {
		t.Errorf("a pass two months later decayed %d, want 2", later.MemoriesDecayed)
	}
	if got, _ := s.Memory(ctx, "pawly", episodic.ID); got.Importance != 2 {
		t.Errorf("the episodic memory is at %d after two decays, want 2", got.Importance)
	}
}

// An issue that stopped being true is closed, not replaced. It leaves
// listings and searches like a superseded memory, stays readable by id, and
// nothing had to invent a memory of a fix that never happened.
func TestResolveMemory(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	issue := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "The flaky snapshot test",
		Content: "It fails about one run in five."})
	other := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "The count query is unindexed"})

	resolved, err := s.ResolveMemory(ctx, "pawly", issue.ID, "the test was deleted in #81")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Resolved() || resolved.ResolvedBy != "the test was deleted in #81" {
		t.Fatalf("ResolveMemory() = %+v", resolved)
	}
	if resolved.Live() {
		t.Error("a resolved memory still counts as live")
	}
	issues, err := s.Memories(ctx, "pawly", []string{memory.KindIssue})
	if err != nil || len(issues) != 1 || issues[0].ID != other.ID {
		t.Errorf("open issues = %v, %v; want only the unindexed query", titles(issues), err)
	}
	results, err := s.Search(ctx, "pawly", "flaky snapshot", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Memories) != 0 {
		t.Errorf("a resolved issue still comes back from search: %v", titles(results.Memories))
	}
	if back, err := s.Memory(ctx, "pawly", issue.ID); err != nil || !back.Resolved() {
		t.Errorf("Memory(%s) = %+v, %v; want it still readable and closed", issue.ID, back, err)
	}
	// Saying it twice changes nothing, and nothing pretends a memory can be
	// its own replacement.
	if again, err := s.ResolveMemory(ctx, "pawly", issue.ID, "again"); err != nil || again.ResolvedBy != "the test was deleted in #81" {
		t.Errorf("resolving twice = %+v, %v", again, err)
	}
}

// The watermark is the newest successful distillation, and nothing else: a
// failed pass is recorded and doesn't move it, and a mechanical pass has
// nothing to do with it.
func TestWatermarkAndPendingEvents(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	start := time.Unix(1700000000, 0)
	var events []memory.Event
	for i := range 5 {
		e, err := s.AppendEvent(ctx, memory.Event{Project: "pawly", Type: "agent_finished", At: start.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	// AgentBox's own bookkeeping is not the project's history: a distillation
	// never reads the event it wrote last time.
	if _, err := s.AppendEvent(ctx, memory.Event{Project: "pawly", Type: memory.EventMemoryConsolidated,
		At: start.Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	watermark, err := s.Watermark(ctx, "pawly")
	if err != nil || !watermark.IsZero() {
		t.Fatalf("Watermark() = %v, %v; want zero before anything was distilled", watermark, err)
	}
	if n, err := s.PendingEvents(ctx, "pawly", watermark); err != nil || n != 5 {
		t.Errorf("PendingEvents() = %d, %v; want the five real events", n, err)
	}

	if _, err := s.RecordPass(ctx, memory.Pass{Project: "pawly", Kind: memory.PassDistill,
		Error: "the chat said nothing", At: start.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Watermark(ctx, "pawly"); err != nil || !got.IsZero() {
		t.Errorf("a failed pass moved the watermark to %v (%v)", got, err)
	}

	// A pass that read the first three leaves two pending.
	if _, err := s.RecordPass(ctx, memory.Pass{Project: "pawly", Kind: memory.PassDistill, EventsRead: 3,
		MemoriesWritten: 2, ThroughEventID: events[2].ID, ThroughAt: events[2].At, At: start.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Watermark(ctx, "pawly")
	if err != nil || !got.Equal(events[2].At) {
		t.Fatalf("Watermark() = %v, %v; want %v", got, err, events[2].At)
	}
	pending, err := s.EventsSince(ctx, "pawly", got, 0)
	if err != nil || len(pending) != 2 || pending[0].ID != events[3].ID {
		t.Fatalf("EventsSince() = %d events, %v; want the last two, oldest first", len(pending), err)
	}
}

// The numbers the app will show a compression ratio from: raw history on one
// side, what the passes made of it on the other.
func TestConsolidationStats(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	start := time.Unix(1700000000, 0)
	for i := range 40 {
		if _, err := s.AppendEvent(ctx, memory.Event{Project: "pawly", Type: "agent_finished",
			At: start.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	kept := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "The API listens on 7777"})
	closed := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "The count query is unindexed"})
	replaced := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "Briefs are capped"})
	add(t, s, memory.Memory{Kind: memory.KindProject, Title: "Briefs are capped at 3000 words", SupersedesID: replaced.ID})
	if _, err := s.ResolveMemory(ctx, "pawly", closed.ID, "fixed in #81"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordPass(ctx, memory.Pass{Project: "pawly", Kind: memory.PassDistill, EventsRead: 40,
		MemoriesWritten: 2, InputBytes: 12000, OutputBytes: 900, ThroughAt: start.Add(39 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.ConsolidationStats(ctx, "pawly")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Events != 40 {
		t.Errorf("Events = %d, want 40", stats.Events)
	}
	if stats.Memories != 2 {
		t.Errorf("Memories = %d, want 2 (%s and its replacement)", stats.Memories, kept.Title)
	}
	if stats.Superseded != 1 || stats.Resolved != 1 {
		t.Errorf("Superseded = %d, Resolved = %d; want 1 and 1", stats.Superseded, stats.Resolved)
	}
	if stats.Passes != 1 || stats.EventsRead != 40 || stats.MemoriesWritten != 2 {
		t.Errorf("passes = %+v", stats)
	}
	if stats.InputBytes != 12000 || stats.OutputBytes != 900 {
		t.Errorf("cost = %d in, %d out; want 12000 and 900", stats.InputBytes, stats.OutputBytes)
	}
	if stats.Pending != 0 {
		t.Errorf("Pending = %d, want 0: the watermark is past the last event", stats.Pending)
	}
	// A project nothing has touched answers zeroes rather than failing on a
	// sum over no rows.
	empty, err := s.ConsolidationStats(ctx, "quiet")
	if err != nil || empty.Events != 0 || empty.Passes != 0 || !empty.LastPass.IsZero() {
		t.Errorf("ConsolidationStats(quiet) = %+v, %v", empty, err)
	}
}

// A context build is a reference: the memories the budget kept don't decay,
// and the ones it weighed and dropped do. That is the whole point of counting
// references rather than dates — a project reading the same twenty memories
// every day shouldn't watch them age out from under it.
func TestBuildingAContextMarksWhatItKept(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	old := time.Unix(1700000000, 0)
	now := time.Now()

	kept := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "The count query is unindexed",
		Content: "It scans the whole table.", Importance: 4, CreatedAt: old})
	// Far more episodic memories than a tiny budget can hold, so the section
	// they would be in is given up whole.
	var dropped []memory.Memory
	for i := range 5 {
		dropped = append(dropped, add(t, s, memory.Memory{Kind: memory.KindEpisodic,
			Title:   "Something happened, number " + string(rune('a'+i)),
			Content: strings.Repeat("a long and forgettable account of it. ", 40), Importance: 4, CreatedAt: old}))
	}

	built, err := s.BuildContext(ctx, memory.ContextRequest{
		Project: "pawly", Query: "count query", Budget: memory.MinBudgetTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	if built.Empty() {
		t.Fatal("the build kept nothing at all")
	}
	if got, err := s.Memory(ctx, "pawly", kept.ID); err != nil || got.ReferencedAt.IsZero() {
		t.Fatalf("the open issue was spent on a context but not marked: %+v, %v", got, err)
	}

	// Ten days later — inside the decay window the reference just reset, and
	// long past the one the ancient created_at would have fallen out of —
	// what the build read is left alone and what it never showed anybody ages.
	pass, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{Now: now.Add(10 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Memory(ctx, "pawly", kept.ID); got.Importance != 4 {
		t.Errorf("a memory the brief keeps spending context on decayed to %d", got.Importance)
	}
	if pass.MemoriesDecayed == 0 {
		t.Errorf("nothing decayed, though %d memories were never put in front of anything", len(dropped))
	}
}

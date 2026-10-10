package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agentbox/internal/memory"
)

func feedback(t *testing.T, s *memory.Store, ref, verdict, why string) memory.FeedbackResult {
	t.Helper()
	out, err := s.Feedback(context.Background(), memory.FeedbackRequest{
		Project: "pawly", Memory: ref, Verdict: verdict, Why: why, Agent: "agent-07", By: "agent-07",
	})
	if err != nil {
		t.Fatalf("Feedback(%s, %s): %v", ref, verdict, err)
	}
	return out
}

func TestWrongAndStaleDropAMemoryToTheFloor(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	port := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "The API listens on 7777", Importance: 5})
	decision := add(t, s, memory.Memory{Kind: memory.KindDecision, Title: "Agents never merge", Importance: 4})
	issue := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "Flaky TestRetire on CI", Importance: 4})

	out := feedback(t, s, port.ID, memory.FeedbackWrong, "it moved to 8080 in #240")
	if out.Was != 5 || out.Memory.Importance != memory.MinImportance || out.Resolved || !out.Memory.Live() {
		t.Errorf("wrong should drop it to the floor and leave it live, got was %d, %+v", out.Was, out.Memory)
	}

	// Stale closes an open item, and only drops anything else.
	if out := feedback(t, s, decision.ID, memory.FeedbackStale, "the user merges now"); out.Resolved || out.Memory.Importance != memory.MinImportance {
		t.Errorf("a stale decision should drop and stay live, got %+v", out)
	}
	out = feedback(t, s, issue.ID, memory.FeedbackStale, "fixed in #241")
	if !out.Resolved || !out.Memory.Resolved() || !strings.Contains(out.Memory.ResolvedBy, "agent-07") ||
		!strings.Contains(out.Memory.ResolvedBy, "#241") {
		t.Errorf("a stale issue should be resolved, saying who and why, got %+v", out.Memory)
	}

	// Who and why are an event.
	events, err := s.Events(ctx, "pawly", memory.EventFilter{Types: []string{memory.EventMemoryFeedback}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("%d feedback events, want 3", len(events))
	}
	var payload map[string]any
	if err := json.Unmarshal(events[2].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if events[2].Agent != "agent-07" || payload["memory"] != port.ID || payload["verdict"] != "wrong" ||
		payload["why"] != "it moved to 8080 in #240" || payload["by"] != "agent-07" {
		t.Errorf("the event should name the memory, verdict, who and why, got %s by %q", events[2].Payload, events[2].Agent)
	}
}

func TestHelpfulRaisesAMemoryAPointUpToTheCeiling(t *testing.T) {
	s := open(t)
	m := add(t, s, memory.Memory{Kind: memory.KindDiscovery, Title: "go test needs CGO_ENABLED=0 here"})

	out := feedback(t, s, m.ID, memory.FeedbackHelpful, "")
	if out.Was != memory.DefaultImportance || out.Memory.Importance != memory.DefaultImportance+1 {
		t.Errorf("helpful should add a point, got %d from %d", out.Memory.Importance, out.Was)
	}
	if out.Memory.ReferencedAt.IsZero() {
		t.Error("helpful should count as the memory having been read, which keeps decay off it")
	}
	// However many readers agree, feedback never makes a memory a 5.
	for range 3 {
		out = feedback(t, s, m.ID, memory.FeedbackHelpful, "")
	}
	if out.Memory.Importance != memory.HelpfulCeiling {
		t.Errorf("helpful went to %d, past the ceiling of %d", out.Memory.Importance, memory.HelpfulCeiling)
	}
	five := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "Never publish the image", Importance: 5})
	if out := feedback(t, s, five.ID, memory.FeedbackHelpful, ""); out.Memory.Importance != 5 {
		t.Errorf("helpful lowered a 5 to %d", out.Memory.Importance)
	}
}

func TestFeedbackFindsAMemoryByItsTitle(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	m := add(t, s, memory.Memory{Kind: memory.KindProject, Title: "Builds go through scripts/release-build.sh"})
	add(t, s, memory.Memory{Kind: memory.KindProject, Title: "The hub is private"})
	add(t, s, memory.Memory{Kind: memory.KindDecision, Title: "The hub is private"})

	// As a brief shows it: bold, in another case.
	if out := feedback(t, s, "**builds go through scripts/release-build.sh**", memory.FeedbackHelpful, ""); out.Memory.ID != m.ID {
		t.Errorf("found %s by title, want %s", out.Memory.ID, m.ID)
	}
	_, err := s.Feedback(ctx, memory.FeedbackRequest{Project: "pawly", Memory: "The hub is private", Verdict: memory.FeedbackHelpful})
	if err == nil || !strings.Contains(err.Error(), "say which by its id") {
		t.Errorf("a title on two memories should be refused, got %v", err)
	}
	_, err = s.Feedback(ctx, memory.FeedbackRequest{Project: "pawly", Memory: "Nothing called this", Verdict: memory.FeedbackHelpful})
	if !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("an unknown memory should be not found, got %v", err)
	}
}

func TestFeedbackIsRefusedWhenItSaysNothing(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	m := add(t, s, memory.Memory{Kind: memory.KindIssue, Title: "Desktop build is slow"})
	for _, req := range []memory.FeedbackRequest{
		{Project: "pawly", Memory: m.ID, Verdict: "meh", Why: "x"},
		{Project: "pawly", Memory: m.ID, Verdict: memory.FeedbackWrong},
		{Project: "pawly", Memory: "", Verdict: memory.FeedbackHelpful},
		{Project: "other", Memory: m.ID, Verdict: memory.FeedbackHelpful},
	} {
		if _, err := s.Feedback(ctx, req); err == nil {
			t.Errorf("Feedback(%+v) should be refused", req)
		}
	}
	if _, err := s.ResolveMemory(ctx, "pawly", m.ID, "fixed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Feedback(ctx, memory.FeedbackRequest{Project: "pawly", Memory: m.ID, Verdict: memory.FeedbackStale, Why: "x"}); err == nil {
		t.Error("feedback on a closed memory should be refused")
	}
}

func TestMergedRestatementsAreCountedAsConfirmations(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	m := seed(t, s, time.Now(), staleStore)
	if _, err := s.MergeDuplicates(ctx, "pawly"); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{
		"names-reused": 2, "model-help-2": 1, "awaiting-3": 2, "containers": 0, "fact-names": 0,
	} {
		got, err := s.Memory(ctx, "pawly", m[key].ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Confirmations != want {
			t.Errorf("%s has %d confirmations, want %d", key, got.Confirmations, want)
		}
	}

	// Wrong takes them away with the importance: a popular memory that is
	// wrong must not keep ranking on its popularity.
	if out := feedback(t, s, m["names-reused"].ID, memory.FeedbackWrong, "fixed by a per-project counter"); out.Memory.Confirmations != 0 {
		t.Errorf("wrong left %d confirmations", out.Memory.Confirmations)
	}
}

func TestExactRestatementsAreConfirmationsAndCarryTheirCount(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	old := time.Unix(1700000000, 0)
	title := "Migrations are appended, never edited"
	add(t, s, memory.Memory{Kind: memory.KindDecision, Title: title, Content: "internal/state", CreatedAt: old})
	add(t, s, memory.Memory{Kind: memory.KindDecision, Title: title, Content: "internal/state, by user_version.",
		CreatedAt: old.Add(time.Hour)})
	third := add(t, s, memory.Memory{Kind: memory.KindDecision, Title: title,
		Content: "internal/state, by user_version. Editing one breaks every install.", CreatedAt: old.Add(2 * time.Hour)})

	if _, err := s.Consolidate(ctx, "pawly", memory.ConsolidateOptions{DecayAfter: -1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Memory(ctx, "pawly", third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Confirmations != 2 || got.Standing() != memory.DefaultImportance+1 {
		t.Errorf("said three times, it has %d confirmations and standing %d", got.Confirmations, got.Standing())
	}

	// An ordinary decision said three times is in everybody's context, as a
	// 4 would be.
	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "pawly"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built.Text, title) {
		t.Errorf("a confirmed decision is missing from the context:\n%s", built.Text)
	}
}

func TestStandingCountsConfirmationsOncePointAtMost(t *testing.T) {
	for _, c := range []struct{ importance, confirmations, want int }{
		{3, 0, 3}, {3, 1, 3}, {3, 2, 4}, {3, 50, 4}, {5, 9, 5}, {1, 2, 2},
	} {
		m := memory.Memory{Importance: c.importance, Confirmations: c.confirmations}
		if got := m.Standing(); got != c.want {
			t.Errorf("importance %d, %d confirmations: standing %d, want %d", c.importance, c.confirmations, got, c.want)
		}
	}
}

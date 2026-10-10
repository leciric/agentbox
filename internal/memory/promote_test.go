package memory_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"agentbox/internal/memory"
)

// serve writes the brief of each of these agents, the way the brief writer
// does: a build for an agent, naming it.
func serve(t *testing.T, s *memory.Store, query string, agents ...string) {
	t.Helper()
	for _, a := range agents {
		if _, err := s.BuildContext(context.Background(), memory.ContextRequest{
			Project: "pawly", Query: query, For: memory.ForAgent, Agent: a,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func suggested(t *testing.T, s *memory.Store) map[string]int {
	t.Helper()
	found, err := s.NoteSuggestions(context.Background(), "pawly", 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, n := range found {
		out[n.Title] = n.Agents
	}
	return out
}

func agents(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("agent-%02d", i+1)
	}
	return out
}

func TestAMemoryServedToEnoughAgentsIsOfferedAsANote(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	rule := add(t, s, memory.Memory{Project: "pawly", Kind: memory.KindProject, Importance: 5,
		Title: "Migrations are appended, never edited", Content: "Tracked with PRAGMA user_version."})
	// In every brief too, and never a rule: an open issue is waiting to be
	// fixed, however many agents read it.
	add(t, s, memory.Memory{Project: "pawly", Kind: memory.KindIssue, Importance: 5,
		Title: "The staging API is down", Content: "Since Monday."})

	serve(t, s, "reminders", agents(memory.PromoteAfterAgents-1)...)
	// One agent's brief rewritten again is still one agent.
	serve(t, s, "reminders", "agent-01", "agent-01")
	// A brief that is only being shown reaches nobody, and the lead's recap
	// isn't an agent's brief.
	serve(t, s, "reminders", "")
	if _, err := s.BuildContext(ctx, memory.ContextRequest{Project: "pawly", For: memory.ForLead}); err != nil {
		t.Fatal(err)
	}
	if got := suggested(t, s); len(got) != 0 {
		t.Fatalf("NoteSuggestions() below the threshold = %v, want none", got)
	}

	serve(t, s, "reminders", agents(memory.PromoteAfterAgents)...)
	got := suggested(t, s)
	if len(got) != 1 || got[rule.Title] != memory.PromoteAfterAgents {
		t.Fatalf("NoteSuggestions() = %v, want only %q, served to %d agents", got, rule.Title, memory.PromoteAfterAgents)
	}

	// The lead's brief offers it, and keeps offering it until it is
	// answered — or until the offer lapses, after which it is never made
	// again.
	for range 2 {
		offered, err := s.OfferNoteSuggestions(ctx, "pawly")
		if err != nil {
			t.Fatal(err)
		}
		if len(offered) != 1 || offered[0].Promotion != memory.PromotionOffered {
			t.Fatalf("OfferNoteSuggestions() = %+v, want the rule, offered", offered)
		}
	}
	if err := s.AgeOffer(ctx, "pawly", rule.ID, memory.OfferLasts); err != nil {
		t.Fatal(err)
	}
	serve(t, s, "reminders", agents(memory.PromoteAfterAgents+3)...)
	if got := suggested(t, s); len(got) != 0 {
		t.Errorf("NoteSuggestions() after the offer lapsed = %v, want none", got)
	}
}

func TestAPromotedMemoryIsNoLongerServed(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	rule := add(t, s, memory.Memory{Project: "pawly", Kind: memory.KindDecision, Importance: 4,
		Title: "Pets are soft-deleted", Content: "Set deleted_at; never DELETE a pet row."})
	serve(t, s, "pets", agents(memory.PromoteAfterAgents)...)

	promoted, err := s.PromoteMemory(ctx, "pawly", rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Promotion != memory.PromotionPromoted || promoted.PromotionAt.IsZero() {
		t.Errorf("PromoteMemory() = %q at %v, want promoted, stamped", promoted.Promotion, promoted.PromotionAt)
	}
	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "pawly", Query: "pets soft-deleted", For: memory.ForAgent, Agent: "agent-09"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(built.Text, rule.Title) {
		t.Errorf("an agent's context still serves the promoted memory:\n%s", built.Text)
	}
	// The notes say it now; a search still finds the memory behind them.
	found, err := s.Search(ctx, "pawly", "soft-deleted", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Memories) != 1 || found.Memories[0].ID != rule.ID {
		t.Errorf("Search() after promotion = %+v, want the memory", found.Memories)
	}
	if got := suggested(t, s); len(got) != 0 {
		t.Errorf("NoteSuggestions() after promotion = %v, want none", got)
	}
	if _, err := s.PromoteMemory(ctx, "pawly", rule.ID); err == nil {
		t.Error("PromoteMemory() twice succeeded, want a refusal")
	}
	if _, err := s.DismissPromotion(ctx, "pawly", rule.ID); err == nil {
		t.Error("DismissPromotion() of a note succeeded, want a refusal")
	}
}

func TestADismissedMemoryStaysAMemoryAndIsNeverOfferedAgain(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	m := add(t, s, memory.Memory{Project: "pawly", Kind: memory.KindProject, Importance: 4,
		Title: "The reminders rewrite is in progress", Content: "Until it lands, edit both reminder paths."})
	serve(t, s, "reminders", agents(memory.PromoteAfterAgents)...)

	if _, err := s.DismissPromotion(ctx, "pawly", m.ID); err != nil {
		t.Fatal(err)
	}
	serve(t, s, "reminders", agents(memory.PromoteAfterAgents*2)...)
	if got := suggested(t, s); len(got) != 0 {
		t.Errorf("NoteSuggestions() after a dismissal = %v, want none", got)
	}
	built, err := s.BuildContext(ctx, memory.ContextRequest{Project: "pawly", Query: "reminders", For: memory.ForAgent, Agent: "agent-30"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built.Text, m.Title) {
		t.Errorf("a dismissed memory is no longer served:\n%s", built.Text)
	}
	if _, err := s.PromoteMemory(ctx, "pawly", m.ID); err == nil {
		t.Error("PromoteMemory() of a dismissed memory succeeded, want a refusal")
	}
}

func TestOnlyStandingKindsArePromotable(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	issue := add(t, s, memory.Memory{Project: "pawly", Kind: memory.KindIssue, Title: "The staging API is down"})
	if _, err := s.PromoteMemory(ctx, "pawly", issue.ID); err == nil || !strings.Contains(err.Error(), "an issue memory") {
		t.Errorf("PromoteMemory() of an issue = %v, want a refusal naming its kind", err)
	}
	if _, err := s.PromoteMemory(ctx, "pawly", "mem_nope"); err == nil {
		t.Error("PromoteMemory() of an unknown id succeeded")
	}
}

func TestNoteText(t *testing.T) {
	for _, tc := range []struct{ title, content, want string }{
		{"Pets are soft-deleted.", "Set deleted_at;\n never DELETE a pet row.", "Pets are soft-deleted: Set deleted_at; never DELETE a pet row."},
		{"The API listens on 7777", "", "The API listens on 7777"},
	} {
		if got := memory.NoteText(memory.Memory{Title: tc.title, Content: tc.content}); got != tc.want {
			t.Errorf("NoteText(%q, %q) = %q, want %q", tc.title, tc.content, got, tc.want)
		}
	}
	long := memory.NoteText(memory.Memory{Title: "T", Content: strings.Repeat("word ", 200)})
	if len(long) > 300 || !strings.HasSuffix(long, "…") {
		t.Errorf("NoteText() of a long memory = %d bytes %q, want it cut", len(long), long[len(long)-10:])
	}
}
